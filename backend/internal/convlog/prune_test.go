package convlog

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func decodeAny(t *testing.T, body string) any {
	t.Helper()
	var out any
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	return out
}

// 生产抽样 2（DeepSeek Harness，/v1/chat/completions）里 97.4% 的字节是
// role=user，但真人只打了 59 字节：系统提示、压缩历史、运行时快照、技能目录
// 全被客户端塞进 user 消息。按 role 裁剪对这种客户端完全无效，只有"取最后
// 一条真实用户输入"才能把这轮真正的人类输入摘出来。
func TestLastUserTextSkipsClientInjectedUserMessages(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"You are an AI agent powered by DeepSeek Harness. The harness checkout is at ..."},
		{"role":"user","content":"This is an automatically generated checkpoint condensing an earlier span of the conversation to free context ..."},
		{"role":"assistant","content":"清理完成"},
		{"role":"user","content":"ok，智谱的搞定了，帮我看看workbuddy的"},
		{"role":"user","content":"<system-reminder> The following workspace instructions may be relevant ..."},
		{"role":"user","content":"Current runtime context. This snapshot supersedes earlier runtime-context snapshots. ..."},
		{"role":"user","content":"<system-reminder> The available skill catalog changed. ..."},
		{"role":"user","content":"？"},
		{"role":"user","content":"继续"}
	]}`)

	require.Equal(t, "继续", LastUserText(ProtocolOpenAIChat, body))
	require.Equal(t, "继续", ExtractPreview(ProtocolOpenAIChat, body, 1024))
}

func TestSanitizeUserTextRejectsInjectedMarkers(t *testing.T) {
	injected := []string{
		"<system-reminder>anything</system-reminder>",
		"This is an automatically generated checkpoint condensing an earlier span of the conversation to free context.",
		"Current runtime context. This snapshot supersedes earlier runtime-context snapshots. files=3",
	}
	for _, text := range injected {
		require.Empty(t, sanitizeUserText(text), text)
	}
	require.Equal(t, "真实提问", sanitizeUserText("  真实提问  "))
}

// 预览压成单行便于列表展示，但落盘的用户输入必须保留原始换行——
// 训练要的是用户实际打出来的样子。
func TestLastUserTextPreservesNewlinesWhilePreviewCollapses(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"line one\nline two"}]}`)

	require.Equal(t, "line one\nline two", LastUserText(ProtocolOpenAIChat, body))
	require.Equal(t, "line one line two", ExtractPreview(ProtocolOpenAIChat, body, 1024))
}

// ScopeFull 仍要给完整请求逐项标角色，下标对齐原数组。
func TestRequestRolesCoversEveryItemInOrder(t *testing.T) {
	decoded := decodeAny(t, `{"input":[
		{"type":"additional_tools","role":"developer"},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"reasoning","summary":[]},
		{"type":"custom_tool_call","name":"shell"},
		{"type":"custom_tool_call_output","output":"ok"},
		{"type":"agent_message","content":[]},
		{"type":"mystery"}
	]}`)

	require.Equal(t, []RoleRef{
		{Index: 0, Role: "developer", Type: "additional_tools"},
		{Index: 1, Role: RoleUser, Type: "message"},
		{Index: 2, Role: RoleAssistant, Type: "reasoning"},
		{Index: 3, Role: RoleAssistant, Type: "custom_tool_call"},
		{Index: 4, Role: RoleTool, Type: "custom_tool_call_output"},
		{Index: 5, Role: RoleAssistant, Type: "agent_message"},
		{Index: 6, Role: RoleUnknown, Type: "mystery"},
	}, RequestRoles(ProtocolOpenAIResponses, decoded))
}

// 后缀判断要能覆盖将来新增的 *_call / *_output 项类型，不必每次追列表。
func TestResponsesRoleFromTypeHandlesUnseenTypes(t *testing.T) {
	require.Equal(t, RoleAssistant, responsesRoleFromType("web_search_call"))
	require.Equal(t, RoleTool, responsesRoleFromType("computer_call_output"))
	// 认不出来就明说 unknown。猜错的角色比缺失的角色危害大得多。
	require.Equal(t, RoleUnknown, responsesRoleFromType("some_future_item"))
	require.Equal(t, RoleUnknown, responsesRoleFromType(""))
}

