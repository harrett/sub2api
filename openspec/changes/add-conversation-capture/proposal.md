## Why

平台目前没有任何"完整会话留存"能力：

- `usage_logs` 只有 token 计数与计费字段，没有正文。
- `content_moderation_logs` 只有 `input_excerpt`，且**没有 `account_id`**，无法按账号池账号回溯。
- `prompt_audit_jobs` / `prompt_audit_events`（`migrations/181_prompt_audit.sql`）刻意不落原文：只存 `prompt_hash` + `redacted_preview`，全文仅在 Redis 短 TTL 里存活，用完即删。

因此两个业务诉求都无法满足：

1. **自研模型训练/蒸馏**需要成规模的真实请求-响应语料，当前一条都留不下来。
2. **上游封号风控**需要"某个账号池账号在某时间段承载过哪些用户输入"，当前既没有正文也没有账号维度。

## What Changes

- 新增独立垂直模块 `backend/internal/convlog`，默认关闭，开启后在网关入站层捕获**客户端请求体**与**客户端响应体**，归一化成统一训练 schema 落盘。
- 落盘走**受限本地 spool → 滚动 gzip JSONL → 异步上传 S3 → 上传成功后删除本地文件**；复用已有备份 S3 客户端与后台"数据备份"页凭证（支持 `reuse_backup_s3`）。
- 新增 PostgreSQL 轻量索引表 `conversation_capture_index`：只存检索元数据 + `input_preview`（默认 1KB，上限 2KB）+ `object_key` + `request_id`，**不存完整 prompt/response**，并强制保留期（默认 30 天）。
- 新增 Beta 风控搜索后台：**必须指定账号池账号 + 时间范围**，默认最近 24 小时，最大跨度 30 天，最大返回 200 条，关键词可选（对 `input_preview` 做 `ILIKE`）；命中后可"查看全文"（按 `object_key` 取回 gzip 段并按 `request_id` 定位单行），并可直接封禁用户。
- 新增管理 API `/api/admin/conversation-capture/*` 与后台"数据备份"页的"会话数据留存"配置卡片。

## Non-goals

- 不做 `tsvector` / `pg_trgm` 全文索引。第一版只做"账号 + 时间范围"B-tree 收敛 + preview `ILIKE`。
- 不做 Parquet / Athena pipeline。gzip JSONL 就是归档格式，离线分析后续再说。
- 不改动、不迁移、不复用 `content_moderation_logs` / `prompt_audit_*`，两套风控互不干扰。
- 不在捕获链路上做任何拦截或阻断——捕获是纯旁路观测，永远不能改变请求结果。

## Impact

- **后端**：新增 `backend/internal/convlog/`；`internal/service/backup_service.go` 增加一个导出的凭证访问方法；`routes/gateway.go` 增加一个中间件；`cmd/server/{wire.go,main.go}` 增加模块装配与生命周期。
- **数据库**：新增 `conversation_capture_index` + 索引；配置存 `settings`，S3 密钥经既有 `SecretEncryptor` 加密。
- **磁盘**：新增 `<data_dir>/convlog/` spool 目录，受 `spool_max_bytes`（默认 2GB）与磁盘余量水位双重保护。
- **前端**：`BackupView.vue` 新增配置卡片；新增 `/admin/conversation-capture` 页面、路由、侧栏入口与 i18n。
- **兼容性**：无 breaking change，默认关闭；关闭时中间件在第一条语句 return，热路径零额外分配。

## Hard Invariants

这几条是本变更的验收红线，任何实现都不得违反：

1. **日志系统故障不得拖垮主业务。** 捕获链路上任何错误（磁盘满、S3 挂、队列满、序列化失败）只允许计数 + 降级，绝不允许影响网关响应。
2. **内存队列必须有界。** 默认 10000 条，上限 20000。队列满时**直接丢弃并计数**，不阻塞写入方，不动态扩容。
3. **磁盘必须有硬上限。** spool 总量超 `spool_max_bytes`、或磁盘剩余低于 `disk_min_free_bytes` 时停止写盘（可选降级为"只写 PostgreSQL 索引"），剩余低于 `disk_critical_free_bytes` 时完全停捕获。
4. **写盘前必须脱敏。** `Authorization` / `x-api-key` / `Cookie` / `Set-Cookie` / 平台 token 一律不落盘；PostgreSQL 只存 `api_key_id`。
5. **PostgreSQL 容量必须封顶。** 索引表按 `index_retention_days` 每日清理，默认 30 天，配置上限 90 天。
