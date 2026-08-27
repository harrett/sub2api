package handler

// concurrency_error_presentation.go — fork 本地文件（upstream 不存在）。
//
// 并发槽位相关的错误此前直接把内部文案吐给客户端：
//
//	429  "Concurrency limit exceeded for user, please retry later"
//	429  "Too many pending requests, please retry later"
//	503  "Service temporarily unavailable, please retry later"
//
// 用户完全无法判断责任方 —— "Concurrency limit exceeded" 既可能是他自己发得太快
// （user 槽位），也可能是上游账号被占满（account 槽位），两者该做的事完全相反。
//
// 这是四条链路里唯一一处「用户侧」与「服务器侧」混在同一个函数产出的错误，
// 因此必须按 slotType 分流，不能像其它三条那样按单一错误码查表：
//
//	user 槽位    → 账户限流问题：用户降低并发就能解决
//	account 槽位 → 服务器侧问题：上游账号被占满，用户无能为力
//	等待队列满   → 服务器侧问题：容量不足
//	兜底 503     → 服务器侧问题
//
// 责任方判定与其它链路共用 internal/pkg/gatewayerr 的 platformErrorGuidance
// 表，措辞统一。
//
// 包一层而不是改 upstream 函数体，是为了让 upstream 对映射逻辑的后续改动仍能
// 干净合入 —— 那边只有函数名一行属于本 fork。

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/gatewayerr"
)

// 责任方指引的查表键。upstream 的返回值不带错误码，只能从 error 类型反推。
const (
	userConcurrencyGuidanceCode      = "USER_CONCURRENCY_EXCEEDED"
	accountConcurrencyGuidanceCode   = "ACCOUNT_CONCURRENCY_EXCEEDED"
	waitQueueFullGuidanceCode        = "WAIT_QUEUE_FULL"
	concurrencyUnavailableGuidanceCd = "CONCURRENCY_SERVICE_UNAVAILABLE"

	// userConcurrencySlotType 与 gateway_helper.go 里构造 ConcurrencyError 时
	// 使用的槽位名保持一致。
	userConcurrencySlotType = "user"
)

// concurrencyErrorResponse 是全部调用点使用的入口，签名与 upstream 一致。
func concurrencyErrorResponse(err error, slotType string) (int, string, string) {
	status, errType, message := upstreamConcurrencyErrorResponse(err, slotType)

	code := concurrencyGuidanceCode(err, slotType)
	if code == "" {
		// 客户端已断开（499），补文案没有意义，也不该改状态码。
		return status, errType, message
	}
	status, message = gatewayerr.PlatformErrorPresentation(code, status, message)
	return status, errType, message
}

// concurrencyGuidanceCode 按 upstream 的分支顺序反推责任方；返回空串表示不加指引。
func concurrencyGuidanceCode(err error, slotType string) string {
	var waitQueueFullErr *WaitQueueFullError
	if errors.As(err, &waitQueueFullErr) {
		return waitQueueFullGuidanceCode
	}

	var concurrencyErr *ConcurrencyError
	if errors.As(err, &concurrencyErr) {
		// 与 upstream 一致：错误自带的 SlotType 优先于调用方传入的默认值。
		if concurrencyErr.SlotType != "" {
			slotType = concurrencyErr.SlotType
		}
		if slotType == userConcurrencySlotType {
			return userConcurrencyGuidanceCode
		}
		return accountConcurrencyGuidanceCode
	}

	if errors.Is(err, context.Canceled) {
		return ""
	}
	return concurrencyUnavailableGuidanceCd
}
