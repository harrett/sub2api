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

// NormalizeRequest 归一化系统提示、工具定义与逐项角色。对话正文不复制——
// 它已经完整存在 raw_request 里，再存一份只是把每条记录体积翻倍。
// 解析失败时返回零值：归一化是尽力而为的，RawRequest 始终保底。
func NormalizeRequest(protocol string, body []byte) Conversation {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return Conversation{}
	}
	var conv Conversation
	switch protocol {
	case ProtocolAnthropicMessages:
		conv.System = flattenTextValue(gjson.GetBytes(body, "system"))
		conv.Roles = rolesFromRoleBearingArray(gjson.GetBytes(body, "messages"))
	case ProtocolOpenAIChat:
		conv.System, conv.Roles = normalizeOpenAIChatRoles(gjson.GetBytes(body, "messages"))
	case ProtocolOpenAIResponses:
		conv.System = gjson.GetBytes(body, "instructions").String()
		conv.Roles = rolesFromResponsesInput(gjson.GetBytes(body, "input"))
	case ProtocolGeminiGenerate:
		conv.System = geminiSystemInstruction(body)
		conv.Roles = rolesFromGeminiContents(gjson.GetBytes(body, "contents"))
	default:
		conv.Roles = rolesFromGenericBody(body)
	}
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() {
		conv.Tools = decodeJSON(tools.Raw)
	}
	return conv
}

// rolesFromRoleBearingArray 处理每项都自带 role 的协议（Anthropic messages）。
func rolesFromRoleBearingArray(array gjson.Result) []RoleRef {
	return collectRoles(array, func(item gjson.Result) (string, string) {
		return normalizeRoleName(item.Get("role").String()), item.Get("type").String()
	})
}

// normalizeOpenAIChatRoles 顺带把 system/developer 消息提到 Conversation.System，
// 与其它协议的视图对齐；这些项在角色索引里仍按原角色标注，下标不会错位。
func normalizeOpenAIChatRoles(messages gjson.Result) (string, []RoleRef) {
	var system string
	roles := collectRoles(messages, func(item gjson.Result) (string, string) {
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		if role == "system" || role == "developer" {
			system = joinNonEmpty(system, flattenTextValue(item.Get("content")))
		}
		return normalizeRoleName(role), ""
	})
	return system, roles
}

func rolesFromResponsesInput(input gjson.Result) []RoleRef {
	if input.Type == gjson.String {
		// input 是裸字符串时整个请求就是一条用户输入。
		return []RoleRef{{Index: 0, Role: RoleUser}}
	}
	return collectRoles(input, func(item gjson.Result) (string, string) {
		itemType := item.Get("type").String()
		if role := item.Get("role").String(); role != "" {
			return normalizeRoleName(role), itemType
		}
		return responsesRoleFromType(itemType), itemType
	})
}

// responsesRoleFromType 推断 Responses 里不带 role 的项属于谁。
//
// Codex 的 agent 循环中这类项占绝大多数（reasoning / *_call / *_output），
// 早先它们统一落到 "user" 兜底上，于是一条只有 2 句真实用户输入的会话被标成
// 65 条 user——拿去蒸馏会让模型学到"工具输出是用户说的话"。
// 后缀判断能覆盖将来新增的 *_call / *_output 类型，认不出就明说 unknown。
func responsesRoleFromType(itemType string) string {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "reasoning", "agent_message":
		return RoleAssistant
	case "":
		return RoleUnknown
	}
	switch {
	case strings.HasSuffix(itemType, "_output"):
		return RoleTool
	case strings.HasSuffix(itemType, "_call"):
		return RoleAssistant
	default:
		return RoleUnknown
	}
}

func rolesFromGeminiContents(contents gjson.Result) []RoleRef {
	return collectRoles(contents, func(item gjson.Result) (string, string) {
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		if role == "" {
			// Gemini 省略 role 时按官方语义就是 user。
			return RoleUser, ""
		}
		return normalizeRoleName(role), ""
	})
}

func geminiSystemInstruction(body []byte) string {
	var system string
	for _, key := range []string{"systemInstruction", "system_instruction"} {
		if node := gjson.GetBytes(body, key); node.Exists() {
			system = joinNonEmpty(system, flattenGeminiParts(node.Get("parts")))
		}
	}
	return system
}

// rolesFromGenericBody 是未知协议的兜底：认得出哪个数组是对话就标注它。
func rolesFromGenericBody(body []byte) []RoleRef {
	for _, key := range []string{"messages", "contents", "input"} {
		if node := gjson.GetBytes(body, key); node.IsArray() {
			return rolesFromRoleBearingArray(node)
		}
	}
	return nil
}

func collectRoles(array gjson.Result, classify func(gjson.Result) (role, itemType string)) []RoleRef {
	if !array.IsArray() {
		return nil
	}
	items := array.Array()
	roles := make([]RoleRef, 0, len(items))
	for i, item := range items {
		role, itemType := classify(item)
		roles = append(roles, RoleRef{Index: i, Role: role, Type: itemType})
	}
	return roles
}

// normalizeRoleName 把各协议的角色名收敛到统一取值。空值返回 unknown 而不是
// 猜一个——猜错的角色比缺失的角色危害大得多。
func normalizeRoleName(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user", "human":
		return RoleUser
	case "assistant", "model":
		return RoleAssistant
	case "tool", "function":
		return RoleTool
	case "":
		return RoleUnknown
	default:
		// system / developer 等原样保留，它们本身就是明确语义。
		return strings.ToLower(strings.TrimSpace(role))
	}
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
