package convlog

import "strings"

// 捕获范围。默认只留两个目标真正需要的东西：本轮用户输入与模型输出。
//
// 这不是省几个字节的问题。agent 客户端每轮都把整段历史重发一遍——生产抽样里
// 一个请求 98% 的 input token 是缓存命中——所以按请求存全量等于单会话 O(n²)。
// 而历史里的助手回复早已由前几条记录各自的 output 存过，用户的历史输入也早已由
// 它们各自那一轮存过：留下的只是同一份内容被抄了 N 遍。
const (
	// ScopeEssential 只留本轮真实用户输入与模型输出，丢弃系统提示、工具定义、
	// 注入上下文和重发的历史。
	ScopeEssential = "essential"
	// ScopeFull 保留脱敏后的完整请求。只有将来要做 agent 轨迹蒸馏才需要，
	// 生产抽样上体积约为 essential 的数万倍。
	ScopeFull = "full"
)

// RequestRoles 给完整请求里的每一项标注角色（ScopeFull 专用）。
// 下标对齐 raw_request 中对话数组的原始顺序。
func RequestRoles(protocol string, decoded any) []RoleRef {
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil
	}
	items := conversationItems(protocol, root)
	if len(items) == 0 {
		return nil
	}
	roles := make([]RoleRef, 0, len(items))
	for i, item := range items {
		role, itemType := classifyItem(protocol, item)
		roles = append(roles, RoleRef{Index: i, Role: role, Type: itemType})
	}
	return roles
}

func conversationItems(protocol string, root map[string]any) []any {
	for _, key := range conversationKeys(protocol) {
		if items, ok := root[key].([]any); ok {
			return items
		}
	}
	return nil
}

func conversationKeys(protocol string) []string {
	switch protocol {
	case ProtocolOpenAIResponses:
		return []string{"input"}
	case ProtocolGeminiGenerate:
		return []string{"contents"}
	case ProtocolAnthropicMessages, ProtocolOpenAIChat:
		return []string{"messages"}
	case ProtocolOpenAIImages:
		// 生图请求没有对话数组，prompt 就是全部输入。
		return nil
	default:
		return []string{"messages", "input", "contents"}
	}
}

// classifyItem 判定一项属于谁。显式 role 优先；缺失时按协议惯例推断。
func classifyItem(protocol string, value any) (role, itemType string) {
	item, ok := value.(map[string]any)
	if !ok {
		return RoleUnknown, ""
	}
	itemType, _ = item["type"].(string)
	rawRole, _ := item["role"].(string)

	if strings.TrimSpace(rawRole) != "" {
		return normalizeRoleName(rawRole), itemTypeFor(protocol, itemType)
	}
	switch protocol {
	case ProtocolGeminiGenerate:
		// Gemini 省略 role 时官方语义就是 user。
		return RoleUser, ""
	default:
		return responsesRoleFromType(itemType), itemTypeFor(protocol, itemType)
	}
}

// itemTypeFor 只在项类型真正携带信息的协议上保留它。Anthropic/chat 的角色已经
// 说明了一切，再存一个 "message" 是纯噪音。
func itemTypeFor(protocol, itemType string) string {
	switch protocol {
	case ProtocolOpenAIResponses, ProtocolUnknown, "":
		return itemType
	default:
		return ""
	}
}
