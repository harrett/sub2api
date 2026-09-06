# 会话数据留存 — 设计

## 1. 数据分层

```
内存有界队列 (10k)      丢弃优先，永不阻塞网关
        ↓
本地 spool (≤2GB)      active.jsonl → rotate → .jsonl.gz
        ↓
S3 / R2 (长期)          logs/year=/month=/day=/hour=/<instance>_<segment>.jsonl.gz
        ↓
PostgreSQL 索引 (14~30d) metadata + input_preview(1KB) + object_key + request_id
```

PostgreSQL 与 S3 解耦：一个 gzip 段包含很多条请求，索引行存 `(object_key, request_id)` 二元组，
查看全文时下载该段并按 `request_id` 定位单行。

## 2. 捕获点

单一入口：`convlog.Middleware()` 注册在网关路由链上，位于 `RequestBodyLimit` 之后、
业务 handler 之前。这样一处覆盖 Anthropic / OpenAI Chat / Responses / Gemini / Antigravity /
Bedrock / Grok 全部上游，无需在每个 forward 分支里埋点。

- **请求体**：中间件用 `pkghttputil.ReadRequestBodyWithPrealloc` 读一次（顺带完成
  `Content-Encoding` 解码并剥离该 header），暂存后把 `c.Request.Body` 换成内存 reader，
  handler 后续读取行为不变。
- **响应体**：包装 `gin.ResponseWriter`，把写出的字节同时抄进有上限的缓冲区。
  参照既有 `handler/ops_error_logger.go:521` 的 `opsCaptureWriter`（同样在网关全链路上，
  SSE 感知、池化、限长），但独立实现，避免改动那份对错误日志语义敏感的代码。
- **元数据**：`c.Next()` 之后从 gin / request context 取，全部已由既有中间件与 handler 落位：
  `middleware.GetAPIKeyFromContext`、`ctxkey.AccountID`、`ctxkey.RequestID`、
  `ctxkey.Model`、`ctxkey.Platform`。

**开关关闭时**：中间件第一行 `if !svc.Enabled()` 直接 `c.Next()` 返回，不包 writer、不读 body。

### 跳过条件

- 非 JSON `Content-Type`（multipart 音视频上传等）
- 请求体超过 `max_request_bytes`（默认 2MB）
- `Enabled()` 为假 / 采样未命中 / 分组或用户在排除名单

## 3. 记录 schema（JSONL 每行一条）

```json
{
  "schema_version": 2,
  "request_id": "req_...",
  "session_id": "01a07589-b523-72a2-a801-f48c22179794",
  "thread_id": "01a07591-d10c-7702-a39c-6d030ec83faf",
  "continuation": false,
  "created_at": "2026-09-06T10:00:00Z",
  "duration_ms": 4210,
  "status_code": 200,
  "stream": true,
  "endpoint": "/v1/responses",
  "protocol": "openai_responses",
  "identity": {
    "user_id": 12, "user_email": "a@b.c",
    "api_key_id": 34, "api_key_name": "cli",
    "group_id": 5, "group_name": "codex-pool",
    "account_id": 78, "platform": "openai"
  },
  "model": { "requested": "gpt-5.6-luna", "upstream": "...", "response": "..." },
  "conversation": {
    "input": "本轮用户真正打出来的那句话（原文，未截断）",
    "output": {
      "role": "assistant",
      "text": "...", "thinking": "...", "tool_calls": [ ... ],
      "stop_reason": "completed", "truncated": false
    }
  },
  "usage": { "input_tokens": 0, "output_tokens": 0, "cache_read_tokens": 0, "cache_creation_tokens": 0 }
}
```

这是默认的 `capture_scope: essential`。`capture_scope: full` 时改为携带脱敏后的整个
`raw_request` 加一份 `conversation.roles` 角色索引，只在需要 agent 轨迹蒸馏时开启。

### 为什么默认只存一轮的用户输入与模型输出

两个目标只需要这些：追溯用户输入、以及"用户输入 → 模型输出"的蒸馏样本。系统提示、
工具定义、注入上下文都是同一客户端每个请求里逐字相同的样板，没有信息量。

更关键的是历史：agent 客户端每轮都把整段对话重发一次（抽样里一个请求 98% 的
input token 是缓存命中），所以按请求存全量等于单会话 O(n²)。而历史里的助手回复早已由
前几条记录各自的 `output` 存过，用户的历史输入也早已由它们各自那一轮存过——留下的
只是同一份内容被抄了 N 遍。

**上下文怎么重建**：不在单条记录里。单条只存本轮的 (用户输入, 模型输出)，
线性对话由 `thread_id` 分组、按 `created_at` 排序拼出来；`session_id` 是更粗的一层
（一个任务），客户端两者都不提供时退化为 `user_id` + 时间窗。蒸馏其实不需要
重建——每条记录本身就是一个完整样本。

分两层是被数据逼出来的：Codex 并行派发子代理时多个线程共用一个 `session_id`，
V2 抽样里四条记录 `session_id` 完全相同，其中三条却是三个不同子代理（A/B/C）
在并行跑，只按 session 排序会把它们串在一起。

