# 网关错误链路对照表（fork 专有）

排查「用户看到的报错和后台记录不一样」「文案没生效」这类问题时先看这里。

平台自身产生的错误分散在**五条互不相干的链路**上。五条共用同一张责任方指引表
`platformErrorGuidance`（`backend/internal/pkg/gatewayerr/gatewayerr.go`），
但**接入点各不相同**——改了一条不会自动覆盖其余四条，这是历次排查踩坑最多的地方。

上游返回的错误**不走这五条**，由后台「错误透传规则」控制，见文末。

## 五条链路

| # | 触发场景 | 判定入口 | fork 包装层 | upstream 改动 |
|---|---|---|---|---|
| 1 | 鉴权 / 订阅 / 余额 / Key 状态 | `middleware.AbortWithError` | `middleware/middleware.go` 调用 `gatewayerr` | `middleware.go` 两个函数体 |
| 2 | 计费与限流检查（RPM、配额） | `handler.billingErrorDetails` | `handler/billing_error_presentation.go` | `gateway_handler.go` 函数改名 1 行 |
| 3 | 账号调度失败（选不出账号 / 全部限流） | `handler.classifyNoAccountErrorFromGin` | `handler/no_account_presentation.go` | `no_account_error.go` 函数改名 2 行 |
| 4 | 并发槽位 / 等待队列 | `handler.concurrencyErrorResponse` | `handler/concurrency_error_presentation.go` | `concurrency_error_response.go` 函数改名 1 行 |
| 5 | OpenAI 原生流式转发中途遇到上游容量降载 | `service.sanitizeOpenAIResponseFailedEventForClient` | `service/openai_capacity_shed_presentation.go` | `openai_gateway_response_handling.go` 函数改名 1 行 |

指引表本身是独立的叶子包 `internal/pkg/gatewayerr`（不是 middleware 的一部分）——
`middleware` 已经导入 `service`，`service` 反过来导入 `middleware` 会直接编译期
成环，所以承载指引表的包不能挂在三者中的任何一个上，必须单独拎出来才能被
middleware / handler / service 同时安全导入。

约定：upstream 的函数体一律不动，只把函数名改成 `upstream*`，同名包装层放在 fork
独有的新文件里，调用 `gatewayerr.PlatformErrorPresentation`。目的是让 upstream
对这些函数内部的后续改动仍能干净合入。

**踩过的坑**：链路 3 的 `classifyNoAccountErrorFromGin` 打好标签后，4 个调用点
都会再传给 `classifySelectionFailureError`——命中"全部账号被限流"这个更精确的
分支时，upstream 的实现会用一条全新的、未经处理的分类结果**整体覆盖**掉已经
打好标签的结果，标签被悄悄冲掉，用户看到裸的英文错误串。**如果某条链路是
"先打标签，中途又被另一个函数整体替换返回值"这种结构，替换点必须重新过一遍
`PlatformErrorPresentation`，不能指望前一次调用的标签能存活下来。**排查时看到
"消息不带 `[标签]`" 但代码明明已经包装过，先怀疑这种覆盖，而不是怀疑包装没生效。

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
| 3 | `ALL_ACCOUNTS_RATE_LIMITED`（比 `NO_AVAILABLE_ACCOUNTS` 更精确的兜底） | **服务器侧问题** | `429` | — |
| 3 | `MODEL_NOT_SUPPORTED_IN_GROUP` | **服务器侧问题** | `404` | — |
| 4 | `USER_CONCURRENCY_EXCEEDED` | 账户限流问题 | `429` | `SUB2API_RPM_ERROR_STATUS` |
| 4 | `ACCOUNT_CONCURRENCY_EXCEEDED` / `WAIT_QUEUE_FULL` / `CONCURRENCY_SERVICE_UNAVAILABLE` | **服务器侧问题** | 不变 | — |
| 5 | `UPSTREAM_CAPACITY_SHED_MIDSTREAM` | **服务器侧问题** | 不变（只改 message，不改 HTTP 状态码） | — |

链路 4 是四条 handler/middleware 链路里唯一一处两种责任方混在同一函数产出的
错误，按 `slotType` 分流：`user` 是用户侧，`account` 与队列满都是服务器侧。
错误自带的 `SlotType` 优先于调用方传入的默认值——忽略它会把 account 槽位耗尽
误判成用户的锅。

链路 5 与前四条性质不同：不是我们自己产生的错误，是**上游原始 SSE 字节**在
客户端已经开始收到输出 token 之后原样转发的（此时无法再切账号重试，已发出去
的内容没法收回）。upstream 的 `sanitizeOpenAIResponseFailedEventForClient` 已经
在改写这个事件的 `.code` 字段（`server_is_overloaded`/`slow_down` →
`server_error`，让 Codex CLI 从"直接终止会话"切到"走客户端自己的重试路径"），
但故意不碰 `.message`——那次改写只关心重试语义。链路 5 的包装层只加一件事：
命中 `isOpenAIUpstreamCapacityShedEvent` 时改写 `.message` 字段贴标签，`.code`
改写逻辑和其余转发路径完全不动。因为改写的是 SSE JSON payload 里的字段而不是
HTTP 响应，这条没有独立状态码可调。

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
2. **消息带 `[标签]`** → 五条链路已生效；客户端仍显示通用文案 = 状态码被客户端吞了，
   查上表「可调」列
3. **消息不带 `[标签]`** → 走的是尚未覆盖的第六条链路，或该版本未部署，或撞上了
   「先打标签、中途被整体覆盖」那类坑（见上面"踩过的坑"）
4. **「上游错误」有数据** → 错误来自上游，看后台「错误透传规则」而不是这五条
5. **「上游错误」为空** → 请求没出网关，是平台自身错误，走这五条

调度失败（链路 3）的详细原因不进客户端响应（会泄露账号池规模），查应用日志：

```bash
docker compose logs sub2api | grep account_select_failed | tail -20
```

输出形如 `pool=12, filtered: quota_auto_pause_weekly=8 model_not_supported=3,
selection_order_exhausted`，可直接判断是账号不够还是模型没配对。

## 上游错误（不走这五条）

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

- 指引表与协议格式化：`backend/internal/pkg/gatewayerr/gatewayerr.go`
- 跨包复用入口：`gatewayerr.PlatformErrorPresentation`（状态码+文案）、
  `gatewayerr.GatewayErrorResponse`（链路 1 专用，按协议渲染整个响应体）
- 上游透传规则：`backend/internal/service/error_passthrough_service.go`
- 默认上游映射：`backend/internal/handler/openai_gateway_handler.go` 的 `mapUpstreamError`
