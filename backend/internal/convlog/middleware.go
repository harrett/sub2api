package convlog

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Middleware 捕获网关的客户端请求与响应。
//
// 注册位置必须在 RequestBodyLimit 之后：请求体大小限制先生效，本中间件才不会
// 替上游把超限的 body 读进内存。
//
// 关闭时第一条语句即返回，不包 writer、不读 body，热路径零额外分配。
func Middleware(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		maxRequestBytes, maxResponseBytes := svc.Limits()
		if svc == nil || !svc.Enabled() || !capturableRequest(c.Request, maxRequestBytes) || !svc.SampleAllows() {
			c.Next()
			return
		}

		requestBody, readErr := drainRequestBody(c.Request)
		// 无论读成功与否都要把 body 还回去：读失败时连同原始错误一起回放，
		// handler 看到的行为与没有本中间件时完全一致。
		restoreRequestBody(c.Request, requestBody, readErr)

		writer := newCaptureWriter(c.Writer, maxResponseBytes)
		original := c.Writer
		c.Writer = writer
		startedAt := time.Now()

		defer func() {
			// 先还原 writer：外层中间件不应看到本包的包装器。
			if c.Writer == writer {
				c.Writer = original
			}
		}()

		c.Next()

		if readErr != nil || len(requestBody) == 0 || len(requestBody) > maxRequestBytes {
			return
		}
		identity := collectIdentity(c)
		if svc.GroupExcluded(identity.GroupID) {
			return
		}
		responseBody, truncated := writer.snapshot()

		svc.Capture(CaptureInput{
			RequestID:         contextString(c, ctxkey.RequestID),
			SessionID:         service.ExtractClientSessionID(c),
			StartedAt:         startedAt,
			Duration:          time.Since(startedAt),
			StatusCode:        original.Status(),
			Endpoint:          requestPath(c),
			ContentType:       c.GetHeader("Content-Type"),
			Stream:            isStreamingResponse(original),
			RequestBody:       requestBody,
			ResponseBody:      responseBody,
			ResponseTruncated: truncated,
			Identity:          identity,
			RequestedModel:    contextString(c, ctxkey.RequestedPublicModel, ctxkey.Model),
			UpstreamModel:     contextString(c, ctxkey.ResolvedUpstreamModel),
			IPAddress:         c.ClientIP(),
		})
	}
}

// capturableRequest 决定这次请求要不要读进内存。
//
// 带 Content-Encoding 的压缩体一律跳过：解压逻辑属于 httputil，在这里重复实现
// 只会引入分歧。
//
// multipart 有条件放行：/v1/images/edits 走 multipart，整段跳过会让生图编辑
// 完全不可追溯——而那正是上传参考图做违规改图的入口。只在 Content-Length 已知
// 且不超过上限时缓冲，避免把大文件上传读进内存；表单里也只取提示词字段，
// 文件分块连读都不读。
func capturableRequest(req *http.Request, maxRequestBytes int) bool {
	if req == nil || req.Body == nil || req.Method == http.MethodGet {
		return false
	}
	encoding := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Type")))
	switch {
	case contentType == "", strings.Contains(contentType, "json"):
		return true
	case isMultipartContentType(contentType):
		// 长度未知（chunked）时无法预先设限，宁可不捕获。
		return req.ContentLength > 0 && req.ContentLength <= int64(maxRequestBytes)
	default:
		return false
	}
}

// drainRequestBody 读完请求体并返回读到的字节与错误。错误发生时已读部分仍要返回，
// restoreRequestBody 会连同错误一起回放。
func drainRequestBody(req *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	if req.ContentLength > 0 && req.ContentLength < int64(MaxMaxRequestBytes) {
		buf.Grow(int(req.ContentLength))
	}
	_, err := io.Copy(&buf, req.Body)
	_ = req.Body.Close()
	return buf.Bytes(), err
}

func restoreRequestBody(req *http.Request, body []byte, err error) {
	req.Body = &replayBody{reader: bytes.NewReader(body), err: err}
	req.ContentLength = int64(len(body))
}

// replayBody 先回放已缓冲的字节，读完后返回原始读取错误（若有）。
type replayBody struct {
	reader *bytes.Reader
	err    error
}

func (b *replayBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err == io.EOF && b.err != nil {
		return n, b.err
	}
	return n, err
}

func (b *replayBody) Close() error { return nil }

func collectIdentity(c *gin.Context) Identity {
	identity := Identity{
		AccountID: contextInt64(c, ctxkey.AccountID),
		Platform:  contextString(c, ctxkey.Platform),
	}
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		identity.UserID = contextInt64(c, ctxkey.UserID)
		return identity
	}
	identity.APIKeyID = apiKey.ID
	identity.APIKeyName = apiKey.Name
	identity.UserID = apiKey.UserID
	if apiKey.User != nil {
		identity.UserEmail = apiKey.User.Email
	}
	if apiKey.GroupID != nil {
		identity.GroupID = *apiKey.GroupID
	}
	if apiKey.Group != nil {
		identity.GroupName = apiKey.Group.Name
		if identity.Platform == "" {
			identity.Platform = apiKey.Group.Platform
		}
	}
	return identity
}

func isStreamingResponse(writer gin.ResponseWriter) bool {
	if writer == nil {
		return false
	}
	return strings.Contains(strings.ToLower(writer.Header().Get("Content-Type")), "text/event-stream")
}

func requestPath(c *gin.Context) string {
	if c.Request == nil || c.Request.URL == nil {
		return ""
	}
	return c.Request.URL.Path
}

// contextString 按顺序尝试多个 context key，返回第一个非空值。
func contextString(c *gin.Context, keys ...ctxkey.Key) string {
	if c == nil || c.Request == nil {
		return ""
	}
	ctx := c.Request.Context()
	for _, key := range keys {
		if value, ok := ctx.Value(key).(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func contextInt64(c *gin.Context, key ctxkey.Key) int64 {
	if c == nil || c.Request == nil {
		return 0
	}
	value, _ := c.Request.Context().Value(key).(int64)
	return value
}
