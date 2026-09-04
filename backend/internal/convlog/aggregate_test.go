package convlog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAggregateAnthropicSSE(t *testing.T) {
	sse := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"model":"claude-opus-4-5","usage":{"input_tokens":10,"cache_read_input_tokens":4}}}`,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"grep"}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"hmm"}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Hello "}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"world"}}`,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
		``,
	}, "\n")

	result := AggregateResponse(ProtocolAnthropicMessages, []byte(sse), false)
	require.Equal(t, "Hello world", result.Output.Text)
	require.Equal(t, "hmm", result.Output.Thinking)
	require.Equal(t, "end_turn", result.Output.StopReason)
	require.Equal(t, "claude-opus-4-5", result.ResponseModel)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadTokens)

	require.Len(t, result.Output.ToolCalls, 1)
	call := result.Output.ToolCalls[0].(map[string]any)
	require.Equal(t, "grep", call["name"])
	require.Equal(t, map[string]any{"q": "x"}, call["arguments"])
}

func TestAggregateOpenAIChatSSEMergesToolCallFragments(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"model":"gpt-5.3","choices":[{"delta":{"content":"Hi"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"ls","arguments":"{\"p\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"/\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":9}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	result := AggregateResponse(ProtocolOpenAIChat, []byte(sse), false)
	require.Equal(t, "Hi", result.Output.Text)
	require.Equal(t, "tool_calls", result.Output.StopReason)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 9, result.Usage.OutputTokens)
	require.Len(t, result.Output.ToolCalls, 1)
	call := result.Output.ToolCalls[0].(map[string]any)
	require.Equal(t, "ls", call["name"])
	require.Equal(t, map[string]any{"p": "/"}, call["arguments"])
}

// Responses 的终帧带完整 response 对象，应覆盖增量拼接的结果。
func TestAggregateOpenAIResponsesPrefersTerminalFrame(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"par"}`,
		`data: {"type":"response.output_text.delta","delta":"tial"}`,
		`data: {"type":"response.completed","response":{"model":"gpt-5.3","status":"completed","output":[{"content":[{"type":"output_text","text":"final answer"}]}],"usage":{"input_tokens":5,"output_tokens":6}}}`,
		``,
	}, "\n")

	result := AggregateResponse(ProtocolOpenAIResponses, []byte(sse), false)
	require.Equal(t, "final answer", result.Output.Text)
	require.Equal(t, "completed", result.Output.StopReason)
	require.Equal(t, "gpt-5.3", result.ResponseModel)
	require.Equal(t, 5, result.Usage.InputTokens)
}

func TestAggregateGeminiSSE(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}],"modelVersion":"gemini-3-pro"}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":8}}`,
		``,
	}, "\n")

	result := AggregateResponse(ProtocolGeminiGenerate, []byte(sse), false)
	require.Equal(t, "Hello", result.Output.Text)
	require.Equal(t, "STOP", result.Output.StopReason)
	require.Equal(t, "gemini-3-pro", result.ResponseModel)
	require.Equal(t, 8, result.Usage.OutputTokens)
}

func TestAggregateNonStreamingAnthropic(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-5","stop_reason":"end_turn",
		"content":[{"type":"thinking","thinking":"t"},{"type":"text","text":"answer"}],
		"usage":{"input_tokens":11,"output_tokens":22}
	}`)

	result := AggregateResponse(ProtocolAnthropicMessages, body, false)
	require.Equal(t, "answer", result.Output.Text)
	require.Equal(t, "t", result.Output.Thinking)
	require.Equal(t, 22, result.Usage.OutputTokens)
	require.False(t, result.Output.Truncated)
}

// 截断标记必须原样传递：半截样本对训练有害，下游要能识别并排除。
func TestAggregateMarksTruncated(t *testing.T) {
	result := AggregateResponse(ProtocolAnthropicMessages, []byte(`data: {"type":"content_bl`), true)
	require.True(t, result.Output.Truncated)
	require.Empty(t, result.Output.Text)
}

// 流式 usage 分多帧下发，后续帧的 0 不能把先前帧记录的真实值清掉。
func TestAggregateKeepsEarlierUsageWhenLaterFrameIsZero(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"a"}}],"usage":{"prompt_tokens":4,"completion_tokens":5}}`,
		`data: {"choices":[{"delta":{"content":"b"}}],"usage":{"prompt_tokens":0,"completion_tokens":0}}`,
		``,
	}, "\n")

	result := AggregateResponse(ProtocolOpenAIChat, []byte(sse), false)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}
