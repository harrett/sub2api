package service

// fork 本地测试。单独成文件，避免长期跟随 upstream rebase 时与
// openai_capacity_shed_test.go 产生冲突。

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSanitizeOpenAIResponseFailedEventLabelsCapacityShedMessage(t *testing.T) {
	// 与线上实际抓到的上游原始 payload 一致：response.failed 事件，
	// message 是 OpenAI 自己的过载文案，此前原样透传给客户端。
	payload := `{"type":"response.failed","response":{"error":{"message":"Our servers are currently overloaded. Please try again later."}}}`

	out, changed := sanitizeOpenAIResponseFailedEventForClient([]byte(payload), "response.failed", true)
	require.True(t, changed)

	// upstream 原有的错误码改写必须照常生效（server_is_overloaded/slow_down
	// 之外的降载文案默认没有 code 字段，这里只验证不会被我们的改动破坏）。
	message := gjson.GetBytes(out, "response.error.message").String()
	require.Contains(t, message, "服务器侧问题")
	require.Contains(t, message, "与你的账户余额和套餐额度无关")
	require.Contains(t, message, "Our servers are currently overloaded", "原始上游说明不能丢失，只是追加标签")
}

func TestSanitizeOpenAIResponseFailedEventLabelsBareErrorFrame(t *testing.T) {
	// 上游降载总是先推 error 帧再收 response.failed，两帧携带同一个错误 ——
	// 与 upstream 既有的 code 改写逻辑保持同样的覆盖范围。
	payload := `{"type":"error","error":{"message":"Server is overloaded. Please try again later."}}`

	out, changed := sanitizeOpenAIResponseFailedEventForClient([]byte(payload), "error", true)
	require.True(t, changed)
	message := gjson.GetBytes(out, "error.message").String()
	require.Contains(t, message, "服务器侧问题")
}

func TestSanitizeOpenAIResponseFailedEventPreservesUpstreamCodeRewrite(t *testing.T) {
	// 确认两层改写没有互相冲突：code 字段仍然被 upstream 的逻辑改写为
	// server_error，message 字段被我们的包装层加上标签。
	payload := `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`

	out, changed := sanitizeOpenAIResponseFailedEventForClient([]byte(payload), "response.failed", true)
	require.True(t, changed)
	require.Equal(t, "server_error", gjson.GetBytes(out, "response.error.code").String())
	require.Contains(t, gjson.GetBytes(out, "response.error.message").String(), "服务器侧问题")
}

func TestSanitizeOpenAIResponseFailedEventLeavesNonCapacityErrorsUntouched(t *testing.T) {
	// 未登记到容量降载表里的错误一律不贴标签，避免瞎猜责任方
	// （这类错误如果需要透传规则改写，走的是 applyOpenAIStreamFailedErrorPassthroughRule
	// 那条独立路径，不归这里管）。
	payload := `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"try again in 3s"}}}`

	out, changed := sanitizeOpenAIResponseFailedEventForClient([]byte(payload), "response.failed", true)
	require.False(t, changed)
	require.JSONEq(t, payload, string(out))
}

func TestSanitizeOpenAIResponseFailedEventIgnoresNonTerminalEventTypes(t *testing.T) {
	payload := `{"type":"response.output_text.delta","response":{"error":{"message":"Server is overloaded"}}}`
	out, changed := sanitizeOpenAIResponseFailedEventForClient([]byte(payload), "response.output_text.delta", true)
	require.False(t, changed)
	require.Equal(t, payload, string(out))
}

func TestSanitizeOpenAIResponseFailedEventHandlesMalformedPayloadSafely(t *testing.T) {
	out, changed := sanitizeOpenAIResponseFailedEventForClient([]byte("not-json"), "response.failed", true)
	require.False(t, changed)
	require.Equal(t, "not-json", string(out))

	out, changed = sanitizeOpenAIResponseFailedEventForClient(nil, "response.failed", true)
	require.False(t, changed)
	require.Nil(t, out)
}

func TestLabelOpenAICapacityShedMessageNoErrorPathIsNoop(t *testing.T) {
	// 命中容量降载关键词，但既没有 response.error 也没有 error 字段可写：
	// 必须原样返回，不能崩溃或造出一个不存在的字段。
	payload := `{"type":"response.failed","response":{"status":"failed"}}`
	out, changed := labelOpenAICapacityShedMessage([]byte(payload), "response.failed")
	require.False(t, changed)
	require.Equal(t, payload, string(out))
}
