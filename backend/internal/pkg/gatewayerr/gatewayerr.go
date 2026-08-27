// Package gatewayerr — fork 本地包（upstream 不存在）。
//
// 目的：平台自身产生的错误（额度、余额、订阅、凭证、限流）此前一律以内部信封
// {"code","message"} 返回。OpenAI / Anthropic / Gemini 客户端都解析不了这个
// 形状，只能退回自己的通用文案（例如 Codex CLI 的
// "exceeded retry limit, last status: 429 Too Many Requests"），用户因此
// 完全看不到真实原因。同时调用方常常直接传 ApplicationError.Error()，把
// `error: code=429 reason="…" metadata=map[]` 这种内部字符串泄露给终端用户。
//
// 本包做三件事：
//  1. 按入站协议输出对应格式的错误体；
//  2. 清洗 ApplicationError 的原始字符串，只保留 message；
//  3. 给已知错误码补上「是你的账户问题还是服务器问题」的分类标签和可操作指引，
//     标签与后台「错误透传规则」里服务器侧错误使用的措辞保持一致。
//
// 注意上游错误（真正来自 OpenAI/Anthropic 等的响应）不走这里，那条链路由
// service.ErrorPassthroughService 的后台规则控制。
//
// 为什么单独成包：这套指引表最初长在 internal/server/middleware 里（当时只有
// API Key 鉴权中间件用得到），后来 internal/handler 的计费检查、账号调度、并发
// 槽位三条链路，以及 internal/service 的流式转发都要复用同一份责任方判断。
// middleware 已经导入 service，若指引表继续留在 middleware 包，service 反过来
// 导入 middleware 会直接编译期成环。提到这个无依赖的叶子包，三边都能安全导入。
//
// 接入点：
//   - internal/server/middleware/middleware.go 的 AbortWithError /
//     abortWithOpenAIQuotaError 两处函数体
//   - internal/handler/billing_error_presentation.go（计费/限流检查）
//   - internal/handler/no_account_presentation.go（账号调度失败）
//   - internal/handler/concurrency_error_presentation.go（并发槽位）
//   - internal/service 里处理 OpenAI 原生流式转发容量降载事件的包装层
//
// 全部接入点都遵循同一个模式：upstream 函数体保持原样，只改函数名一行，
// 实际的标签/指引逻辑放在 fork 独有的包装函数里，调用本包的
// PlatformErrorPresentation。以便长期跟随 upstream rebase 时把冲突面压到最小。
package gatewayerr

import (
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/googleapi"
)

// gatewayDialect 表示调用方期望的错误体格式。
type gatewayDialect uint8

const (
	// dialectInternal 是后台 / 用户面板使用的内部信封，Vue 前端依赖它，必须保持不变。
	dialectInternal gatewayDialect = iota
	dialectOpenAI
	dialectAnthropic
	dialectGemini
)

// InternalEnvelope 是后台/面板路径使用的内部错误信封，与
// middleware.ErrorResponse 字段和 JSON 标签完全一致（该类型定义在 middleware
// 包，本包不能反向导入，故在这里保留一份功能等价的最小定义）。
type InternalEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// quotaErrorStatus 是「额度/余额耗尽」类错误返回给网关客户端的状态码。
//
// 默认 402 而非 429：Codex CLI 等客户端对 429 会自动退避重试，用户要等完整个
// 重试链才能看到错误，而额度耗尽重试没有任何意义。402 不触发重试，消息会立刻
// 呈现给用户。若某个客户端对 402 处理不佳，可用环境变量改回 429 或改成 400，
// 不需要重新编译。
var quotaErrorStatus = envStatusCode("SUB2API_QUOTA_ERROR_STATUS", http.StatusPaymentRequired)

