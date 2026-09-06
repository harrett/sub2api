package convlog

import (
	"strings"

	"github.com/tidwall/gjson"
)

// IsContinuation 判断本次请求是否只是 agent 循环的续跑，而不是用户新提了一句。
//
// 判据是结构性的、只看本次请求：最后一条 user 项之后还有没有 assistant / tool 项。
// 有就说明模型这次是在回应工具结果，不是在回应我们存下来的那句用户输入。
//
// 为什么需要它：V2 抽样 user114 连续三条记录的 input 完全相同（同一句
// "重新检查工作区…"），输出分别是「只有 tool_call」「文本+tool_call」「最终答复」。
// 三条都合法，但把它们当成三个 (指令, 回答) 样本去蒸馏是错的——前两条的输出根本
// 不是对那句话的回答。风控列表里它们也会显示成三行一模一样的输入。
//
// 标记而不是丢弃：续跑轮里的模型输出对 agent 轨迹蒸馏有价值，风控也需要看到
// 完整活动。让下游自己按需过滤。
func IsContinuation(protocol string, body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	items := conversationArrayFromBody(protocol, body)
	if !items.IsArray() {
		return false
	}

	lastUser := -1
	array := items.Array()
	for i, item := range array {
		if itemRoleForTurn(protocol, item) == RoleUser {
			lastUser = i
		}
	}
	if lastUser < 0 {
		return false
	}
	for _, item := range array[lastUser+1:] {
		switch itemRoleForTurn(protocol, item) {
		case RoleAssistant, RoleTool:
			return true
		}
	}
	return false
}

func conversationArrayFromBody(protocol string, body []byte) gjson.Result {
	for _, key := range conversationKeys(protocol) {
		if node := gjson.GetBytes(body, key); node.IsArray() {
			return node
		}
	}
	return gjson.Result{}
}

// itemRoleForTurn 与 classifyItem 同源，但直接吃 gjson 结果，省掉一次整体解码。
func itemRoleForTurn(protocol string, item gjson.Result) string {
	if role := item.Get("role").String(); strings.TrimSpace(role) != "" {
		return normalizeRoleName(role)
	}
	if protocol == ProtocolGeminiGenerate {
		return RoleUser
	}
	return responsesRoleFromType(item.Get("type").String())
}
