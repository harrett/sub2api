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
