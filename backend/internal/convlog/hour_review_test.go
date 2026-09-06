package convlog

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// 生产一小时实测：608 条 503/429/404 全都没有任何模型输出，其中 596 条根本没绑定到
// 上游账号——既不可蒸馏，也不构成账号封禁风险，却占了 79% 的字节，还被客户端重试
// 反复放大。这类请求不再留存。
func TestCaptureSkipsFailedRequestsWithoutModelOutput(t *testing.T) {
	svc := newTestService(t, true)
	svc.Capture(CaptureInput{
		StatusCode:   503,
		Endpoint:     "/v1/responses",
		RequestBody:  []byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`),
		ResponseBody: []byte(`{"error":{"message":"no available accounts"}}`),
	})

	require.Empty(t, svc.sink.queue, "an output-less failure must not be stored")
	require.EqualValues(t, 1, svc.Runtime().SkippedNoOutput)
}

// 失败但上游确实回了内容时仍要留存：那次调用真的用掉了账号。
func TestCaptureKeepsFailedRequestThatCarriesModelOutput(t *testing.T) {
	svc := newTestService(t, true)
	svc.Capture(CaptureInput{
		StatusCode:  400,
		Endpoint:    "/v1/messages",
		RequestBody: []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		ResponseBody: []byte(`{"model":"claude-opus-4-5","stop_reason":"end_turn",
			"content":[{"type":"text","text":"partial answer"}]}`),
	})

	require.Len(t, svc.sink.queue, 1)
	require.Zero(t, svc.Runtime().SkippedNoOutput)
}

// 生图端点没有对话数组，用户输入就是 prompt。漏掉它等于让风控对生图完全失明。
func TestImageGenerationPromptIsCapturedAsUserInput(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"a red bicycle in the rain","size":"1024x1024"}`)

	require.Equal(t, ProtocolOpenAIImages, DetectProtocol("/v1/images/generations", body))
	require.Equal(t, "a red bicycle in the rain", LastUserText(ProtocolOpenAIImages, body))
	// 生图请求没有对话数组，永远不该被判成 agent 续跑。
	require.False(t, IsContinuation(ProtocolOpenAIImages, body))
}

// 生图响应把整张图以 base64 放在 b64_json，单条可达 MB 级；只留元信息。
func TestImageResponseStoresMetadataNotPixels(t *testing.T) {
	body := []byte(`{"created":1,"size":"1024x1024","data":[
		{"b64_json":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","revised_prompt":"a red bicycle, rainy street"}
	]}`)

	result := AggregateResponse(ProtocolOpenAIImages, body, false)
	encoded, err := json.Marshal(result.Output)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "AAAAAAAA", "base64 pixels must never be persisted")
	require.Contains(t, string(encoded), "a red bicycle, rainy street")
	require.Contains(t, string(encoded), "1024x1024")
}

// b64_json 在任何路径下都要被丢掉，不只是生图分支。
func TestRedactJSONDropsBase64ImagePayloads(t *testing.T) {
	var payload any
	require.NoError(t, json.Unmarshal([]byte(`{"data":[{"b64_json":"SECRETPIXELS","url":"https://x/y.png"}]}`), &payload))
	cleaned := redactJSON(payload).(map[string]any)
	item := cleaned["data"].([]any)[0].(map[string]any)
	_, present := item["b64_json"]
	require.False(t, present)
	require.Equal(t, "https://x/y.png", item["url"])
}

// Codex 的目标续跑指令整块都是机器生成的，28 条记录里占了 263KB 的"用户输入"。
func TestCodexInternalContextIsNotUserInput(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"<codex_internal_context source=\"goal\">\nContinue working toward the active thread goal.\n</codex_internal_context>"}]}`)
	require.Empty(t, LastUserText(ProtocolOpenAIChat, body))
}

// continuation 不能用 omitempty：false 与"旧版本没写这个字段"必须能区分。
func TestContinuationFlagIsAlwaysEmitted(t *testing.T) {
	encoded, err := json.Marshal(Record{SchemaVersion: RecordSchemaVersion})
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"continuation":false`)
}