**`continuation` 标记**：为真表示本轮只是 agent 循环续跑，用户没有新提问——判据是
本次请求里最后一条 user 项之后还有 assistant / tool 项。V2 抽样 user114 连续三条
记录的 input 一字不差，输出分别是「只有 tool_call」「文本+tool_call」「最终答复」；
三条都合法，但当成三个 (指令, 回答) 样本去蒸馏是错的，风控列表里也是三行重复。
标记而不丢弃：续跑轮的输出对 agent 轨迹蒸馏有价值，风控也需要看到完整活动。
风控页默认折叠，蒸馏取指令样本时按此排除。

**为什么取"最后一条"用户输入**：客户端注入的内容（系统提示、压缩检查点、运行时快照、
技能目录）总是排在真人那句之前，两份生产抽样都如此；取最后一条既能避开注入，又与列表
预览天然一致。更早的真实用户输入不会丢——它们各自曾是所属那一轮请求的"最后一条"。

按 role 裁剪则**不可行**：抽样 2 的客户端（DeepSeek Harness）把系统提示、压缩历史、
运行时快照全塞进 `role: "user"`，97.4% 的字节都是 user，而真人只打了 59 字节。

注入内容有两种形态，处理方式不同：
- **整条消息就是注入**（DeepSeek 的压缩检查点、运行时快照、Codex 安全策略）：整条丢弃，
  继续往前找上一条。
- **注入追加在用户消息内部**（`<environment_details>`、AIDE 系的
  `<workspace_attachment>`、Claude Code 的 `<system-reminder>`、Codex 的
  `<codex_internal_context>`）：只挖掉标签块。
  整条丢会丢掉用户真正打的"进行修复"，整条留会把每轮都变的时间戳与工作区文件树
  写进语料——`<workspace_attachment>` 在 V2 抽样的一条记录里占了 62% 的"用户输入"。

三份生产抽样实测：

| 抽样 | 协议 | 原始 | v2 essential | gzip | 保留的用户输入 |
|---|---|---|---|---|---|
| 1（Codex agent，77 项 input） | responses | 1,192,656 | 5,761（-99.5%） | 346,836 → 2,523 | 真实提问 |
| 2（DeepSeek Harness，97.4% 字节是 user） | chat | 667,434 | 659（-99.9%） | 132,794 → 414 | `继续` |
| 3（KFlash/Cline，83% 是 tool 输出） | chat | 841,148 | 564（-99.9%） | 218,749 → 431 | `进行修复` |

`encrypted_content`（上游 reasoning 附带的不透明密文，抽样 1 里占 64KB / 5.4%）在落盘前
整段删除——人和模型都读不了它。

### 只写索引、不写正文的请求

**追溯是第一需求**，所以没有任何请求会被完全丢弃——被裁掉的只是对象存储里的正文。

判据：失败 + 模型无输出 + **从未绑定上游账号**。这类请求写 PostgreSQL 索引行
（用户、账号、时间、模型、状态、1KB 输入预览全在，可搜可查），但不往对象存储写
正文。运行态 `index_only_total` 计数。

生产一小时实测的依据：847 条记录里 608 条是 503/429/404，**无一条带模型输出**，
占了 79% 的字节，客户端重试还会把同一份输入反复写进来（最极端的一例：724KB 的
输入被 503 重试存了 5 份）。

但**整条丢弃会捅出追溯窟窿**：那一小时 13 个用户里有 8 个只出现在这类失败里，
全丢等于这些人在风控里彻底隐身，17 条不同的用户输入（含真人提问）会凭空消失。
只丢正文则两头都保住：索引成本约为正文的 7%（240MB/30 天 vs 3.5GB/30 天）。

判据用 `account_id` 而不是状态码：已经绑定账号之后才失败的请求正文照留——
那次调用真的碰到了账号池，正是"防止账号被上游封"要追的对象。

### 生图端点

`/v1/images/*` 没有对话数组，用户输入就是 `prompt`。此前它被判成未知协议、
`input` 落成空串——风控对生图完全失明，而生图恰恰是常见滥用面。

图片两个方向都不留存，只留提示词文本：

- **用户上传的参考图**：`/v1/images/edits` 走 multipart，表单里的文件分块连读都不读，
  只取 `prompt` 字段。此前整个 multipart 请求被跳过，等于生图编辑完全不可追溯——
  而那正是上传参考图做违规改图的入口。只在 `Content-Length` 已知且不超上限时缓冲。
- **模型生成的图**：响应只留 `revised_prompt`（模型改写后的提示词，属文本输出）
  与 `size`，不留 `b64_json`，也不留指向图片的 `url`（短时效链接，存了既无用又等于
  保留了产出图）。

另有一层与协议无关的兜底：`b64_json` / `inline_data` / `inlineData` 整段删除，
任何字段下以 `data:image/` 开头的字符串替换成 `[IMAGE]`——内联图可能出现在任意
字段名下，只能按值识别。

### 协议归一化

