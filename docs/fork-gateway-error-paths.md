# 网关错误链路对照表（fork 专有）

排查「用户看到的报错和后台记录不一样」「文案没生效」这类问题时先看这里。

平台自身产生的错误分散在**四条互不相干的链路**上。四条共用同一张责任方指引表
`platformErrorGuidance`（`backend/internal/server/middleware/gateway_error_format.go`），
但**接入点各不相同**——改了一条不会自动覆盖其余三条，这是历次排查踩坑最多的地方。

上游返回的错误**不走这四条**，由后台「错误透传规则」控制，见文末。

## 四条链路

| # | 触发场景 | 判定入口 | fork 包装层 | upstream 改动 |
|---|---|---|---|---|
| 1 | 鉴权 / 订阅 / 余额 / Key 状态 | `middleware.AbortWithError` | `middleware/gateway_error_format.go` | `middleware.go` 两个函数体 |
| 2 | 计费与限流检查（RPM、配额） | `handler.billingErrorDetails` | `handler/billing_error_presentation.go` | `gateway_handler.go` 函数改名 1 行 |
| 3 | 账号调度失败（选不出账号） | `handler.classifyNoAccountErrorFromGin` | `handler/no_account_presentation.go` | `no_account_error.go` 函数改名 1 行 |
| 4 | 并发槽位 / 等待队列 | `handler.concurrencyErrorResponse` | `handler/concurrency_error_presentation.go` | `concurrency_error_response.go` 函数改名 1 行 |

约定：upstream 的函数体一律不动，只把函数名改成 `upstream*`，同名包装层放在 fork
独有的新文件里。目的是让 upstream 对这些函数内部的后续改动仍能干净合入。

## 责任方与状态码

文案统一以 `[标签]` 开头，用户看到「服务器侧问题」即表示与其账户余额、套餐额度无关，
**不该去充值**。

「状态码」列是用户实际收到的值；「不变」表示指引表不覆盖，沿用该链路原本的状态码。
只有「可调」列有变量名的那几行会被指引表改写。

配了 `SUB2API_PURCHASE_URL` 时，仅以下错误会在文案末尾追加 `[访问 …]`：
`USAGE_LIMIT_EXCEEDED`、`INSUFFICIENT_BALANCE`、`SUBSCRIPTION_EXPIRED`、
`SUBSCRIPTION_NOT_FOUND`、`USER_PLATFORM_*_QUOTA_EXHAUSTED`。
**服务器侧与限流类一律不带链接**——为我们的容量问题引导用户充值是最糟的体验。

| 链路 | 错误码 | 标签 | 状态码 | 可调 |
|---|---|---|---|---|
| 1 | `USAGE_LIMIT_EXCEEDED` / `API_KEY_QUOTA_EXHAUSTED` | 账户额度问题 | `402` | `SUB2API_QUOTA_ERROR_STATUS` |
| 1 | `INSUFFICIENT_BALANCE` / `SUBSCRIPTION_*` | 账户余额/订阅问题 | 不变 | — |
| 1 | `INVALID_API_KEY` / `API_KEY_EXPIRED` / `API_KEY_DISABLED` / `API_KEY_REQUIRED` / `INVALID_AUTH_RATE_LIMITED` | 账户凭证问题 | 不变 | — |
| 1 | `USER_NOT_FOUND` / `USER_INACTIVE` | 账户状态问题 | 不变 | — |
| 1 | `ACCESS_DENIED` / `GROUP_NOT_ALLOWED` | 账户权限问题 | 不变 | — |
| 1 | `API_KEY_AUTH_OVERLOADED` / `INTERNAL_ERROR` / `SUBSCRIPTION_MAINTENANCE_FAILED` | **服务器侧问题** | 不变 | — |
| 2 | `USER_PLATFORM_{DAILY,WEEKLY,MONTHLY}_QUOTA_EXHAUSTED` | 账户额度问题 | `402` | `SUB2API_QUOTA_ERROR_STATUS` |
| 2 | `API_KEY_RATE_{5H,1D,7D}_EXCEEDED` | 账户额度问题 | `402` | `SUB2API_QUOTA_ERROR_STATUS` |
| 2 | `USER_RPM_EXCEEDED` / `GROUP_RPM_EXCEEDED` | 账户限流问题 | `429` | `SUB2API_RPM_ERROR_STATUS` |
| 2 | `BILLING_SERVICE_ERROR` | **服务器侧问题** | `503` | — |
| 3 | `NO_AVAILABLE_ACCOUNTS` | **服务器侧问题** | `503` | — |
| 3 | `MODEL_NOT_SUPPORTED_IN_GROUP` | **服务器侧问题** | `404` | — |
| 4 | `USER_CONCURRENCY_EXCEEDED` | 账户限流问题 | `429` | `SUB2API_RPM_ERROR_STATUS` |
| 4 | `ACCOUNT_CONCURRENCY_EXCEEDED` / `WAIT_QUEUE_FULL` / `CONCURRENCY_SERVICE_UNAVAILABLE` | **服务器侧问题** | 不变 | — |

