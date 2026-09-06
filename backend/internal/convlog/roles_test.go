package convlog

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// 生产抽样里 77 项 input 有 63 项不带 role，旧的 `role == "" -> user` 兜底把
// 推理、工具调用、工具输出全标成了 user：一条只有 2 句真实用户输入的会话被记成
// 65 条 user。这类语料会让模型学到"工具输出是用户说的话"。
func TestResponsesRolesInferredFromItemType(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"additional_tools","role":"developer"},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]},
		{"type":"custom_tool_call","name":"shell"},
		{"type":"custom_tool_call_output","output":"ok"},
		{"type":"function_call","name":"f"},
		{"type":"function_call_output","output":"ok"},
		{"type":"agent_message","content":[]},
		{"type":"message","role":"assistant","content":[]}
	]}`)

	conv := NormalizeRequest(ProtocolOpenAIResponses, body)
	require.Equal(t, []RoleRef{
		{Index: 0, Role: "developer", Type: "additional_tools"},
		{Index: 1, Role: RoleUser, Type: "message"},
		{Index: 2, Role: RoleAssistant, Type: "reasoning"},
		{Index: 3, Role: RoleAssistant, Type: "custom_tool_call"},
		{Index: 4, Role: RoleTool, Type: "custom_tool_call_output"},
		{Index: 5, Role: RoleAssistant, Type: "function_call"},
		{Index: 6, Role: RoleTool, Type: "function_call_output"},
		{Index: 7, Role: RoleAssistant, Type: "agent_message"},
		{Index: 8, Role: RoleAssistant, Type: "message"},
	}, conv.Roles)
}

// 后缀判断要能覆盖将来新增的 *_call / *_output 项类型，不必每次追列表。
func TestResponsesRoleFromTypeHandlesUnseenTypes(t *testing.T) {
	require.Equal(t, RoleAssistant, responsesRoleFromType("web_search_call"))
	require.Equal(t, RoleAssistant, responsesRoleFromType("local_shell_call"))
	require.Equal(t, RoleTool, responsesRoleFromType("computer_call_output"))
	require.Equal(t, RoleTool, responsesRoleFromType("local_shell_call_output"))
}

// 认不出来就明说 unknown。猜错的角色比缺失的角色危害大得多。
func TestUnrecognizedItemsAreUnknownNotUser(t *testing.T) {
	require.Equal(t, RoleUnknown, responsesRoleFromType("some_future_item"))
	require.Equal(t, RoleUnknown, responsesRoleFromType(""))

	conv := NormalizeRequest(ProtocolOpenAIResponses, []byte(`{"input":[{"type":"mystery"}]}`))
	require.Equal(t, []RoleRef{{Index: 0, Role: RoleUnknown, Type: "mystery"}}, conv.Roles)
}

// 角色索引按下标对齐 raw_request，任何一项都不能被跳过，否则下标全部错位。
func TestRoleIndexStaysAlignedWithRawRequest(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"system","content":"s"},
		{"role":"user","content":"u"},
		{"role":"assistant","content":"a"},
		{"role":"tool","content":"t"}
	]}`)

	conv := NormalizeRequest(ProtocolOpenAIChat, body)
	require.Len(t, conv.Roles, 4)
	for i, ref := range conv.Roles {
		require.Equal(t, i, ref.Index)
	}
	require.Equal(t, "system", conv.Roles[0].Role)
	require.Equal(t, RoleTool, conv.Roles[3].Role)
}

// 正文只应存在一份。conversation 里出现对话正文就意味着又开始存两遍了。
func TestConversationCarriesNoMessageBodies(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"UNIQUE-PROMPT-MARKER"}]}
	]}`)

	conv := NormalizeRequest(ProtocolOpenAIResponses, body)
	encoded, err := json.Marshal(conv)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "UNIQUE-PROMPT-MARKER")
}

// encrypted_content 是上游的不透明密文，人和模型都读不了，只占体积。
func TestRedactJSONDropsOpaqueEncryptedContent(t *testing.T) {
	var payload any
	require.NoError(t, json.Unmarshal([]byte(`{"input":[
		{"type":"reasoning","encrypted_content":"gAAAAAB-opaque-blob","summary":[{"text":"keep me"}]}
	]}`), &payload))

	cleaned := redactJSON(payload).(map[string]any)
	item := cleaned["input"].([]any)[0].(map[string]any)

	_, present := item["encrypted_content"]
	require.False(t, present, "opaque blobs must be dropped, not merely redacted")
	require.NotNil(t, item["summary"], "the useful reasoning summary must survive")
}
