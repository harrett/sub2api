//go:build unit

package handler

// fork 本地测试。单独成文件，避免长期跟随 upstream rebase 时与其测试冲突。

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConcurrencyErrorSplitsUserAndServerResponsibility(t *testing.T) {
	// 这是四条链路里唯一一处两种责任方混在同一函数产出的错误，
	// 分流错了会让用户为服务端的容量问题背锅（或反过来）。
	userStatus, _, userMessage := concurrencyErrorResponse(&ConcurrencyError{SlotType: "user"}, "user")
	require.Contains(t, userMessage, "账户限流问题")
	require.Contains(t, userMessage, "请减少并发请求数")
	require.NotContains(t, userMessage, "服务器侧问题")
	require.Equal(t, rpmErrorStatusForTest(), userStatus, "用户侧短期限流复用 RPM 的状态码开关")

	accountStatus, _, accountMessage := concurrencyErrorResponse(&ConcurrencyError{SlotType: "account"}, "account")
	require.Contains(t, accountMessage, "服务器侧问题")
	require.Contains(t, accountMessage, "与你的账户余额和套餐额度无关")
	require.NotContains(t, accountMessage, "账户限流问题")
	require.Equal(t, http.StatusTooManyRequests, accountStatus, "服务器侧不改状态码")
}

func TestConcurrencyErrorSlotTypeOnErrorWinsOverArgument(t *testing.T) {
	// 与 upstream 一致：错误自带的 SlotType 优先于调用方传入的默认值。
	// 若反推时忽略它，account 槽位耗尽会被误判成用户的锅。
	_, _, message := concurrencyErrorResponse(&ConcurrencyError{SlotType: "account"}, "user")
	require.Contains(t, message, "服务器侧问题")
	require.NotContains(t, message, "账户限流问题")
}

func TestConcurrencyErrorWaitQueueFullIsServerSide(t *testing.T) {
	status, errType, message := concurrencyErrorResponse(&WaitQueueFullError{SlotType: "user"}, "user")
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", errType)
	// 队列满是容量不足，不是用户发得太快 —— 即便槽位是 user 也算服务器侧。
	require.Contains(t, message, "服务器侧问题")
	require.Contains(t, message, "排队请求过多")
}

func TestConcurrencyErrorClientDisconnectStaysUntouched(t *testing.T) {
	// 499：客户端已经断开，补文案没有意义，也不该改状态码。
	status, errType, message := concurrencyErrorResponse(context.Canceled, "user")
	require.Equal(t, statusClientClosedRequest, status)
	require.Equal(t, "api_error", errType)
	require.Equal(t, "context canceled", message)
}

func TestConcurrencyErrorUnknownFallbackIsServerSide(t *testing.T) {
	status, _, message := concurrencyErrorResponse(errors.New("redis down"), "user")
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Contains(t, message, "服务器侧问题")
	require.Contains(t, message, "并发调度服务暂时不可用")
}

func TestConcurrencyGuidanceCodeMapping(t *testing.T) {
	require.Equal(t, userConcurrencyGuidanceCode, concurrencyGuidanceCode(&ConcurrencyError{SlotType: "user"}, "user"))
	require.Equal(t, accountConcurrencyGuidanceCode, concurrencyGuidanceCode(&ConcurrencyError{SlotType: "account"}, "user"))
	require.Equal(t, accountConcurrencyGuidanceCode, concurrencyGuidanceCode(&ConcurrencyError{}, "account"))
	require.Equal(t, waitQueueFullGuidanceCode, concurrencyGuidanceCode(&WaitQueueFullError{SlotType: "user"}, "user"))
	require.Equal(t, concurrencyUnavailableGuidanceCd, concurrencyGuidanceCode(errors.New("boom"), "user"))
	require.Empty(t, concurrencyGuidanceCode(context.Canceled, "user"), "客户端断开不加指引")
}

// rpmErrorStatusForTest 复现 middleware 侧的默认值，避免跨包导出内部变量。
func rpmErrorStatusForTest() int { return http.StatusTooManyRequests }