链路 4 是唯一一处两种责任方混在同一函数产出的错误，按 `slotType` 分流：
`user` 是用户侧，`account` 与队列满都是服务器侧。错误自带的 `SlotType` 优先于调用方
传入的默认值——忽略它会把 account 槽位耗尽误判成用户的锅。

## 状态码开关

进程启动时读取，改完必须**重启容器**（`docker compose up -d`），reload 无效。

| 变量 | 默认 | 作用 |
|---|---|---|
| `SUB2API_QUOTA_ERROR_STATUS` | `402` | 额度/配额耗尽类 |
| `SUB2API_RPM_ERROR_STATUS` | `429` | 用户侧短期限流（RPM + 用户并发） |
| `SUB2API_PURCHASE_URL` | 空 | 充值页地址，配了才在文案末尾追加 `[访问 …]` |
| `SUB2API_CONSOLE_URL` | 空 | 控制台，`PURCHASE` 未配时的回退 |

选值依据（已实测的客户端行为，以 Codex CLI 为准）：

- **`429`** → 客户端走专用限流分支，重试到耗尽后打印自己的
  `exceeded retry limit, last status: 429`，**丢弃我们的响应体**，用户看不到任何提示
- **`402`** → 不重试，完整显示我们的消息 ✅
- **`503`** → 未实测，5xx 大概率同 429 被吞
- 长周期配额（小时~天级）重试永远不会成功，必须用不可重试的状态码
- 短周期限流（秒级）保留 `429`，退避有机会真正恢复，用户无感

不要把限流类改成 `402`：Payment Required 会把「请求太快」误导成「需要充值」。

## 排查顺序

1. **看后台错误详情的「响应详情」**——那就是实际发给客户端的响应体
2. **消息带 `[标签]`** → 四条链路已生效；客户端仍显示通用文案 = 状态码被客户端吞了，
   查上表「可调」列
3. **消息不带 `[标签]`** → 走的是第五条未覆盖的链路，或该版本未部署
4. **「上游错误」有数据** → 错误来自上游，看后台「错误透传规则」而不是这四条
5. **「上游错误」为空** → 请求没出网关，是平台自身错误，走这四条

调度失败（链路 3）的详细原因不进客户端响应（会泄露账号池规模），查应用日志：

```bash
docker compose logs sub2api | grep account_select_failed | tail -20
```

输出形如 `pool=12, filtered: quota_auto_pause_weekly=8 model_not_supported=3,
selection_order_exhausted`，可直接判断是账号不够还是模型没配对。

## 上游错误（不走这四条）

真正来自 OpenAI / Anthropic 等的错误由 `service.ErrorPassthroughService` 处理，
规则在**管理后台 → 账号管理 → 工具菜单 → 错误透传规则**。

- 未命中规则 → 走 `mapUpstreamError` 的默认映射，**丢弃上游原文**
- 命中规则 → 可透传原状态码/原文，或替换成自定义文案
- 匹配按优先级从小到大，**首个命中即返回**
- 「透传原文」与「自定义文案」二选一，**不能拼接**

服务器侧的上游错误建议**关闭**「透传上游错误信息」并写自定义文案：直接透传
OpenAI 的 `You've hit your usage limit` 会让终端用户以为是**自己**额度用完了。

`429` 底下混着两类，靠关键词拆开（用「错误码 且 关键词」模式，优先级更高的先匹配）：

| 上游原文 | 恢复时间 | 建议状态码 |
|---|---|---|
| `Rate limit exceeded`（频率） | 几十秒 | `503`，自动退避有意义 |
| `usage limit` / `quota exceeded`（额度） | 小时~天 | `400`，重试无用，必须让用户看到 |

## 相关代码

- 指引表与协议格式化：`backend/internal/server/middleware/gateway_error_format.go`
- 跨包复用入口：`middleware.PlatformErrorPresentation`
- 上游透传规则：`backend/internal/service/error_passthrough_service.go`
- 默认上游映射：`backend/internal/handler/openai_gateway_handler.go` 的 `mapUpstreamError`
