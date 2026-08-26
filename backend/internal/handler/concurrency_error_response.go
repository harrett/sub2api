package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

const statusClientClosedRequest = 499

// upstreamConcurrencyErrorResponse 是 upstream 的原始映射，函数体保持原样。
// 对外入口 concurrencyErrorResponse 在 concurrency_error_presentation.go 里包了
// 一层，按 slotType 区分责任方后补上标签与可操作指引。
func upstreamConcurrencyErrorResponse(err error, slotType string) (int, string, string) {
	var waitQueueFullErr *WaitQueueFullError
	if errors.As(err, &waitQueueFullErr) {
		return http.StatusTooManyRequests, "rate_limit_error",
			"Too many pending requests, please retry later"
	}

	var concurrencyErr *ConcurrencyError
	if errors.As(err, &concurrencyErr) {
		if concurrencyErr.SlotType != "" {
			slotType = concurrencyErr.SlotType
		}
		return http.StatusTooManyRequests, "rate_limit_error",
			fmt.Sprintf("Concurrency limit exceeded for %s, please retry later", slotType)
	}

	if errors.Is(err, context.Canceled) {
		return statusClientClosedRequest, "api_error", "context canceled"
	}

	return http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable, please retry later"
}