// rpmErrorStatus 是「每分钟请求数超限」返回给网关客户端的状态码。
//
// 默认保持 429，因为 RPM 与配额耗尽性质不同：窗口 60 秒内就重置，理论上
// Retry-After + SDK 自动退避能让请求自愈，用户完全无感。
//
// 但实测 Codex CLI 的整条重试链只跨约 6 秒（间隔 1s/1s/2s/2s），只有恰好越过
// 分钟边界时才会成功；其余情况它走专用的限流分支、直接丢弃响应体，只打印自己的
// "exceeded retry limit, last status: 429 Too Many Requests" —— 用户看不到
// 「你请求太快了」这个真实原因，反而会以为是服务端故障。
//
// 想让 RPM 的提示也能透出来，把它设成 400：非 429 的 4xx 客户端不会重试，
// 会直接把响应体打出来。代价是放弃自动自愈，属于产品取舍，故不默认开启。
//
// 刻意不复用 quotaErrorStatus：402 Payment Required 用在限流上语义错误，
// 会把「请求太快」误导成「需要充值」。
var rpmErrorStatus = envStatusCode("SUB2API_RPM_ERROR_STATUS", http.StatusTooManyRequests)

// 站点地址一律来自环境变量，不写死在文案里 —— 同一份镜像会部署到多个域名。
// 两个都没配就不追加地址，文案本身依然完整可读。
//
//	SUB2API_PURCHASE_URL=https://sub2api.com/purchase   充值 / 购买套餐页
//	SUB2API_CONSOLE_URL=https://sub2api.com/dashboard   控制台（purchase 未配时的回退）
var (
	consoleURL  = strings.TrimSpace(os.Getenv("SUB2API_CONSOLE_URL"))
	purchaseURL = firstNonEmpty(strings.TrimSpace(os.Getenv("SUB2API_PURCHASE_URL")), consoleURL)
)

// linkKind 指明某条指引该附带哪个站点地址。
type linkKind uint8

const (
	linkNone linkKind = iota
	linkPurchase
	linkConsole
)

// errorGuidance 描述一个平台错误码对用户的含义。
type errorGuidance struct {
	// label 让用户一眼分清责任方，与后台错误透传规则的「服务器侧问题」措辞对齐。
	label string
	// hint 是用户可以立刻执行的动作。
	hint string
	// link 非 linkNone 时在文案末尾追加对应地址；对应环境变量未配置则不追加。
	link linkKind
	// status 非零时覆盖原状态码。
	status int
}

