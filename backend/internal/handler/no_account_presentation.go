package handler

// no_account_presentation.go — fork 本地文件（upstream 不存在）。
//
// 账号调度失败时，upstream 的 classifyNoAccountError 只会给出：
//
//	503 api_error  "Service temporarily unavailable"
//	404 model_not_found  "Model %q is not supported by any configured account in this group"
//
// 前者是个纯兜底文案，用户完全无法判断责任方 —— 实际场景通常是上游账号被限流后
// 集中进入冷却/额度暂停，池子里一个都选不出来，属于服务端容量问题；但不少用户会
// 误以为是自己额度用完而去充值。
//
// 这里统一走 gatewayerr.PlatformErrorPresentation，与 API Key 鉴权中间件、
// 计费检查等链路共用同一张 platformErrorGuidance 表，保证各处措辞一致。
// 状态码不做重映射：503 可重试、404 是配置问题不可重试，upstream 的判断本就正确。
//
// 注意排障详情（调度器的 pool=/filtered: 摘要）不放进客户端响应 —— 那会泄露
// 账号池规模与内部过滤原因。它已经由各调用点的
// reqLog.Warn("*.account_select_failed", zap.Error(err)) 写入应用日志。
//
// 包一层而不是改 upstream 函数体，是为了让 upstream 对分类逻辑的后续改动仍能
// 干净合入 —— 那边只有函数名一行属于本 fork。

import (
	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/gatewayerr"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 责任方指引的查表键。upstream 的分类结果不带错误码，只能由 ModelNotFound 反推。
const (
	noAvailableAccountsGuidanceCode = "NO_AVAILABLE_ACCOUNTS"
	modelNotSupportedGuidanceCode   = "MODEL_NOT_SUPPORTED_IN_GROUP"
	allAccountsRateLimitedGuidance  = "ALL_ACCOUNTS_RATE_LIMITED"
)

// classifyNoAccountErrorFromGin 是全部调用点使用的入口，签名与 upstream 一致。
func classifyNoAccountErrorFromGin(
	c *gin.Context,
	diag service.ModelAvailabilityDiagnoser,
	apiKey *service.APIKey,
	routingModel string,
	displayModel string,
	platform string,
) noAccountErrorClassification {
	classification := upstreamClassifyNoAccountErrorFromGin(c, diag, apiKey, routingModel, displayModel, platform)

	code := noAvailableAccountsGuidanceCode
	if classification.ModelNotFound {
		code = modelNotSupportedGuidanceCode
	}
	classification.Status, classification.Message = gatewayerr.PlatformErrorPresentation(
		code,
		classification.Status,
		classification.Message,
	)
	return classification
}

// classifySelectionFailureError 是全部调用点使用的入口，签名与 upstream 一致。
//
// 全部 4 个调用点都是先调用 classifyNoAccountErrorFromGin（上面已经打好标签），
// 再用这个函数的返回值整体覆盖 cls —— upstream 命中"全部账号被限流"这个更精确
// 的场景时，会用一条全新的、未经处理的 noAccountErrorClassification 直接替换
// 掉已打标签的结果，导致标签被悄悄冲掉（用户看到裸的
// "All available accounts are currently rate-limited. Please retry later."）。
// 因此这里必须重新补一次，而不能指望调用方那次 classifyNoAccountErrorFromGin
// 的标签能存活下来。
//
// 用结构体整体比较判断 upstream 是否真的命中替换：三个字段都是标量，可比较；
// 未命中时原样返回 fallback（已经带标签），不会重复加工。
func classifySelectionFailureError(err error, fallback noAccountErrorClassification) noAccountErrorClassification {
	result := upstreamClassifySelectionFailureError(err, fallback)
	if result == fallback {
		return result
	}
	result.Status, result.Message = gatewayerr.PlatformErrorPresentation(
		allAccountsRateLimitedGuidance,
		result.Status,
		result.Message,
	)
	return result
}
