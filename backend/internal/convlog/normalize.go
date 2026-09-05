package convlog

import (
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

// DetectProtocol 依据入站路径与请求体形态判断协议。
// 路径优先：同一个 body 形态在不同端点上语义不同（例如 /v1/responses 与 /v1/messages）。
func DetectProtocol(endpoint string, body []byte) string {
	path := strings.ToLower(endpoint)
	switch {
	case strings.Contains(path, "/responses"):
		return ProtocolOpenAIResponses
	case strings.Contains(path, "/chat/completions"):
		return ProtocolOpenAIChat
	case strings.Contains(path, "/messages"):
		return ProtocolAnthropicMessages
	case strings.Contains(path, "generatecontent"), strings.Contains(path, "/v1beta/"):
		return ProtocolGeminiGenerate
	}
	if !gjson.ValidBytes(body) {
		return ProtocolUnknown
	}
	switch {
	case gjson.GetBytes(body, "contents").IsArray():
		return ProtocolGeminiGenerate
	case gjson.GetBytes(body, "input").Exists():
		return ProtocolOpenAIResponses
	case gjson.GetBytes(body, "system").Exists():
		return ProtocolAnthropicMessages
	case gjson.GetBytes(body, "messages").IsArray():
		return ProtocolOpenAIChat
	}
	return ProtocolUnknown
}

// ExtractPreview 返回用于风控检索的用户输入预览，按 UTF-8 边界截断到 limit 字节。
//
// 不能复用内容审核的提取器：那套逻辑只看 messages/input 的**最后一个元素**，
// 因为审核关心"用户刚发出的这一句"。风控关心的是"用户到底打了什么字"，而
// agent 流量（Codex/Claude Code）的最后一个元素通常是 function_call_output 或
// tool_result，按审核语义取会得到空串——这正是列表里出现"（无文本输入）"而
// 全文里明明有用户输入的原因。这里改成从后往前找最近一条**真正的用户文本**。
func ExtractPreview(protocol string, body []byte, limit int) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	return truncateUTF8(lastUserText(protocol, body), limit)
}

func lastUserText(protocol string, body []byte) string {
	switch protocol {
	case ProtocolAnthropicMessages, ProtocolOpenAIChat:
		return lastUserTextFromMessages(gjson.GetBytes(body, "messages"))
	case ProtocolOpenAIResponses:
		return lastUserTextFromResponsesInput(gjson.GetBytes(body, "input"))
	case ProtocolGeminiGenerate:
		return lastUserTextFromGeminiContents(gjson.GetBytes(body, "contents"))
	default:
		// 协议识别失败时挨个试，任何一个能取到就用它。
		for _, candidate := range []string{
			lastUserTextFromResponsesInput(gjson.GetBytes(body, "input")),
			lastUserTextFromMessages(gjson.GetBytes(body, "messages")),
			lastUserTextFromGeminiContents(gjson.GetBytes(body, "contents")),
		} {
			if candidate != "" {
				return candidate
			}
		}
		return ""
	}
}

func lastUserTextFromMessages(messages gjson.Result) string {
	return scanBackwards(messages, func(item gjson.Result) string {
		if !isUserRole(item.Get("role").String()) {
			return ""
		}
		return userTextFromContent(item.Get("content"))
	})
}

func lastUserTextFromResponsesInput(input gjson.Result) string {
	if input.Type == gjson.String {
		return sanitizeUserText(input.String())
	}
	return scanBackwards(input, func(item gjson.Result) string {
		if !isUserRole(item.Get("role").String()) {
			return ""
		}
		if text := userTextFromContent(item.Get("content")); text != "" {
			return text
		}
		// Responses 的 input 项也可能直接是 {"type":"input_text","text":...}
		return sanitizeUserText(item.Get("text").String())
	})
}

func lastUserTextFromGeminiContents(contents gjson.Result) string {
	return scanBackwards(contents, func(item gjson.Result) string {
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		if role != "" && role != "user" {
			return ""
		}
		return sanitizeUserText(flattenGeminiParts(item.Get("parts")))
	})
}