func TestRequestRolesGeminiTreatsMissingRoleAsUser(t *testing.T) {
	decoded := decodeAny(t, `{"contents":[{"parts":[{"text":"hi"}]},{"role":"model","parts":[]}]}`)
	require.Equal(t, []RoleRef{
		{Index: 0, Role: RoleUser},
		{Index: 1, Role: RoleAssistant},
	}, RequestRoles(ProtocolGeminiGenerate, decoded))
}

// conversation 不再携带 system / tools：它们是 raw_request 的逐字副本，
// 且系统提示与工具定义在同一客户端的每个请求里完全相同。
func TestConversationSchemaHasNoSystemOrTools(t *testing.T) {
	encoded, err := json.Marshal(Conversation{Input: "hi", Output: &Output{Role: "assistant"}})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"system"`)
	require.NotContains(t, string(encoded), `"tools"`)
	require.Contains(t, string(encoded), `"input":"hi"`)
}

// 抽样 3（KFlash/Cline 系客户端）把 <environment_details> 追加在用户消息**内部**，
// 里面还带每轮都变的时间戳。整条丢会丢掉"进行修复"，整条留会把时间戳噪音写进语料，
// 所以必须只挖掉标签块。
func TestLastUserTextStripsInlineEnvironmentDetails(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"进行修复\n<environment_details>\nCurrent time: 2026-09-06T09:48:55+08:00\nActive file: AndroidManifest.xml\nVisible files:\n  AndroidManifest.xml\n</environment_details>"}]}`)

	require.Equal(t, "进行修复", LastUserText(ProtocolOpenAIChat, body))
	require.Equal(t, "进行修复", ExtractPreview(ProtocolOpenAIChat, body, 1024))
}

// Claude Code 把 system-reminder 追加进同一条用户消息时同样只挖块，不丢人写的部分。
func TestStripInjectedBlocksKeepsSurroundingHumanText(t *testing.T) {
	require.Equal(t, "帮我改这个",
		sanitizeUserText("帮我改这个\n<system-reminder>internal note</system-reminder>"))
	require.Equal(t, "before after",
		sanitizeUserText("before <environment_details>x</environment_details> after"))
}

// 上游截断会留下没有闭合标签的残缺块，不能让它泄进语料。
func TestStripInjectedBlocksHandlesUnclosedTag(t *testing.T) {
	require.Equal(t, "真实提问",
		sanitizeUserText("真实提问\n<environment_details>\nCurrent time: 2026-09-06T09:48"))
}

// 整条都是注入块时结果为空，扫描继续往前找上一条真实用户输入。
func TestUserMessageThatIsOnlyAnInjectedBlockYieldsNothing(t *testing.T) {
	require.Empty(t, sanitizeUserText("<environment_details>only this</environment_details>"))

	body := []byte(`{"messages":[
		{"role":"user","content":"真实提问"},
		{"role":"user","content":"<environment_details>noise</environment_details>"}
	]}`)
	require.Equal(t, "真实提问", LastUserText(ProtocolOpenAIChat, body))
}

// V2 抽样 user114：AIDE 系客户端把工作区文件树/变更清单以 <workspace_attachment>
// 追加在用户消息内部，在一条记录里占了 62% 的"用户输入"。
func TestLastUserTextStripsWorkspaceAttachment(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"重新检查工作区 不是之前的参考文件 <workspace_attachment>当前对话已附着工作区。\n首次加载工作区：\n├── 📁 jni\n└── 📄 aide_ndk.sh (240B)</workspace_attachment>"}]}`)

	require.Equal(t, "重新检查工作区 不是之前的参考文件", LastUserText(ProtocolOpenAIChat, body))
}
