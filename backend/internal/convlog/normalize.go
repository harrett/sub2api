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

// moderationProtocol 把本包的协议标识映射到内容审核模块的标识，以便复用其
// "取最后一条用户输入"的提取器（含 system-reminder 过滤）。
func moderationProtocol(protocol string) string {
	switch protocol {
	case ProtocolAnthropicMessages:
		return service.ContentModerationProtocolAnthropicMessages
	case ProtocolOpenAIChat:
		return service.ContentModerationProtocolOpenAIChat
	case ProtocolOpenAIResponses:
		return service.ContentModerationProtocolOpenAIResponses
	case ProtocolGeminiGenerate:
		return service.ContentModerationProtocolGemini
	default:
		return ""
	}
}

// ExtractPreview 返回用于风控检索的用户输入预览：最后一条用户消息的文本，
// 按 UTF-8 边界截断到 limit 字节。
func ExtractPreview(protocol string, body []byte, limit int) string {
	text := service.ExtractContentModerationText(moderationProtocol(protocol), body)
	return truncateUTF8(text, limit)
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