// resolveGuidanceURL 返回该指引要展示的地址，未配置时返回空串。
func resolveGuidanceURL(kind linkKind) string {
	switch kind {
	case linkPurchase:
		return purchaseURL
	case linkConsole:
		return consoleURL
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// platformErrorGuidance 覆盖 api_key_auth.go 会产生的全部错误码。
// 未列出的错误码仍会被清洗消息，只是不带标签。
var platformErrorGuidance = map[string]errorGuidance{
	// ── 用户账户侧：用户自己能处理 ────────────────────────────────
	"USAGE_LIMIT_EXCEEDED": {
		label:  "账户额度问题",
		hint:   "你的套餐用量已达上限，这不是服务器故障。请等待用量窗口重置，或升级套餐。",
		link:   linkPurchase,
		status: quotaErrorStatus,
	},
	"API_KEY_QUOTA_EXHAUSTED": {
		label:  "账户额度问题",
		hint:   "该 API Key 的额度已用完，这不是服务器故障。请为该 Key 追加额度，或改用其他 Key。",
		status: quotaErrorStatus,
	},
	"INSUFFICIENT_BALANCE": {
		label: "账户余额问题",
		hint:  "账户余额不足，这不是服务器故障。请充值后重试。",
		link:  linkPurchase,
	},
	"SUBSCRIPTION_EXPIRED": {
		label: "账户订阅问题",
		hint:  "订阅已过期，请续费后重试。",
		link:  linkPurchase,
	},
	"SUBSCRIPTION_NOT_FOUND": {
		label: "账户订阅问题",
		hint:  "当前分组下没有有效订阅，请先购买对应套餐。",
		link:  linkPurchase,
	},
	"SUBSCRIPTION_INVALID": {
		label: "账户订阅问题",
		hint:  "订阅状态异常，请检查订阅，或联系管理员。",
	},
	"API_KEY_EXPIRED": {
		label: "账户凭证问题",
		hint:  "API Key 已过期，请重新生成一个。",
	},
	"API_KEY_DISABLED": {
		label: "账户凭证问题",
		hint:  "API Key 已被禁用，请检查后重新启用，或联系管理员。",
	},
	"INVALID_API_KEY": {
		label: "账户凭证问题",
		hint:  "API Key 无效，请确认 Authorization 头里填的是完整且未被删除的 Key。",
	},
	"API_KEY_REQUIRED": {
		label: "账户凭证问题",
		hint:  "请求未携带 API Key，请在 Authorization 头中以 Bearer 方式传入。",
	},
	"INVALID_AUTH_RATE_LIMITED": {
		label: "账户凭证问题",
		hint:  "无效鉴权尝试过多，已被临时限流。请先确认 Key 正确，稍后再试。",
	},
	"USER_NOT_FOUND": {
		label: "账户状态问题",
		hint:  "该 API Key 关联的账号不存在，请联系管理员。",
	},
	"USER_INACTIVE": {
		label: "账户状态问题",
		hint:  "账号未激活或已被停用，请联系管理员。",
	},
	"ACCESS_DENIED": {
		label: "账户权限问题",
		hint:  "当前来源 IP 不在该 Key 的允许列表内，请调整白名单后重试。",
	},
	"GROUP_NOT_ALLOWED": {
		label: "账户权限问题",
		hint:  "该 Key 所属的专属分组已不再允许你使用，请联系管理员。",
	},

	// ── 计费/限流侧：由 handler 层 billingErrorDetails 经
	//    PlatformErrorPresentation 复用，保证两条链路措辞一致 ──────
	//
	// 长周期配额重试没有意义（重置要等数小时到数十天），必须改用
	// quotaErrorStatus —— 原本返回 429 + Retry-After=数万秒，客户端在几秒内
	// 重试几次就放弃并丢掉响应体，用户永远看不到真实原因。
	"USER_PLATFORM_DAILY_QUOTA_EXHAUSTED": {
		label:  "账户额度问题",
		hint:   "该平台的每日用量配额已用完，这不是服务器故障。请等待次日重置，或升级套餐。",
		link:   linkPurchase,
		status: quotaErrorStatus,
	},
	"USER_PLATFORM_WEEKLY_QUOTA_EXHAUSTED": {
		label:  "账户额度问题",
		hint:   "该平台的每周用量配额已用完，这不是服务器故障。请等待下周重置，或升级套餐。",
		link:   linkPurchase,
		status: quotaErrorStatus,
	},
	"USER_PLATFORM_MONTHLY_QUOTA_EXHAUSTED": {
		label:  "账户额度问题",
		hint:   "该平台的每月用量配额已用完，这不是服务器故障。请等待下月重置，或升级套餐。",
		link:   linkPurchase,
		status: quotaErrorStatus,
	},
	// API Key 上的 5h/1d/7d 速率限额由管理员配置，用户自己改不了，不给购买链接。
	"API_KEY_RATE_5H_EXCEEDED": {
		label:  "账户额度问题",
		hint:   "该 API Key 的 5 小时限额已用完，这不是服务器故障。请等待窗口重置，或联系管理员调整限额。",
		status: quotaErrorStatus,
	},
	"API_KEY_RATE_1D_EXCEEDED": {
		label:  "账户额度问题",
		hint:   "该 API Key 的单日限额已用完，这不是服务器故障。请等待次日重置，或联系管理员调整限额。",
		status: quotaErrorStatus,
	},
	"API_KEY_RATE_7D_EXCEEDED": {
		label:  "账户额度问题",
		hint:   "该 API Key 的 7 天限额已用完，这不是服务器故障。请等待窗口重置，或联系管理员调整限额。",
		status: quotaErrorStatus,
	},
	// RPM 与用户并发默认保持 429（窗口很短，退避有机会真正恢复），
	// 但可用 SUB2API_RPM_ERROR_STATUS 改成 400 让提示能透传，取舍见该变量注释。
	//
	// 用户并发超限与 RPM 同属「用户侧短期限流」：都是用户自己发得太快，
	// 都在秒级恢复，用户降速就能解决，故共用同一个状态码开关。
	"USER_CONCURRENCY_EXCEEDED": {
		label:  "账户限流问题",
		hint:   "你同时发起的请求数超过了账号的并发上限，这不是服务器故障。请减少并发请求数，或等前面的请求完成后重试。",
		status: rpmErrorStatus,
	},
	"USER_RPM_EXCEEDED": {
		label:  "账户限流问题",
		hint:   "你的请求频率超过了账号的每分钟上限，这不是服务器故障。请降低并发或稍等一分钟后重试。",
		status: rpmErrorStatus,
	},
	"GROUP_RPM_EXCEEDED": {
		label:  "账户限流问题",
		hint:   "你的请求频率超过了该分组的每分钟上限，这不是服务器故障。请降低并发或稍等一分钟后重试。",
		status: rpmErrorStatus,
	},

	// ── 服务器侧：用户做什么都没用，别让他去充值 ──────────────────
	"BILLING_SERVICE_ERROR": {
		label: "服务器侧问题",
		hint:  "计费服务暂时不可用，与你的账户余额和套餐额度无关。请稍后重试。",
	},
	// 账号调度失败（handler 层 classifyNoAccountError 的两个分支）。
	// 原文案只有一句 "Service temporarily unavailable"，用户完全无法判断责任方，
	// 很多人会误以为是自己额度用完而去充值。
	"NO_AVAILABLE_ACCOUNTS": {
		label: "服务器侧问题",
		hint:  "当前分组的上游账号都处于限流冷却或额度暂停中，服务器已尝试全部可用账号。这与你的账户余额和套餐额度无关，请稍后重试；持续出现请联系管理员。",
	},
	// 调度器能明确判断出"全部候选账号都被上游限流"这个更精确的原因时命中
	// （相对于 NO_AVAILABLE_ACCOUNTS 那个更宽泛的兜底）。保持 429 不变：
	// 上游限流通常几十秒到几分钟内解除，跟 RPM 一样短周期，不适合改成不可重试。
	"ALL_ACCOUNTS_RATE_LIMITED": {
		label: "服务器侧问题",
		hint:  "当前分组下所有支持该模型的账号都被上游临时限流，服务器已尝试全部可用账号。这与你的账户余额和套餐额度无关，通常几十秒到几分钟内恢复，请稍后重试；持续出现请联系管理员。",
	},
	// 模型在该分组无任何账号支持：属于服务端配置问题，重试永远不会成功，
	// 因此保持 404 而不是 503，也不引导用户充值。
	"MODEL_NOT_SUPPORTED_IN_GROUP": {
		label: "服务器侧问题",
		hint:  "该分组下没有账号配置支持这个模型，属于服务端配置问题，重试无法解决。请改用其他模型，或联系管理员为该分组补充支持该模型的账号。",
	},
	// 并发槽位相关（handler 层 concurrencyErrorResponse）。注意这几条里
	// USER_CONCURRENCY_EXCEEDED 是用户侧、其余都是服务器侧 —— 同一个函数产出的
	// 错误分属两种责任方，必须按 slotType 区分，不能一刀切。
	"ACCOUNT_CONCURRENCY_EXCEEDED": {
		label: "服务器侧问题",
		hint:  "上游账号的并发槽位已满，服务器正在等待空闲账号。这与你的账户余额和套餐额度无关，请稍后重试；持续出现请联系管理员。",
	},
	"WAIT_QUEUE_FULL": {
		label: "服务器侧问题",
		hint:  "服务器当前排队请求过多，等待队列已满。这与你的账户余额和套餐额度无关，请稍后重试；持续出现请联系管理员。",
	},
	"CONCURRENCY_SERVICE_UNAVAILABLE": {
		label: "服务器侧问题",
		hint:  "并发调度服务暂时不可用，与你的账户余额和套餐额度无关。请稍后重试或联系管理员。",
	},
	"API_KEY_AUTH_OVERLOADED": {
		label: "服务器侧问题",
		hint:  "鉴权服务暂时过载，与你的账户余额和套餐额度无关。请稍后重试。",
	},
	"INTERNAL_ERROR": {
		label: "服务器侧问题",
		hint:  "服务器内部错误，与你的账户余额和套餐额度无关。请稍后重试或联系管理员。",
	},
	"SUBSCRIPTION_MAINTENANCE_FAILED": {
		label: "服务器侧问题",
		hint:  "订阅用量窗口维护失败，与你的账户余额和套餐额度无关。请联系管理员。",
	},
	// OpenAI 原生流式转发中途遇到上游容量降载（capacity shed）事件时命中：
	// 客户端已经开始收到 token，无法再切账号重试，只能把上游的失败事件原样
	// 转发。这是四条 handler/middleware 链路之外唯一一处"必须原样转发上游 SSE
	// 字节、但仍要在 message 里补标签"的场景，详见
	// internal/service 里对应的包装层。
	"UPSTREAM_CAPACITY_SHED_MIDSTREAM": {
		label: "服务器侧问题",
		hint:  "上游服务当前容量紧张，暂时拒绝了这次生成请求。这与你的账户余额和套餐额度无关，请稍后重试。",
	},
}

// PlatformErrorPresentation 让 handler / service 层复用这里的责任方标签、
// 可操作指引与额度类状态码策略，保证「中间件拦截」「计费检查拒绝」「账号调度
// 失败」「并发槽位」「流式转发容量降载」五条链路对用户的措辞完全一致。
//
// reason 取自 pkg/errors 的 Reason（如 USER_PLATFORM_MONTHLY_QUOTA_EXHAUSTED）
// 或调用方自定义的查表键（如 NO_AVAILABLE_ACCOUNTS）。未登记的 reason 只清洗
// 消息、不改状态码，因此对未知错误是安全的空操作。
//
// 注意这里只负责「状态码 + 文案」；响应体的协议形状仍由各调用方自己的 writer
// 决定，它们本来就已经按平台输出正确格式。
func PlatformErrorPresentation(reason string, statusCode int, message string) (int, string) {
	guidance, ok := platformErrorGuidance[reason]
	if !ok {
		return statusCode, unwrapApplicationErrorMessage(message)
	}
	if guidance.status > 0 {
		statusCode = guidance.status
	}
	return statusCode, clientFacingMessage(guidance, true, message)
}

// GatewayErrorResponse 把平台错误渲染成调用方能解析的形状。
// 返回最终状态码和响应体；内部路径（后台/面板）原样返回内部信封。
func GatewayErrorResponse(c *gin.Context, statusCode int, code, message string) (int, any) {
	dialect := detectGatewayDialect(c)
	if dialect == dialectInternal {
		// 后台/面板保持内部信封和原状态码不变，但仍然清洗 ApplicationError 的
		// 原始字符串 —— 运维错误日志和前端提示都不该出现 metadata=map[]。
		return statusCode, InternalEnvelope{Code: code, Message: unwrapApplicationErrorMessage(message)}
	}

	guidance, hasGuidance := platformErrorGuidance[code]
	if hasGuidance && guidance.status > 0 {
		statusCode = guidance.status
	}
	text := clientFacingMessage(guidance, hasGuidance, message)

	switch dialect {
	case dialectAnthropic:
		return statusCode, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    anthropicErrorType(statusCode),
				"message": text,
			},
		}
	case dialectGemini:
		return statusCode, gin.H{
			"error": gin.H{
				"code":    statusCode,
				"message": text,
				"status":  geminiStatusString(statusCode),
			},
		}
	default:
		return statusCode, gin.H{
			"error": gin.H{
				"message": text,
				"type":    openAIErrorType(statusCode),
				"param":   nil,
				"code":    strings.ToLower(code),
			},
		}
	}
}

