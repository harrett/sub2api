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
  "schema_version": 1,
  "request_id": "req_...",
  "created_at": "2026-09-04T10:00:00Z",
  "duration_ms": 4210,
  "status_code": 200,
  "stream": true,
  "endpoint": "/v1/messages",
  "protocol": "anthropic_messages",
  "identity": {
    "user_id": 12, "user_email": "a@b.c",
    "api_key_id": 34, "api_key_name": "cli",
    "group_id": 5, "group_name": "claude-pool",
    "account_id": 78, "account_name": "acct-3", "platform": "anthropic"
  },
  "model": { "requested": "claude-opus-4-5", "upstream": "...", "response": "..." },
  "conversation": {
    "system": "...",
    "messages": [ { "role": "user", "content": [ { "type": "text", "text": "..." } ] } ],
    "tools": [ ... ],
    "output": {
      "role": "assistant",
      "content": [ { "type": "text", "text": "..." } ],
      "tool_calls": [ ... ],
      "stop_reason": "end_turn",
      "truncated": false
    }
  },
  "usage": { "input_tokens": 0, "output_tokens": 0, "cache_read_tokens": 0, "cache_creation_tokens": 0 },
  "raw_request": { ... }
}
```

`conversation` 是训练直接消费的归一化视图；`raw_request` 保留客户端原始 JSON（已脱敏）作为
回溯底本。**原始响应流不落盘** —— SSE 冗余约 3~5x，归一化输出已覆盖训练需要。

### 协议归一化

| protocol | 请求 | 响应（非流式） | 响应（SSE） |
|---|---|---|---|
| `anthropic_messages` | `system` + `messages[]` + `tools[]` | `content[]` / `stop_reason` | `content_block_delta.delta.{text,thinking,partial_json}` + `message_delta` |
| `openai_chat` | `messages[]` + `tools[]` | `choices[0].message` | `choices[0].delta.{content,tool_calls}` |
| `openai_responses` | `instructions` + `input[]` + `tools[]` | `output[]` | `response.output_text.delta`，终帧 `response.completed.response` 优先 |
| `gemini_generate` | `systemInstruction` + `contents[]` + `tools[]` | `candidates[0].content.parts[]` | 逐帧 `candidates[0].content.parts[]` 累加 |

无法识别的协议走通用兜底：请求原样进 `raw_request`，响应尝试通用 delta 字段收集。
响应缓冲被截断时置 `output.truncated = true`。

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
