package convlog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 请求头优先；Codex 只在 body 里带会话标识，抽样 2/3 的客户端两处都没有。
func TestResolveSessionID(t *testing.T) {
	codexBody := []byte(`{"prompt_cache_key":"01a07589-b523-72a2-a801-f48c22179794",
		"client_metadata":{"session_id":"01a07589-b523-72a2-a801-f48c22179794"}}`)

	require.Equal(t, "hdr-1", resolveSessionID("hdr-1", codexBody), "header wins")
	require.Equal(t, "01a07589-b523-72a2-a801-f48c22179794", resolveSessionID("", codexBody))
	require.Empty(t, resolveSessionID("", []byte(`{"messages":[]}`)), "no session signal is a valid state")
	require.Empty(t, resolveSessionID("", nil))
	require.Empty(t, resolveSessionID("", []byte("not json")))
}

// 会话标识会进 SQL 与日志关联字段，控制字符与超长值一律拒绝而不是截断——
// 截断会让两个不同会话别名成同一个。
func TestSanitizeSessionIDRejectsUnsafeValues(t *testing.T) {
	require.Empty(t, sanitizeSessionID("bad\nvalue"))
	require.Empty(t, sanitizeSessionID("bad\x00value"))
	require.Empty(t, sanitizeSessionID(string(make([]byte, maxSessionIDLength+1))))
	require.Equal(t, "sess-1", sanitizeSessionID("  sess-1  "))
}