// clientFacingMessage 组装最终文案：[分类] 原始说明 — 可操作指引[访问 地址]。
func clientFacingMessage(guidance errorGuidance, hasGuidance bool, raw string) string {
	base := unwrapApplicationErrorMessage(raw)
	if !hasGuidance {
		return base
	}

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(guidance.label)
	b.WriteString("] ")
	if base != "" {
		b.WriteString(base)
		b.WriteString(" — ")
	}
	b.WriteString(guidance.hint)
	if url := resolveGuidanceURL(guidance.link); url != "" {
		b.WriteString("[访问 ")
		b.WriteString(url)
		b.WriteString("]")
	}
	return b.String()
}

// applicationErrorMessagePattern 匹配 pkg/errors.ApplicationError.Error() 的输出：
//
//	error: code=429 reason="DAILY_LIMIT_EXCEEDED" message="daily usage limit exceeded" metadata=map[]
//
// 调用方常常直接把它当成用户可读文案传进 AbortWithError，这里只取 message 部分。
var applicationErrorMessagePattern = regexp.MustCompile(`\bmessage=("(?:[^"\\]|\\.)*")`)

func unwrapApplicationErrorMessage(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "error: code=") {
		return raw
	}
	match := applicationErrorMessagePattern.FindStringSubmatch(raw)
	if len(match) != 2 {
		return raw
	}
	unquoted, err := strconv.Unquote(match[1])
	if err != nil {
		return raw
	}
	return strings.TrimSpace(unquoted)
}