// scanBackwards 从数组末尾往前找第一个能提取出文本的元素。
func scanBackwards(array gjson.Result, extract func(gjson.Result) string) string {
	if !array.IsArray() {
		return ""
	}
	items := array.Array()
	for i := len(items) - 1; i >= 0; i-- {
		if text := extract(items[i]); text != "" {
			return text
		}
	}
	return ""
}

// isUserRole 把 Responses 的 message 项与 chat 的 user 消息统一看待。
func isUserRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "user")
}

// userTextFromContent 从 content（字符串或块数组）里取出纯文本，
// 忽略 tool_result / image 这类非用户输入的块。
func userTextFromContent(content gjson.Result) string {
	switch {
	case !content.Exists():
		return ""
	case content.Type == gjson.String:
		return sanitizeUserText(content.String())
	case content.IsArray():
		var parts []string
		content.ForEach(func(_, block gjson.Result) bool {
			switch strings.ToLower(strings.TrimSpace(block.Get("type").String())) {
			case "", "text", "input_text":
				if text := sanitizeUserText(block.Get("text").String()); text != "" {
					parts = append(parts, text)
				}
			}
			return true
		})
		return strings.Join(parts, "\n")
	case content.IsObject():
		return sanitizeUserText(content.Get("text").String())
	default:
		return ""
	}
}

// sanitizeUserText 丢掉平台自己注入的上下文块，只留真正的用户输入。
// 注入内容（system-reminder、Codex 安全策略文档）出现在预览里会把风控人员
// 引向错误结论——他们会以为这些话是用户说的。
func sanitizeUserText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.Contains(text, "<system-reminder>") || service.IsInjectedPlatformPrompt(text) {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

// NormalizeRequest 把客户端请求体解析成统一的对话视图。
// 解析失败时返回零值——归一化是尽力而为的，RawRequest 始终保底。
func NormalizeRequest(protocol string, body []byte) Conversation {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return Conversation{}
	}
	switch protocol {
	case ProtocolAnthropicMessages:
		return normalizeAnthropicRequest(body)
	case ProtocolOpenAIChat:
		return normalizeOpenAIChatRequest(body)
	case ProtocolOpenAIResponses:
		return normalizeOpenAIResponsesRequest(body)
	case ProtocolGeminiGenerate:
		return normalizeGeminiRequest(body)
	default:
		return normalizeGenericRequest(body)
	}
}

func normalizeAnthropicRequest(body []byte) Conversation {
	conv := Conversation{
		System:   flattenTextValue(gjson.GetBytes(body, "system")),
		Messages: messagesFromArray(gjson.GetBytes(body, "messages")),
	}
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() {
		conv.Tools = decodeJSON(tools.Raw)
	}
	return conv
}

func normalizeOpenAIChatRequest(body []byte) Conversation {
	messages := messagesFromArray(gjson.GetBytes(body, "messages"))
	conv := Conversation{}
	// OpenAI 把 system/developer 放在 messages 里；单独抽出来对齐 Anthropic 视图，
	// 训练侧不必再按协议分支处理。
	kept := messages[:0]
	for _, msg := range messages {
		role := strings.ToLower(msg.Role)
		if role == "system" || role == "developer" {
			if text := flattenAnyText(msg.Content); text != "" {
				conv.System = joinNonEmpty(conv.System, text)
			}
			continue
		}
		kept = append(kept, msg)
	}
	conv.Messages = kept
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() {
		conv.Tools = decodeJSON(tools.Raw)
	}
	return conv
}

func normalizeOpenAIResponsesRequest(body []byte) Conversation {
	conv := Conversation{System: gjson.GetBytes(body, "instructions").String()}
	input := gjson.GetBytes(body, "input")
	switch {
	case input.Type == gjson.String:
		conv.Messages = []Message{{Role: "user", Content: input.String()}}
	case input.IsArray():
		conv.Messages = messagesFromArray(input)
	}
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() {
		conv.Tools = decodeJSON(tools.Raw)
	}
	return conv
}

