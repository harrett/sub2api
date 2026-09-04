package convlog

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

// AggregateResult 是从客户端响应字节里还原出的模型输出。
type AggregateResult struct {
	Output        Output
	Usage         Usage
	ResponseModel string
}

// AggregateResponse 把捕获到的客户端响应字节还原成归一化输出。
//
// truncated 表示响应缓冲已达上限被截断；此时仍尽力还原已收到的部分，并在输出上打标记，
// 训练侧据此排除半截样本。
func AggregateResponse(protocol string, body []byte, truncated bool) AggregateResult {
	result := AggregateResult{Output: Output{Role: "assistant", Truncated: truncated}}
	if len(body) == 0 {
		return result
	}
	if isSSE(body) {
		aggregateSSE(protocol, body, &result)
	} else {
		aggregateJSON(protocol, body, &result)
	}
	return result
}

// isSSE 判断响应体是否为 Server-Sent Events。非流式响应是单个 JSON 文档，
// 首个非空白字节必为 '{' 或 '['。
func isSSE(body []byte) bool {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return false
	}
	return bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:")) ||
		bytes.Contains(trimmed, []byte("\ndata:"))
}

// forEachSSEData 遍历 SSE 的 data 载荷。截断的尾帧会被丢弃（非法 JSON 直接跳过）。
func forEachSSEData(body []byte, fn func(payload []byte)) {
	for _, rawLine := range bytes.Split(body, []byte("\n")) {
		line := bytes.TrimRight(rawLine, "\r")
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if !gjson.ValidBytes(payload) {
			continue
		}
		fn(payload)
	}
}

func aggregateSSE(protocol string, body []byte, result *AggregateResult) {
	var text, thinking strings.Builder
	toolCalls := newToolCallAccumulator()

	forEachSSEData(body, func(payload []byte) {
		switch protocol {
		case ProtocolAnthropicMessages:
			aggregateAnthropicEvent(payload, &text, &thinking, toolCalls, result)
		case ProtocolOpenAIChat:
			aggregateOpenAIChatEvent(payload, &text, toolCalls, result)
		case ProtocolOpenAIResponses:
			aggregateOpenAIResponsesEvent(payload, &text, result)
		case ProtocolGeminiGenerate:
			aggregateGeminiPayload(payload, &text, result)
		default:
			aggregateGenericEvent(payload, &text, result)
		}
	})

	result.Output.Text = text.String()
	result.Output.Thinking = thinking.String()
	if calls := toolCalls.finish(); len(calls) > 0 {
		result.Output.ToolCalls = calls
	}
}

func aggregateAnthropicEvent(payload []byte, text, thinking *strings.Builder, tools *toolCallAccumulator, result *AggregateResult) {
	switch gjson.GetBytes(payload, "type").String() {
	case "message_start":
		message := gjson.GetBytes(payload, "message")
		if model := message.Get("model").String(); model != "" {
			result.ResponseModel = model
		}
		applyAnthropicUsage(message.Get("usage"), &result.Usage)
	case "content_block_start":
		block := gjson.GetBytes(payload, "content_block")
		if block.Get("type").String() == "tool_use" {
			tools.start(int(gjson.GetBytes(payload, "index").Int()), block.Get("id").String(), block.Get("name").String())
		}
	case "content_block_delta":
		delta := gjson.GetBytes(payload, "delta")
		switch delta.Get("type").String() {
		case "text_delta":
			text.WriteString(delta.Get("text").String())
		case "thinking_delta":
			thinking.WriteString(delta.Get("thinking").String())
		case "input_json_delta":
			tools.appendArgs(int(gjson.GetBytes(payload, "index").Int()), delta.Get("partial_json").String())
		}
	case "message_delta":
		if stop := gjson.GetBytes(payload, "delta.stop_reason").String(); stop != "" {
			result.Output.StopReason = stop
		}
		applyAnthropicUsage(gjson.GetBytes(payload, "usage"), &result.Usage)
	}
}

func aggregateOpenAIChatEvent(payload []byte, text *strings.Builder, tools *toolCallAccumulator, result *AggregateResult) {
	if model := gjson.GetBytes(payload, "model").String(); model != "" {
		result.ResponseModel = model
	}
	choice := gjson.GetBytes(payload, "choices.0")
	text.WriteString(choice.Get("delta.content").String())
	if reasoning := choice.Get("delta.reasoning_content").String(); reasoning != "" {
		// 国产 OpenAI 兼容上游把思维链放在 reasoning_content。
		result.Output.Thinking += reasoning
	}
	if finish := choice.Get("finish_reason").String(); finish != "" {
		result.Output.StopReason = finish
	}
	choice.Get("delta.tool_calls").ForEach(func(_, call gjson.Result) bool {
		index := int(call.Get("index").Int())
		tools.start(index, call.Get("id").String(), call.Get("function.name").String())
		tools.appendArgs(index, call.Get("function.arguments").String())
		return true
	})
	applyOpenAIUsage(gjson.GetBytes(payload, "usage"), &result.Usage)
}