// ──────────────────────────────────────────────────────────
// 入站协议识别
// ──────────────────────────────────────────────────────────

// geminiDialectPrefixes 前缀唯一，不会与其他协议重叠。
var geminiDialectPrefixes = []string{
	"/v1beta",
	"/antigravity/v1beta",
}

var anthropicDialectPrefixes = []string{
	"/v1/messages",
	"/messages",
	"/antigravity/v1/messages",
}

var openAIDialectPrefixes = []string{
	"/v1/responses",
	"/responses",
	"/openai/v1/responses",
	"/backend-api/codex",
	"/v1/chat/completions",
	"/chat/completions",
	"/v1/embeddings",
	"/embeddings",
	"/v1/images",
	"/images",
	"/v1/videos",
	"/videos",
	"/v1/live",
	"/v1/realtime",
	"/realtime",
	"/v1/alpha/search",
	"/alpha/search",
	"/tts",
	"/stt",
	"/custom-voices",
	"/web_search",
	"/x_search",
}

// ambiguousGatewayPrefixes 是 Anthropic 与 OpenAI 客户端都会请求的端点
// （/v1/models 最典型），无法只靠路径判定，改用请求头区分。
var ambiguousGatewayPrefixes = []string{
	"/v1/models",
	"/models",
	"/v1/usage",
	"/usage",
	"/v1/sub2api",
	"/antigravity/v1/models",
	"/antigravity/v1/usage",
	"/antigravity/models",
}

