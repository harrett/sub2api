package convlog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// V2 抽样 user114 连续三条记录的 input 一字不差（同一句"重新检查工作区…"），
// 输出分别是「只有 tool_call」「文本+tool_call」「最终答复」。三条都合法，但把
// 它们当成三个 (指令, 回答) 样本是错的，风控列表里也是三行重复。
func TestIsContinuationDetectsAgentLoopTurns(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		body     string
		want     bool
	}{
		{
			name:     "user message is last: a fresh ask",
			protocol: ProtocolOpenAIChat,
			body: `{"messages":[
				{"role":"system","content":"sys"},
				{"role":"assistant","content":"earlier answer"},
				{"role":"user","content":"重新检查工作区"}
			]}`,
			want: false,
		},
		{
			name:     "tool output after the user turn: a continuation",
			protocol: ProtocolOpenAIChat,
			body: `{"messages":[
				{"role":"user","content":"重新检查工作区"},
				{"role":"assistant","tool_calls":[{"id":"1"}]},
				{"role":"tool","tool_call_id":"1","content":"result"}
			]}`,
			want: true,
		},
		{
			name:     "responses agent loop after the user item",
			protocol: ProtocolOpenAIResponses,
			body: `{"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]},
				{"type":"reasoning","summary":[]},
				{"type":"custom_tool_call_output","output":"ok"}
			]}`,
			want: true,
		},
		{
			name:     "trailing injected user block still counts as a fresh ask",
			protocol: ProtocolOpenAIChat,
			body: `{"messages":[
				{"role":"user","content":"进行修复"},
				{"role":"user","content":"<environment_details>ctx</environment_details>"}
			]}`,
			want: false,
		},
		{
			name:     "no user turn at all",
			protocol: ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"system","content":"sys"}]}`,
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, IsContinuation(tc.protocol, []byte(tc.body)))
		})
	}
}

func TestIsContinuationIgnoresMalformedBodies(t *testing.T) {
	require.False(t, IsContinuation(ProtocolOpenAIChat, nil))
	require.False(t, IsContinuation(ProtocolOpenAIChat, []byte("not json")))
	require.False(t, IsContinuation(ProtocolOpenAIChat, []byte(`{"messages":"nope"}`)))
}

// Codex 并行派发子代理时多个线程共用一个 session_id——V2 抽样里四条记录
// session_id 相同，其中三条是三个不同子代理在并行跑。thread_id 才能把它们分开。
func TestResolveThreadIDSeparatesParallelSubagents(t *testing.T) {
	body := []byte(`{"client_metadata":{
		"session_id":"01a070a3-d4ae-7dd0-8832-e03d1fb4f474",
		"thread_id":"01a07591-d10c-7702-a39c-6d030ec83faf"}}`)

	require.Equal(t, "01a070a3-d4ae-7dd0-8832-e03d1fb4f474", resolveSessionID("", body))
	require.Equal(t, "01a07591-d10c-7702-a39c-6d030ec83faf", resolveThreadID(body))

	require.Empty(t, resolveThreadID([]byte(`{"messages":[]}`)))
	require.Empty(t, resolveThreadID(nil))
}
