package convlog

import (
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// maxSessionIDLength 与 usage_logs.session_id 及本表的列宽一致。超长值直接丢弃
// 而不是截断——截断会让两个不同会话别名成同一个。
const maxSessionIDLength = 255

// bodySessionIDPaths 是各客户端在请求体里放会话标识的位置，按可信度排序。
//
// 请求头能覆盖 Claude Code 与 OpenAI 兼容客户端，但 Codex 只在 body 里带
// （prompt_cache_key 与 client_metadata.session_id 是同一个值），抽样 2/3 的
// 客户端则两处都没有——那种情况退化为按用户 + 时间窗重建上下文。
var bodySessionIDPaths = []string{
	"client_metadata.session_id",
	"prompt_cache_key",
	"session_id",
	"conversation_id",
	"metadata.session_id",
}

// resolveThreadID 取会话内的线程标识。Codex 并行派发子代理时，多个线程共用一个
// session_id——V2 抽样 user1268 的四条记录 session_id 完全相同，其中三条却是三个
// 不同的子代理（A/B/C）在并行跑。只按 session_id 排序会把它们串在一起，
// thread_id 才是"一条线性对话"的正确粒度。
func resolveThreadID(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range []string{"client_metadata.thread_id", "thread_id"} {
		if id := sanitizeSessionID(gjson.GetBytes(body, path).String()); id != "" {
			return id
		}
	}
	return ""
}

// resolveSessionID 优先用请求头解析出的值，其次从请求体里找。
func resolveSessionID(fromHeader string, body []byte) string {
	if id := sanitizeSessionID(fromHeader); id != "" {
		return id
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range bodySessionIDPaths {
		if id := sanitizeSessionID(gjson.GetBytes(body, path).String()); id != "" {
			return id
		}
	}
	return ""
}

// sanitizeSessionID 拒绝含控制字符的值，避免客户端把注入载荷塞进关联字段；
// 也拒绝超长值，防止别名。与 service.ExtractClientSessionID 的处理保持一致。
func sanitizeSessionID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxSessionIDLength || !utf8.ValidString(raw) {
		return ""
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return raw
}