func detectGatewayDialect(c *gin.Context) gatewayDialect {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return dialectInternal
	}
	path := strings.TrimRight(c.Request.URL.Path, "/")
	if path == "" {
		return dialectInternal
	}

	// 后台与用户面板：Vue 前端按 {"code","message"} 解析，绝不能改。
	if hasPathPrefix(path, "/api") {
		return dialectInternal
	}
	if matchesAnyPathPrefix(path, geminiDialectPrefixes) {
		return dialectGemini
	}
	if matchesAnyPathPrefix(path, anthropicDialectPrefixes) {
		return dialectAnthropic
	}
	if matchesAnyPathPrefix(path, openAIDialectPrefixes) {
		return dialectOpenAI
	}
	if matchesAnyPathPrefix(path, ambiguousGatewayPrefixes) {
		return dialectFromRequestHeaders(c)
	}
	return dialectInternal
}

// dialectFromRequestHeaders 按各家 SDK 的特征头判定协议。
func dialectFromRequestHeaders(c *gin.Context) gatewayDialect {
	header := c.Request.Header
	if header.Get("x-goog-api-key") != "" {
		return dialectGemini
	}
	if header.Get("anthropic-version") != "" || header.Get("x-api-key") != "" {
		return dialectAnthropic
	}
	return dialectOpenAI
}

func matchesAnyPathPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if hasPathPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// hasPathPrefix 只在完整路径段边界上匹配，避免 /v1/modelsX 命中 /v1/models。
func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// ──────────────────────────────────────────────────────────
// 各协议的错误类型取值
// ──────────────────────────────────────────────────────────

func openAIErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusPaymentRequired, http.StatusTooManyRequests:
		return "insufficient_quota"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusBadRequest:
		return "invalid_request_error"
	default:
		if statusCode >= 500 {
			return "api_error"
		}
		return "invalid_request_error"
	}
}

// geminiStatusString 补上 googleapi 没有覆盖的 402。额度耗尽在 Google 的
// canonical code 里就是 RESOURCE_EXHAUSTED，直接落到 UNKNOWN 会让 Gemini SDK
// 无法归类。
func geminiStatusString(statusCode int) string {
	if statusCode == http.StatusPaymentRequired {
		return "RESOURCE_EXHAUSTED"
	}
	return googleapi.HTTPStatusToGoogleStatus(statusCode)
}

func anthropicErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	default:
		if statusCode >= 500 {
			return "api_error"
		}
		return "invalid_request_error"
	}
}

// envStatusCode 读取环境变量里的 HTTP 状态码，非法值回退到默认值。
func envStatusCode(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 400 || value > 599 {
		return fallback
	}
	return value
}