func aggregateOpenAIResponsesEvent(payload []byte, text *strings.Builder, result *AggregateResult) {
	switch gjson.GetBytes(payload, "type").String() {
	case "response.output_text.delta":
		text.WriteString(gjson.GetBytes(payload, "delta").String())
	case "response.reasoning_summary_text.delta":
		result.Output.Thinking += gjson.GetBytes(payload, "delta").String()
	case "response.completed", "response.incomplete", "response.failed":
		// 终帧带完整 response 对象，优先用它覆盖增量拼接的结果。
		response := gjson.GetBytes(payload, "response")
		if !response.Exists() {
			return
		}
		if model := response.Get("model").String(); model != "" {
			result.ResponseModel = model
		}
		if status := response.Get("status").String(); status != "" {
			result.Output.StopReason = status
		}
		if output := response.Get("output"); output.Exists() {
			result.Output.Content = decodeJSON(output.Raw)
			if full := flattenResponsesOutputText(output); full != "" {
				text.Reset()
				text.WriteString(full)
			}
		}
		applyResponsesUsage(response.Get("usage"), &result.Usage)
	}
}

func aggregateGeminiPayload(payload []byte, text *strings.Builder, result *AggregateResult) {
	if model := gjson.GetBytes(payload, "modelVersion").String(); model != "" {
		result.ResponseModel = model
	}
	candidate := gjson.GetBytes(payload, "candidates.0")
	candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
		text.WriteString(part.Get("text").String())
		return true
	})
	if reason := candidate.Get("finishReason").String(); reason != "" {
		result.Output.StopReason = reason
	}
	applyGeminiUsage(gjson.GetBytes(payload, "usageMetadata"), &result.Usage)
}

// aggregateGenericEvent 是未知协议的兜底：收集常见的 delta 文本字段。
func aggregateGenericEvent(payload []byte, text *strings.Builder, result *AggregateResult) {
	for _, path := range []string{"delta.text", "delta", "choices.0.delta.content", "text"} {
		if node := gjson.GetBytes(payload, path); node.Type == gjson.String && node.String() != "" {
			text.WriteString(node.String())
			return
		}
	}
	if model := gjson.GetBytes(payload, "model").String(); model != "" {
		result.ResponseModel = model
	}
}

func aggregateJSON(protocol string, body []byte, result *AggregateResult) {
	if !gjson.ValidBytes(body) {
		return
	}
	switch protocol {
	case ProtocolAnthropicMessages:
		result.ResponseModel = gjson.GetBytes(body, "model").String()
		result.Output.StopReason = gjson.GetBytes(body, "stop_reason").String()
		content := gjson.GetBytes(body, "content")
		result.Output.Content = decodeJSON(content.Raw)
		result.Output.Text = flattenAnthropicContentText(content)
		result.Output.Thinking = flattenAnthropicThinking(content)
		applyAnthropicUsage(gjson.GetBytes(body, "usage"), &result.Usage)
	case ProtocolOpenAIChat:
		result.ResponseModel = gjson.GetBytes(body, "model").String()
		message := gjson.GetBytes(body, "choices.0.message")
		result.Output.StopReason = gjson.GetBytes(body, "choices.0.finish_reason").String()
		result.Output.Text = message.Get("content").String()
		result.Output.Thinking = message.Get("reasoning_content").String()
		if calls := message.Get("tool_calls"); calls.Exists() {
			result.Output.ToolCalls = toAnySlice(decodeJSON(calls.Raw))
		}
		applyOpenAIUsage(gjson.GetBytes(body, "usage"), &result.Usage)
	case ProtocolOpenAIResponses:
		result.ResponseModel = gjson.GetBytes(body, "model").String()
		result.Output.StopReason = gjson.GetBytes(body, "status").String()
		output := gjson.GetBytes(body, "output")
		result.Output.Content = decodeJSON(output.Raw)
		result.Output.Text = flattenResponsesOutputText(output)
		applyResponsesUsage(gjson.GetBytes(body, "usage"), &result.Usage)
	case ProtocolGeminiGenerate:
		result.ResponseModel = gjson.GetBytes(body, "modelVersion").String()
		candidate := gjson.GetBytes(body, "candidates.0")
		result.Output.StopReason = candidate.Get("finishReason").String()
		parts := candidate.Get("content.parts")
		result.Output.Content = decodeJSON(parts.Raw)
		result.Output.Text = flattenGeminiParts(parts)
		applyGeminiUsage(gjson.GetBytes(body, "usageMetadata"), &result.Usage)
	default:
		result.ResponseModel = gjson.GetBytes(body, "model").String()
		result.Output.Content = decodeJSON(string(body))
	}
}

