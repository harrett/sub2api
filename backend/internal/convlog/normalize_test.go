package convlog

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectProtocol(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		body     string
		want     string
	}{
		{"anthropic by path", "/v1/messages", `{"messages":[]}`, ProtocolAnthropicMessages},
		{"responses by path", "/v1/responses", `{"input":"hi"}`, ProtocolOpenAIResponses},
		{"chat by path", "/v1/chat/completions", `{"messages":[]}`, ProtocolOpenAIChat},
		{"gemini by path", "/v1beta/models/gemini:generateContent", `{"contents":[]}`, ProtocolGeminiGenerate},
		{"gemini by body", "/unknown", `{"contents":[{"role":"user"}]}`, ProtocolGeminiGenerate},
		{"chat by body", "/unknown", `{"messages":[{"role":"user"}]}`, ProtocolOpenAIChat},
		{"unknown", "/unknown", `not json`, ProtocolUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, DetectProtocol(tc.endpoint, []byte(tc.body)))
		})
	}
}

func TestNormalizeAnthropicRequest(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-5",
		"system":[{"type":"text","text":"you are helpful"}],
		"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],
		"tools":[{"name":"grep"}]
	}`)

	conv := NormalizeRequest(ProtocolAnthropicMessages, body)
	require.Equal(t, "you are helpful", conv.System)
	require.Len(t, conv.Messages, 1)
	require.Equal(t, "user", conv.Messages[0].Role)
	require.NotNil(t, conv.Tools)
}

// OpenAI 把 system 放在 messages 里；归一化后必须提到 Conversation.System，
// 训练侧才不用再按协议分支处理。
func TestNormalizeOpenAIChatLiftsSystemMessage(t *testing.T) {
	body := []byte(`{
		"messages":[
			{"role":"system","content":"be concise"},
			{"role":"user","content":"hi"}
		]
	}`)

	conv := NormalizeRequest(ProtocolOpenAIChat, body)
	require.Equal(t, "be concise", conv.System)
	require.Len(t, conv.Messages, 1)
	require.Equal(t, "user", conv.Messages[0].Role)
}

func TestNormalizeGeminiMapsModelRoleToAssistant(t *testing.T) {
	body := []byte(`{
		"systemInstruction":{"parts":[{"text":"sys"}]},
		"contents":[
			{"role":"user","parts":[{"text":"hi"}]},
			{"role":"model","parts":[{"text":"hello"}]}
		]
	}`)

	conv := NormalizeRequest(ProtocolGeminiGenerate, body)
	require.Equal(t, "sys", conv.System)
	require.Len(t, conv.Messages, 2)
	require.Equal(t, "assistant", conv.Messages[1].Role)
}

func TestExtractPreviewTruncatesOnUTF8Boundary(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"中文中文中文"}]}`)

	preview := ExtractPreview(ProtocolOpenAIChat, body, 7)
	require.True(t, len(preview) <= 7)
	require.True(t, json.Valid([]byte(`"`+preview+`"`)), "preview must stay valid UTF-8")
	require.Equal(t, "中文", preview)
}

func TestRedactJSONRemovesCredentials(t *testing.T) {
	var payload any
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"x",
		"max_tokens":100,
		"api_key":"sk-secret",
		"nested":{"authorization":"Bearer abc","messages":[{"token":"tok"}]}
	}`), &payload))

	redacted := redactJSON(payload).(map[string]any)
	require.Equal(t, redactedPlaceholder, redacted["api_key"])
	// max_tokens 与 api_key 只差一个词，精确键名匹配不能误伤它。
	require.EqualValues(t, 100, redacted["max_tokens"])

	nested := redacted["nested"].(map[string]any)
	require.Equal(t, redactedPlaceholder, nested["authorization"])
	messages := nested["messages"].([]any)
	require.Equal(t, redactedPlaceholder, messages[0].(map[string]any)["token"])
}
