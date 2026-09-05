package convlog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// agent 流量的最后一个元素几乎总是工具结果而不是用户消息。预览必须往回扫，
// 否则列表里全是"（无文本输入）"，而全文里明明有用户输入。
func TestExtractPreviewScansBackPastToolTail(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		body     string
		want     string
	}{
		{
			name:     "responses ends with function_call_output",
			protocol: ProtocolOpenAIResponses,
			body: `{"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"研究微信群数据获取方案"}]},
				{"type":"function_call","name":"shell","arguments":"{}"},
				{"type":"function_call_output","call_id":"c1","output":"done"}
			]}`,
			want: "研究微信群数据获取方案",
		},
		{
			name:     "responses ends with reasoning item",
			protocol: ProtocolOpenAIResponses,
			body: `{"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"second"}]},
				{"type":"reasoning","summary":[]}
			]}`,
			want: "second",
		},
		{
			name:     "anthropic ends with tool_result",
			protocol: ProtocolAnthropicMessages,
			body: `{"messages":[
				{"role":"user","content":[{"type":"text","text":"帮我逆向这个协议"}]},
				{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}
			]}`,
			want: "帮我逆向这个协议",
		},
		{
			name:     "chat completions ends with tool role",
			protocol: ProtocolOpenAIChat,
			body: `{"messages":[
				{"role":"user","content":"dump the memory"},
				{"role":"assistant","tool_calls":[{"id":"1"}]},
				{"role":"tool","tool_call_id":"1","content":"result"}
			]}`,
			want: "dump the memory",
		},
		{
			name:     "gemini ends with function response",
			protocol: ProtocolGeminiGenerate,
			body: `{"contents":[
				{"role":"user","parts":[{"text":"analyse this binary"}]},
				{"role":"model","parts":[{"functionCall":{"name":"run"}}]},
				{"role":"function","parts":[{"functionResponse":{"name":"run"}}]}
			]}`,
			want: "analyse this binary",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ExtractPreview(tc.protocol, []byte(tc.body), 1024))
		})
	}
}

// 取的必须是最近一条用户输入，不是第一条——风控看的是用户最新在说什么。
func TestExtractPreviewPrefersMostRecentUserTurn(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"older question"},
		{"role":"assistant","content":"answer"},
		{"role":"user","content":"newer question"}
	]}`)
	require.Equal(t, "newer question", ExtractPreview(ProtocolOpenAIChat, body, 1024))
}

// 平台注入的上下文不是用户说的话，出现在预览里会把风控引向错误结论。
func TestExtractPreviewSkipsInjectedPlatformContext(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"real user question"},
		{"role":"user","content":"<system-reminder>internal note</system-reminder>"}
	]}`)
	require.Equal(t, "real user question", ExtractPreview(ProtocolOpenAIChat, body, 1024))
}

// 没有任何用户文本（纯图片/纯工具回合）时仍应返回空串，由前端显示占位文案。
func TestExtractPreviewEmptyWhenNoUserText(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"data":"x"}}]}]}`)
	require.Empty(t, ExtractPreview(ProtocolOpenAIChat, body, 1024))
}

func TestExtractPreviewHandlesPlainStringInput(t *testing.T) {
	require.Equal(t, "just a string",
		ExtractPreview(ProtocolOpenAIResponses, []byte(`{"input":"just a string"}`), 1024))
}

// 协议识别失败时兜底：仍要能从常见字段里挖出用户输入。
func TestExtractPreviewUnknownProtocolFallsBack(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"fallback works"}]}`)
	require.Equal(t, "fallback works", ExtractPreview(ProtocolUnknown, body, 1024))
}
