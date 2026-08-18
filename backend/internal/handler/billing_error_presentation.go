package handler

// billing_error_presentation.go — fork 本地文件（upstream 不存在）。
//
// 计费/限流检查被拒时，upstream 的 upstreamBillingErrorDetails 会把所有限额
// 一律映射成 429。对「用户/分组 RPM」这是对的（窗口 60 秒内重置，Retry-After
// 配合 SDK 自动退避能真正恢复），但对长周期配额是错的：
//
//	平台月配额耗尽 → 429 + Retry-After 可达数万秒
//
// OpenAI 兼容客户端（Codex CLI 等）拿到 429 会在几秒内重试几次然后放弃，
// 打印自己的 "exceeded retry limit, last status: 429 Too Many Requests" 并
// 丢掉响应体 —— 用户永远看不到「你的月配额用完了」。
//
// 这里统一走 middleware.PlatformErrorPresentation，与 API Key 鉴权中间件那条
// 链路共用同一张 platformErrorGuidance 表，保证两处对用户的措辞一致：
// 长周期配额改用不可重试的状态码并附上购买链接，RPM 保持 429。
//
// 包成一层而不是改 upstreamBillingErrorDetails 的函数体，是为了让 upstream 对
// 该函数内部的后续改动仍能干净合入 —— 那边只有函数名一行属于本 fork。

import (
	pkgerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
)

// billingErrorDetails 是全部 19 个调用点使用的入口，签名与 upstream 保持一致。
func billingErrorDetails(err error) (status int, code, message string, retryAfter int) {
	status, code, message, retryAfter = upstreamBillingErrorDetails(err)
	status, message = middleware2.PlatformErrorPresentation(pkgerrors.Reason(err), status, message)
	return status, code, message, retryAfter
}
