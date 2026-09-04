package convlog

import (
	"bytes"
	"sync"

	"github.com/gin-gonic/gin"
)

// captureWriter 在把响应写给客户端的同时抄一份到有上限的缓冲区。
//
// 嵌入 gin.ResponseWriter 而不是逐个方法转发：Flush/Hijack/CloseNotify/Pusher 等
// 流式必需的方法必须原样直达底层 writer，任何遗漏都会破坏 SSE。
type captureWriter struct {
	gin.ResponseWriter

	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCaptureWriter(inner gin.ResponseWriter, limit int) *captureWriter {
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	return &captureWriter{ResponseWriter: inner, limit: limit}
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.capture(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

// capture 只抄到上限为止。达到上限后继续放行写出，但标记 truncated——
// 半截样本对训练有害，必须让下游能识别并排除。
func (w *captureWriter) capture(b []byte) {
	if len(b) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return
	}
	if len(b) > remaining {
		w.buf.Write(b[:remaining])
		w.truncated = true
		return
	}
	w.buf.Write(b)
}

// snapshot 返回捕获到的响应字节副本与截断标记。
func (w *captureWriter) snapshot() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, w.buf.Len())
	copy(out, w.buf.Bytes())
	return out, w.truncated
}