| protocol | 用户输入（从后往前找最近一条真实用户文本） | 响应（非流式） | 响应（SSE） |
|---|---|---|---|
| `anthropic_messages` | `messages[]` 中 `role=user` 的文本块 | `content[]` / `stop_reason` | `content_block_delta.delta.{text,thinking,partial_json}` + `message_delta` |
| `openai_chat` | `messages[]` 中 `role=user` 的文本 | `choices[0].message` | `choices[0].delta.{content,tool_calls}` |
| `openai_responses` | `input[]` 中 `role=user` 项的 `input_text` | `output[]` | `response.output_text.delta`，终帧 `response.completed.response` 优先 |
| `gemini_generate` | `contents[]` 中 `role=user`（缺省即 user）的 `parts[].text` | `candidates[0].content.parts[]` | 逐帧 `candidates[0].content.parts[]` 累加 |

从后往前扫是必须的：agent 流量的最后一项几乎总是 `tool_result` / `function_call_output` /
`reasoning`，只看最后一个元素会得到空串——这正是列表里出现"（无文本输入）"而全文里明明
有用户输入的原因。

命中的文本还要过一遍注入内容过滤（`<system-reminder>`、Codex 安全策略文档、会话压缩
检查点、运行时快照）：这些以 `role=user` 发出但不是人打的，混进语料会让风控看错人。

无法识别的协议走通用兜底：按 `messages` / `contents` / `input` 依次尝试，响应侧收集通用
delta 字段。响应缓冲被截断时置 `output.truncated = true`。

## 4. 有界队列与降级

```go
queue chan *Record  // cap = queue_capacity（默认 10000，上限 20000）
```

- 入队用 `select { case q <- rec: default: dropped++ }` —— **永不阻塞**。
- 丢弃计数、队列深度、写盘失败数、上传失败数在 `/runtime` 暴露给后台。
- 丢弃率超阈值只记 WARN 日志，不自动关功能（避免抖动导致语料断档）。

单请求内存占用上限 = `max_request_bytes` + `max_response_bytes`（默认 2MB + 2MB），
再叠加队列上限 10000 条 —— 因此队列容量与单条上限必须一起收紧，配置校验里做乘积告警。

## 5. Spool 状态机

```
active-<segment>.jsonl          正在写
  ↓ rotate（≥64MB 或 ≥5min 或进程退出）
pending-<segment>.jsonl.gz      已压缩，待上传
  ↓ S3 PutObject 成功
删除本地文件
```

- 段 ID 在打开时生成，`object_key` 立即可算出，因此索引行插入时就能带上最终 key。
- 进程启动扫描 spool 目录：`.jsonl.gz` 直接进上传队列；遗留 `active-*.jsonl` 先补压缩再入队。
- 上传失败指数退避（2s → 5min 封顶），单文件失败不阻塞其它文件。
- 对象 key 含 instance id + 段 ID，天然唯一，不会互相覆盖。

### 磁盘保护水位

| 条件 | 行为 |
|---|---|
| spool 总量 ≥ `spool_max_bytes`（2GB） | 停止写新段，只写 PostgreSQL 索引（`object_key` 留空） |
| 磁盘剩余 < `disk_min_free_bytes`（8GB） | 同上 |
| 磁盘剩余 < `disk_critical_free_bytes`（5GB） | 完全停捕获，记 ERROR |

水位检查每 30s 一次（`syscall.Statfs`），不在每条记录上做 syscall。

## 6. Beta 风控搜索

查询约束（后端强校验，不只是前端限制）：

- `account_id` **必填**
- `start` / `end` **必填**，默认最近 24 小时，跨度上限 30 天
- `limit` 上限 200
- `keyword` 可选，仅对 `input_preview` 做 `ILIKE '%kw%'`

SQL 形态：

```sql
SELECT ... FROM conversation_capture_index
WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
  AND ($4 = '' OR input_preview ILIKE '%' || $4 || '%')
ORDER BY created_at DESC LIMIT $5;
```

先靠 `(account_id, created_at DESC)` B-tree 把结果集压到很小，再对少量 preview 扫描。
禁止"全账号 + 全时间 + 关键词"的全表扫描。

命中行可：
1. 查看全文 —— 按 `object_key` 从本地 spool 或 S3 取段，解 gzip 后按 `request_id` 匹配单行。
2. 封禁用户 —— 复用既有用户封禁能力，不新造一套。

## 7. 复用而非重造

| 需求 | 复用 |
|---|---|
| S3 客户端 | `repository/s3_client.go:27`（backup 与 image storage 共用） |
| S3 凭证与"复用备份配置" | `service/image_storage_settings.go` 的 `reuse_backup_s3` 模式 |
| 密钥加解密 | `service.SecretEncryptor` + `BackupService.EncryptionKeyConfigured()` |
| 响应体捕获 | `handler/ops_error_logger.go` 的 writer 包装思路 |
| 异步落库 sink | `service/ops_system_log_sink.go` 的有界队列 + 批量 + 退避 |
| 模块装配 | `securityaudit.ProviderSet` 的垂直模块模式 |