func normalizeGeminiRequest(body []byte) Conversation {
	conv := Conversation{}
	for _, key := range []string{"systemInstruction", "system_instruction"} {
		if node := gjson.GetBytes(body, key); node.Exists() {
			conv.System = joinNonEmpty(conv.System, flattenGeminiParts(node.Get("parts")))
		}
	}
	contents := gjson.GetBytes(body, "contents")
	if contents.IsArray() {
		messages := make([]Message, 0, len(contents.Array()))
		contents.ForEach(func(_, item gjson.Result) bool {
			role := item.Get("role").String()
			if role == "" {
				role = "user"
			}
			if role == "model" {
				role = "assistant"
			}
			messages = append(messages, Message{Role: role, Content: decodeJSON(item.Get("parts").Raw)})
			return true
		})
		conv.Messages = messages
	}
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() {
		conv.Tools = decodeJSON(tools.Raw)
	}
	return conv
}

// normalizeGenericRequest 是未知协议的兜底：把能认出来的 messages/contents/input
// 收进来，认不出就留空，靠 RawRequest 保底。
func normalizeGenericRequest(body []byte) Conversation {
	conv := Conversation{}
	if messages := gjson.GetBytes(body, "messages"); messages.IsArray() {
		conv.Messages = messagesFromArray(messages)
	}
	if conv.Messages == nil {
		if contents := gjson.GetBytes(body, "contents"); contents.IsArray() {
			conv.Messages = messagesFromArray(contents)
		}
	}
	if input := gjson.GetBytes(body, "input"); conv.Messages == nil && input.Type == gjson.String {
		conv.Messages = []Message{{Role: "user", Content: input.String()}}
	}
	return conv
}

func messagesFromArray(node gjson.Result) []Message {
	if !node.IsArray() {
		return nil
	}
	array := node.Array()
	messages := make([]Message, 0, len(array))
	for _, item := range array {
		role := item.Get("role").String()
		if role == "" {
			role = "user"
		}
		content := item.Get("content")
		if !content.Exists() {
			// Responses API 的 input 项可能直接是 {"type":"input_text","text":...}
			content = item
		}
		messages = append(messages, Message{Role: role, Content: decodeJSON(content.Raw)})
	}
	return messages
}

// flattenTextValue 把 string 或 [{type:text,text:...}] 结构压成纯文本。
func flattenTextValue(node gjson.Result) string {
	switch {
	case !node.Exists():
		return ""
	case node.Type == gjson.String:
		return node.String()
	case node.IsArray():
		var parts []string
		node.ForEach(func(_, item gjson.Result) bool {
			if text := item.Get("text").String(); text != "" {
				parts = append(parts, text)
			}
			return true
		})
		return strings.Join(parts, "\n")
	case node.IsObject():
		return node.Get("text").String()
	default:
		return ""
	}
}

func flattenGeminiParts(node gjson.Result) string {
	if !node.IsArray() {
		return node.Get("text").String()
	}
	var parts []string
	node.ForEach(func(_, item gjson.Result) bool {
		if text := item.Get("text").String(); text != "" {
			parts = append(parts, text)
		}
		return true
	})
	return strings.Join(parts, "\n")
}

func flattenAnyText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			if text := flattenAnyText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
		return ""
	default:
		return ""
	}
}

// decodeJSON 把原始 JSON 片段解成 any，失败时返回原始字符串而不是丢弃。
func decodeJSON(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return raw
	}
	return out
}

func joinNonEmpty(existing, addition string) string {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return existing
	}
	if existing == "" {
		return addition
	}
	return existing + "\n" + addition
}

// truncateUTF8 按 UTF-8 边界截断，避免在多字节字符中间切断产生乱码。
func truncateUTF8(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return text[:cut]
}

// utf8Start 判断字节是否为 UTF-8 序列的首字节（非 10xxxxxx 续字节）。
func utf8Start(b byte) bool {
	return b&0xC0 != 0x80
}
