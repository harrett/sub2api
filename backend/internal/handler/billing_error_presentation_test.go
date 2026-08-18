//go:build unit

package handler

// fork 本地测试。单独成文件，避免长期跟随 upstream rebase 时与其测试冲突。

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBillingErrorDetailsMakesLongWindowQuotaNonRetryable(t *testing.T) {
	// 这几类窗口重置要等数小时到数十天，429 会被客户端吞进重试链，
	// 用户永远看不到真实原因。
	cases := []struct {
		name string
		err  error
	}{
		{"platform daily", service.ErrUserPlatformDailyQuotaExhausted},
		{"platform weekly", service.ErrUserPlatformWeeklyQuotaExhausted},
		{"platform monthly", service.ErrUserPlatformMonthlyQuotaExhausted},
		{"api key 5h", service.ErrAPIKeyRateLimit5hExceeded},
		{"api key 1d", service.ErrAPIKeyRateLimit1dExceeded},
		{"api key 7d", service.ErrAPIKeyRateLimit7dExceeded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstreamStatus, _, _, _ := upstreamBillingErrorDetails(tc.err)
			require.Equal(t, http.StatusTooManyRequests, upstreamStatus, "upstream 原始映射应仍是 429")

			status, _, message, _ := billingErrorDetails(tc.err)
			require.NotEqual(t, http.StatusTooManyRequests, status, "长周期配额不能停在 429")
			require.Equal(t, http.StatusPaymentRequired, status)
			require.Contains(t, message, "账户额度问题")
			require.Contains(t, message, "这不是服务器故障")
		})
	}
}

func TestBillingErrorDetailsKeepsRPMRetryable(t *testing.T) {
	// RPM 窗口 60 秒内就重置，429 + Retry-After 让 SDK 自动退避恢复才是对的，
	// 换成不可重试的状态码反而让客户端直接失败。
	for _, err := range []error{service.ErrUserRPMExceeded, service.ErrGroupRPMExceeded} {
		status, _, message, retryAfter := billingErrorDetails(err)
		require.Equal(t, http.StatusTooManyRequests, status)
		require.Positive(t, retryAfter, "RPM 必须保留 Retry-After 供 SDK 退避")
		require.LessOrEqual(t, retryAfter, 60)
		require.Contains(t, message, "账户限流问题")
		// 限流不是花钱能解决的，不应引导去购买。
		require.NotContains(t, message, "升级套餐")
	}
}

func TestBillingErrorDetailsLabelsBillingServiceOutageAsServerSide(t *testing.T) {
	status, _, message, _ := billingErrorDetails(service.ErrBillingServiceUnavailable)
	require.Equal(t, http.StatusServiceUnavailable, status, "服务器侧故障不改状态码")
	require.Contains(t, message, "服务器侧问题")
	require.Contains(t, message, "与你的账户余额和套餐额度无关")
}

func TestBillingErrorDetailsPassesUnknownErrorsThrough(t *testing.T) {
	// 未登记的错误必须是安全的空操作：状态码和文案都不动。
	unknown := errUnknownForBillingTest{}
	wantStatus, _, wantMessage, _ := upstreamBillingErrorDetails(unknown)
	status, _, message, _ := billingErrorDetails(unknown)
	require.Equal(t, wantStatus, status)
	require.Equal(t, wantMessage, message)
}

type errUnknownForBillingTest struct{}

func (errUnknownForBillingTest) Error() string { return "some brand new failure" }