func flattenAnthropicContentText(content gjson.Result) string {
	var parts []string
	content.ForEach(func(_, block gjson.Result) bool {
		if block.Get("type").String() == "text" {
			parts = append(parts, block.Get("text").String())
		}
		return true
	})
	return strings.Join(parts, "")
}

func flattenAnthropicThinking(content gjson.Result) string {
	var parts []string
	content.ForEach(func(_, block gjson.Result) bool {
		if block.Get("type").String() == "thinking" {
			parts = append(parts, block.Get("thinking").String())
		}
		return true
	})
	return strings.Join(parts, "")
}

func flattenResponsesOutputText(output gjson.Result) string {
	var parts []string
	output.ForEach(func(_, item gjson.Result) bool {
		item.Get("content").ForEach(func(_, block gjson.Result) bool {
			if text := block.Get("text").String(); text != "" {
				parts = append(parts, text)
			}
			return true
		})
		return true
	})
	return strings.Join(parts, "")
}

func applyAnthropicUsage(node gjson.Result, usage *Usage) {
	if !node.Exists() {
		return
	}
	setIfPositive(&usage.InputTokens, int(node.Get("input_tokens").Int()))
	setIfPositive(&usage.OutputTokens, int(node.Get("output_tokens").Int()))
	setIfPositive(&usage.CacheReadTokens, int(node.Get("cache_read_input_tokens").Int()))
	setIfPositive(&usage.CacheCreationTokens, int(node.Get("cache_creation_input_tokens").Int()))
}

func applyOpenAIUsage(node gjson.Result, usage *Usage) {
	if !node.Exists() {
		return
	}
	setIfPositive(&usage.InputTokens, int(node.Get("prompt_tokens").Int()))
	setIfPositive(&usage.OutputTokens, int(node.Get("completion_tokens").Int()))
	setIfPositive(&usage.CacheReadTokens, int(node.Get("prompt_tokens_details.cached_tokens").Int()))
}

func applyResponsesUsage(node gjson.Result, usage *Usage) {
	if !node.Exists() {
		return
	}
	setIfPositive(&usage.InputTokens, int(node.Get("input_tokens").Int()))
	setIfPositive(&usage.OutputTokens, int(node.Get("output_tokens").Int()))
	setIfPositive(&usage.CacheReadTokens, int(node.Get("input_tokens_details.cached_tokens").Int()))
}

func applyGeminiUsage(node gjson.Result, usage *Usage) {
	if !node.Exists() {
		return
	}
	setIfPositive(&usage.InputTokens, int(node.Get("promptTokenCount").Int()))
	setIfPositive(&usage.OutputTokens, int(node.Get("candidatesTokenCount").Int()))
	setIfPositive(&usage.CacheReadTokens, int(node.Get("cachedContentTokenCount").Int()))
}

// setIfPositive 只在新值为正时覆盖：流式响应里 usage 会分多帧下发，
// 后续帧的 0 不应把先前帧记录的真实值清掉。
func setIfPositive(target *int, value int) {
	if value > 0 {
		*target = value
	}
}

func toAnySlice(value any) []any {
	if slice, ok := value.([]any); ok {
		return slice
	}
	if value == nil {
		return nil
	}
	return []any{value}
}

// toolCallAccumulator 按 index 拼接流式工具调用：名称与 id 只在首帧出现，
// 参数以 JSON 片段分多帧下发。
type toolCallAccumulator struct {
	order []int
	calls map[int]*toolCallState
}

type toolCallState struct {
	ID   string
	Name string
	Args strings.Builder
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{calls: make(map[int]*toolCallState)}
}

func (a *toolCallAccumulator) start(index int, id, name string) {
	state, ok := a.calls[index]
	if !ok {
		state = &toolCallState{}
		a.calls[index] = state
		a.order = append(a.order, index)
	}
	if id != "" {
		state.ID = id
	}
	if name != "" {
		state.Name = name
	}
}

func (a *toolCallAccumulator) appendArgs(index int, fragment string) {
	if fragment == "" {
		return
	}
	state, ok := a.calls[index]
	if !ok {
		state = &toolCallState{}
		a.calls[index] = state
		a.order = append(a.order, index)
	}
	state.Args.WriteString(fragment)
}

func (a *toolCallAccumulator) finish() []any {
	if len(a.order) == 0 {
		return nil
	}
	out := make([]any, 0, len(a.order))
	for _, index := range a.order {
		state := a.calls[index]
		call := map[string]any{"index": index}
		if state.ID != "" {
			call["id"] = state.ID
		}
		if state.Name != "" {
			call["name"] = state.Name
		}
		if args := state.Args.String(); args != "" {
			// 参数是拼出来的 JSON 文本；能解析就存结构，解析不了（例如被截断）就存原文。
			call["arguments"] = decodeJSON(args)
		}
		out = append(out, call)
	}
	return out
}
