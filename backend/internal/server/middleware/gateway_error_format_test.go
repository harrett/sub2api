//go:build unit

package middleware

// fork 本地测试，覆盖 gateway_error_format.go。
// 单独成文件是为了长期跟随 upstream rebase 时不与其测试文件产生冲突。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newGatewayErrorTestContext 构造一个只带路径和请求头的 gin.Context。
func newGatewayErrorTestContext(path string, headers map[string]string) *gin.Context {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

func TestGatewayErrorResponseKeepsInternalEnvelopeForPanel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newGatewayErrorTestContext("/api/v1/user/profile", nil)
	status, body := gatewayErrorResponse(c, http.StatusTooManyRequests, "USAGE_LIMIT_EXCEEDED", "daily usage limit exceeded")

	// 后台/面板必须保持内部信封，Vue 前端按 {"code","message"} 解析；
	// 状态码也不能被重映射，否则前端的 429 分支会失效。
	require.Equal(t, http.StatusTooManyRequests, status)
	envelope, ok := body.(ErrorResponse)
	require.True(t, ok, "panel routes must keep the internal ErrorResponse envelope")
	require.Equal(t, "USAGE_LIMIT_EXCEEDED", envelope.Code)
	require.Equal(t, "daily usage limit exceeded", envelope.Message)
}

func TestGatewayErrorResponseSanitizesPanelMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)

	raw := `error: code=429 reason="DAILY_LIMIT_EXCEEDED" message="daily usage limit exceeded" metadata=map[]`
	c := newGatewayErrorTestContext("/api/v1/user/profile", nil)
	status, body := gatewayErrorResponse(c, http.StatusTooManyRequests, "USAGE_LIMIT_EXCEEDED", raw)

	// 信封和状态码不变（前端契约），但内部字符串仍要清洗掉。
	require.Equal(t, http.StatusTooManyRequests, status)
	envelope, ok := body.(ErrorResponse)
	require.True(t, ok)
	require.Equal(t, "USAGE_LIMIT_EXCEEDED", envelope.Code)
	require.Equal(t, "daily usage limit exceeded", envelope.Message)
}

// withSiteURLs 临时覆盖站点地址（两者都是 init 期从环境变量求值的包级变量）。
func withSiteURLs(t *testing.T, purchase, console string) {
	t.Helper()
	prevPurchase, prevConsole := purchaseURL, consoleURL
	purchaseURL, consoleURL = purchase, console
	t.Cleanup(func() { purchaseURL, consoleURL = prevPurchase, prevConsole })
}

func TestGatewayErrorResponseAppendsConfiguredPurchaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSiteURLs(t, "https://example.test/purchase", "https://example.test/dashboard")

	c := newGatewayErrorTestContext("/v1/responses", nil)
	_, body := gatewayErrorResponse(c, http.StatusForbidden, "INSUFFICIENT_BALANCE", "Insufficient account balance")
	encoded, err := json.Marshal(body)
	require.NoError(t, err)

	require.Contains(t, string(encoded), "[访问 https://example.test/purchase]")
	require.NotContains(t, string(encoded), "dashboard", "purchase 已配置时不应回退到控制台地址")
}

func TestGatewayErrorResponseFallsBackToConsoleURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 只配了控制台地址时，购买类指引回退到它，而不是丢掉链接。
	withSiteURLs(t, "https://example.test/dashboard", "https://example.test/dashboard")

	c := newGatewayErrorTestContext("/v1/responses", nil)
	_, body := gatewayErrorResponse(c, http.StatusForbidden, "SUBSCRIPTION_EXPIRED", "subscription has expired")
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "[访问 https://example.test/dashboard]")
}

func TestGatewayErrorResponseOmitsLinkWhenUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSiteURLs(t, "", "")

	c := newGatewayErrorTestContext("/v1/responses", nil)
	_, body := gatewayErrorResponse(c, http.StatusForbidden, "INSUFFICIENT_BALANCE", "Insufficient account balance")
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	var decoded struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	// 未配置时必须干净收尾：文案完整，末尾不留空括号或半截的「[访问 」。
	require.Equal(t,
		"[账户余额问题] Insufficient account balance — 账户余额不足，这不是服务器故障。请充值后重试。",
		decoded.Error.Message)
}

func TestGatewayErrorResponseNeverLinksServerSideCauses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withSiteURLs(t, "https://example.test/purchase", "https://example.test/dashboard")

	// 服务器侧问题引导用户去充值是最糟的体验，必须无链接。
	for _, code := range []string{"API_KEY_AUTH_OVERLOADED", "INTERNAL_ERROR", "SUBSCRIPTION_MAINTENANCE_FAILED"} {
		c := newGatewayErrorTestContext("/v1/responses", nil)
		_, body := gatewayErrorResponse(c, http.StatusServiceUnavailable, code, "boom")
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "example.test", code)
		require.Contains(t, string(encoded), "服务器侧问题", code)
	}
}

