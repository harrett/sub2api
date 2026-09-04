package convlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T, enabled bool) *Service {
	t.Helper()
	spool := newTestSpool(t, nil)
	settings := &Settings{Enabled: enabled}
	normalizeSettings(settings)
	return &Service{
		spool:    spool,
		sink:     NewSink(spool, nil, 100, DefaultQueueMaxBytes),
		uploader: NewUploader(spool),
		current:  settings,
	}
}

func runCaptureRequest(t *testing.T, svc *Service, req *http.Request, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware(svc))
	router.POST("/v1/messages", handler)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

// 最关键的不变量：中间件读走请求体后必须完整还回去，handler 的行为与没有它时一致。
func TestMiddlewareReplaysRequestBodyToHandler(t *testing.T) {
	svc := newTestService(t, true)
	payload := `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	var seen string
	recorder := runCaptureRequest(t, svc, req, func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		seen = string(body)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	require.Equal(t, payload, seen)
	require.Equal(t, http.StatusOK, recorder.Code)
}

// 关闭时中间件必须完全透明：不包 writer、不动 body。
func TestMiddlewareIsTransparentWhenDisabled(t *testing.T) {
	svc := newTestService(t, false)
	payload := `{"messages":[{"role":"user","content":"hi"}]}`

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	var wrapped bool
	var seen string
	recorder := runCaptureRequest(t, svc, req, func(c *gin.Context) {
		_, wrapped = c.Writer.(*captureWriter)
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		seen = string(body)
		c.String(http.StatusOK, "ok")
	})

	require.False(t, wrapped, "disabled capture must not wrap the response writer")
	require.Equal(t, payload, seen)
	require.Equal(t, http.StatusOK, recorder.Code)

	depth, _, _, _, _, _ := svc.sink.Stats()
	require.Zero(t, depth)
}

func TestMiddlewareSkipsNonJSONAndCompressedBodies(t *testing.T) {
	svc := newTestService(t, true)

	multipart := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("binary"))
	multipart.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	require.False(t, capturableRequest(multipart))

	compressed := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	compressed.Header.Set("Content-Type", "application/json")
	compressed.Header.Set("Content-Encoding", "gzip")
	require.False(t, capturableRequest(compressed))

	runCaptureRequest(t, svc, multipart, func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	depth, _, _, _, _, _ := svc.sink.Stats()
	require.Zero(t, depth, "non-JSON requests must not enter the queue")
}

// 捕获一条完整请求：队列里应出现一条可解析、且带归一化输出的记录。
func TestMiddlewareEnqueuesNormalizedRecord(t *testing.T) {
	svc := newTestService(t, true)
	payload := `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"how do I ship it"}]}`

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	runCaptureRequest(t, svc, req, func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json",
			[]byte(`{"model":"claude-opus-4-5","stop_reason":"end_turn","content":[{"type":"text","text":"like this"}],"usage":{"input_tokens":5,"output_tokens":3}}`))
	})

	rec := <-svc.sink.queue
	require.Equal(t, "how do I ship it", rec.row.InputPreview)
	require.Equal(t, http.StatusOK, rec.row.StatusCode)
	require.Equal(t, ProtocolAnthropicMessages, rec.row.Protocol)
	require.Equal(t, 3, rec.row.OutputTokens)

	var record Record
	require.NoError(t, json.Unmarshal(rec.line, &record))
	require.Equal(t, RecordSchemaVersion, record.SchemaVersion)
	require.NotNil(t, record.Conversation.Output)
	require.Equal(t, "like this", record.Conversation.Output.Text)
	require.Len(t, record.Conversation.Messages, 1)
	require.NotNil(t, record.RawRequest)
}

// 落盘前必须脱敏：请求体里的凭证不能出现在 JSONL 里。
func TestMiddlewareRedactsCredentialsBeforeQueueing(t *testing.T) {
	svc := newTestService(t, true)
	payload := `{"messages":[{"role":"user","content":"hi"}],"api_key":"sk-live-secret"}`

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-live-secret")

	runCaptureRequest(t, svc, req, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	rec := <-svc.sink.queue
	require.NotContains(t, string(rec.line), "sk-live-secret")
	require.Contains(t, string(rec.line), redactedPlaceholder)
}

// 响应缓冲达上限后仍要放行写出，只是把记录标记为截断。
func TestCaptureWriterTruncatesButKeepsStreaming(t *testing.T) {
	recorder := httptest.NewRecorder()
	inner := &fakeResponseWriter{ResponseWriter: recorder}
	writer := newCaptureWriter(inner, 4)

	_, err := writer.Write([]byte("abcdefgh"))
	require.NoError(t, err)

	captured, truncated := writer.snapshot()
	require.Equal(t, "abcd", string(captured))
	require.True(t, truncated)
	require.Equal(t, "abcdefgh", recorder.Body.String(), "客户端必须收到完整响应")
}

// fakeResponseWriter 让 httptest.ResponseRecorder 满足 gin.ResponseWriter。
type fakeResponseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *fakeResponseWriter) Status() int              { return w.status }
func (w *fakeResponseWriter) Size() int                { return w.size }
func (w *fakeResponseWriter) Written() bool            { return w.size > 0 }
func (w *fakeResponseWriter) WriteHeaderNow()          {}
func (w *fakeResponseWriter) Flush()                   {}
func (w *fakeResponseWriter) Pusher() http.Pusher      { return nil }
func (w *fakeResponseWriter) CloseNotify() <-chan bool { return make(chan bool) }
func (w *fakeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack not supported in tests")
}
func (w *fakeResponseWriter) WriteString(s string) (int, error) {
	n, err := w.ResponseWriter.Write([]byte(s))
	w.size += n
	return n, err
}

func (w *fakeResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

func (w *fakeResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