func TestNoHardcodedSiteURLInGuidance(t *testing.T) {
	// 防回归：文案里不许再出现写死的域名。
	for code, guidance := range platformErrorGuidance {
		require.NotContains(t, guidance.hint, "http://", code)
		require.NotContains(t, guidance.hint, "https://", code)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	require.Equal(t, "a", firstNonEmpty("a", "b"))
	require.Equal(t, "b", firstNonEmpty("", "b"))
	require.Equal(t, "", firstNonEmpty("", ""))
	require.Equal(t, "", firstNonEmpty())
}

func TestGeminiStatusStringCoversPaymentRequired(t *testing.T) {
	require.Equal(t, "RESOURCE_EXHAUSTED", geminiStatusString(http.StatusPaymentRequired))
	require.Equal(t, "RESOURCE_EXHAUSTED", geminiStatusString(http.StatusTooManyRequests))
	require.Equal(t, "PERMISSION_DENIED", geminiStatusString(http.StatusForbidden))
	require.Equal(t, "INTERNAL", geminiStatusString(http.StatusServiceUnavailable))
}

func TestGatewayErrorResponseUnwrapsApplicationErrorAndLabelsQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 这是 api_key_auth.go 传进来的真实字符串：validateErr.Error()。
	raw := `error: code=429 reason="DAILY_LIMIT_EXCEEDED" message="daily usage limit exceeded" metadata=map[]`
	c := newGatewayErrorTestContext("/v1/responses", nil)
	status, body := gatewayErrorResponse(c, http.StatusTooManyRequests, "USAGE_LIMIT_EXCEEDED", raw)

	require.Equal(t, http.StatusPaymentRequired, status, "quota errors must not stay on 429; clients auto-retry it")

	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	var decoded struct {
		Error struct {
			Message string  `json:"message"`
			Type    string  `json:"type"`
			Param   *string `json:"param"`
			Code    string  `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	require.NotContains(t, decoded.Error.Message, "metadata=map[]", "internal error formatting must not leak")
	require.NotContains(t, decoded.Error.Message, "reason=")
	require.Contains(t, decoded.Error.Message, "daily usage limit exceeded")
	require.Contains(t, decoded.Error.Message, "账户额度问题")
	require.Equal(t, "insufficient_quota", decoded.Error.Type)
	require.Equal(t, "usage_limit_exceeded", decoded.Error.Code)
	require.Nil(t, decoded.Error.Param)
}

func TestGatewayErrorResponseLabelsServerSideCauses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newGatewayErrorTestContext("/v1/responses", nil)
	status, body := gatewayErrorResponse(c, http.StatusServiceUnavailable, "API_KEY_AUTH_OVERLOADED", "API key authentication is temporarily unavailable")

	require.Equal(t, http.StatusServiceUnavailable, status)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	message := string(encoded)

	// 服务器侧问题绝不能引导用户去充值 —— 这正是这次改动要解决的核心困惑。
	require.Contains(t, message, "服务器侧问题")
	require.Contains(t, message, "与你的账户余额和套餐额度无关")
	require.NotContains(t, message, "账户额度问题")
}

func TestGatewayErrorResponseDialectByPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		path        string
		wantDialect gatewayDialect
	}{
		{"codex responses", "/v1/responses", dialectOpenAI},
		{"root responses", "/responses", dialectOpenAI},
		{"codex direct", "/backend-api/codex/responses", dialectOpenAI},
		{"chat completions", "/v1/chat/completions", dialectOpenAI},
		{"images", "/v1/images/generations", dialectOpenAI},
		{"anthropic messages", "/v1/messages", dialectAnthropic},
		{"anthropic count tokens", "/v1/messages/count_tokens", dialectAnthropic},
		{"antigravity messages", "/antigravity/v1/messages", dialectAnthropic},
		{"gemini v1beta", "/v1beta/models/gemini-3-pro:generateContent", dialectGemini},
		{"antigravity v1beta", "/antigravity/v1beta/models/x:generateContent", dialectGemini},
		{"panel api", "/api/v1/admin/accounts", dialectInternal},
		{"unknown path", "/setup/status", dialectInternal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantDialect, detectGatewayDialect(newGatewayErrorTestContext(tc.path, nil)))
		})
	}
}

func TestGatewayErrorResponseDialectByHeaderOnAmbiguousPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// /v1/models 三种客户端都会调，只能靠 SDK 的特征头区分。
	require.Equal(t, dialectGemini,
		detectGatewayDialect(newGatewayErrorTestContext("/v1/models", map[string]string{"x-goog-api-key": "k"})))
	require.Equal(t, dialectAnthropic,
		detectGatewayDialect(newGatewayErrorTestContext("/v1/models", map[string]string{"anthropic-version": "2023-06-01"})))
	require.Equal(t, dialectAnthropic,
		detectGatewayDialect(newGatewayErrorTestContext("/v1/models", map[string]string{"x-api-key": "k"})))
	require.Equal(t, dialectOpenAI,
		detectGatewayDialect(newGatewayErrorTestContext("/v1/models", map[string]string{"authorization": "Bearer sk-x"})))
}

func TestGatewayErrorResponseAnthropicAndGeminiShapes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("anthropic", func(t *testing.T) {
		c := newGatewayErrorTestContext("/v1/messages", nil)
		_, body := gatewayErrorResponse(c, http.StatusForbidden, "INSUFFICIENT_BALANCE", "Insufficient account balance")
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		var decoded struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Equal(t, "error", decoded.Type)
		require.Equal(t, "permission_error", decoded.Error.Type)
		require.Contains(t, decoded.Error.Message, "账户余额问题")
	})

	t.Run("gemini", func(t *testing.T) {
		c := newGatewayErrorTestContext("/v1beta/models/gemini-3-pro:generateContent", nil)
		_, body := gatewayErrorResponse(c, http.StatusForbidden, "INSUFFICIENT_BALANCE", "Insufficient account balance")
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		var decoded struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Equal(t, http.StatusForbidden, decoded.Error.Code)
		require.NotEmpty(t, decoded.Error.Status)
		require.Contains(t, decoded.Error.Message, "账户余额问题")
	})
}

func TestGatewayErrorResponseLeavesUnknownCodesAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newGatewayErrorTestContext("/v1/responses", nil)
	status, body := gatewayErrorResponse(c, http.StatusBadRequest, "SOME_NEW_UPSTREAM_CODE", "something specific happened")

	require.Equal(t, http.StatusBadRequest, status)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "something specific happened")
	// 没有登记指引的错误码不加标签，避免瞎猜责任方。
	require.NotContains(t, string(encoded), "账户")
	require.NotContains(t, string(encoded), "服务器侧")
}

func TestUnwrapApplicationErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "daily limit",
			raw:  `error: code=429 reason="DAILY_LIMIT_EXCEEDED" message="daily usage limit exceeded" metadata=map[]`,
			want: "daily usage limit exceeded",
		},
		{
			name: "with cause",
			raw:  `error: code=403 reason="SUBSCRIPTION_EXPIRED" message="subscription has expired" metadata=map[] cause=sql: no rows`,
			want: "subscription has expired",
		},
		{
			name: "escaped quotes survive unquoting",
			raw:  `error: code=400 reason="BAD" message="model \"gpt-5\" not allowed" metadata=map[]`,
			want: `model "gpt-5" not allowed`,
		},
		{
			name: "plain message untouched",
			raw:  "Insufficient account balance",
			want: "Insufficient account balance",
		},
		{
			name: "malformed wrapper falls back to raw",
			raw:  "error: code=429 reason=BROKEN",
			want: "error: code=429 reason=BROKEN",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, unwrapApplicationErrorMessage(tc.raw))
		})
	}
}

func TestHasPathPrefixMatchesOnSegmentBoundary(t *testing.T) {
	require.True(t, hasPathPrefix("/v1/models", "/v1/models"))
	require.True(t, hasPathPrefix("/v1/models/gpt-5", "/v1/models"))
	require.False(t, hasPathPrefix("/v1/modelsX", "/v1/models"))
	require.False(t, hasPathPrefix("/v1/model", "/v1/models"))
}

func TestEnvStatusCodeRejectsInvalidValues(t *testing.T) {
	t.Setenv("SUB2API_TEST_STATUS", "")
	require.Equal(t, http.StatusPaymentRequired, envStatusCode("SUB2API_TEST_STATUS", http.StatusPaymentRequired))

	t.Setenv("SUB2API_TEST_STATUS", "400")
	require.Equal(t, http.StatusBadRequest, envStatusCode("SUB2API_TEST_STATUS", http.StatusPaymentRequired))

	t.Setenv("SUB2API_TEST_STATUS", "not-a-number")
	require.Equal(t, http.StatusPaymentRequired, envStatusCode("SUB2API_TEST_STATUS", http.StatusPaymentRequired))

	t.Setenv("SUB2API_TEST_STATUS", "200")
	require.Equal(t, http.StatusPaymentRequired, envStatusCode("SUB2API_TEST_STATUS", http.StatusPaymentRequired), "non-error status must be rejected")
}
