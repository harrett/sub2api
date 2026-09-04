# 任务分解

## 后端 — 数据层

- [x] `backend/migrations/234_conversation_capture.sql`：`conversation_capture_index` 表 +
      `(account_id, created_at DESC)` / `(user_id, created_at DESC)` / `(created_at DESC)` /
      `(request_id)` 索引；默认配置写入 `settings`。
- [x] `convlog/repository.go`：`database/sql` 实现 Insert / Search / GetByRequestID /
      DeleteOlderThan / Stats。搜索参数在仓储层再校验一次（账号必填、跨度上限）。

## 后端 — 捕获管线

- [x] `convlog/types.go`：`Record`、`Settings`、`RuntimeStats`、常量与默认值。
- [x] `convlog/settings.go`：读写 `settings` 表，支持 `reuse_backup_s3`，密钥加密，
      `Invalidate()` 后下一次请求即生效。
- [x] `convlog/redact.go`：header / body 敏感字段脱敏（Authorization、x-api-key、
      Cookie、Set-Cookie、api_key、access_token）。
- [x] `convlog/normalize.go`：四类协议的请求归一化 + 通用兜底。
- [x] `convlog/aggregate.go`：非流式 JSON 与 SSE 的输出聚合，含截断标记。
- [x] `convlog/spool.go`：滚动写入、gzip、段 ID 与 object key 生成、磁盘水位检查、
      启动扫描恢复。
- [x] `convlog/uploader.go`：S3 上传、指数退避、成功后删本地、失败不阻塞其它文件。
- [x] `convlog/sink.go`：有界队列（默认 10000）、非阻塞入队、丢弃计数、批量落索引。
- [x] `convlog/service.go`：装配上述组件，暴露 `Enabled()`、`Submit()`、`Start()`、
      `Shutdown()`、`Search()`、`FetchFullRecord()`、`Runtime()`。

## 后端 — 接入

- [x] `convlog/capture_writer.go`：`gin.ResponseWriter` 包装，限长捕获。
- [x] `convlog/middleware.go`：开关短路、请求体 tee、元数据收集、提交。
- [x] `service/backup_service.go`：新增导出的 `ResolveS3Credentials`（供 convlog 复用凭证）。
- [x] `routes/gateway.go`：注册中间件（`RequestBodyLimit` 之后）。
- [x] `convlog/module.go` + `cmd/server/{wire.go,main.go}`：ProviderSet、启动、优雅关闭
      （关闭时 flush 队列 + rotate 当前段）。

## 后端 — 管理 API

- [x] `convlog/handler.go` + `routes/admin.go`：
      - `GET/PUT /api/admin/conversation-capture/config`
      - `POST /api/admin/conversation-capture/config/test`
      - `GET /api/admin/conversation-capture/runtime`
      - `GET /api/admin/conversation-capture/records`（Beta 搜索，账号+时间必填）
      - `GET /api/admin/conversation-capture/records/:request_id/full`
- [x] 保留期清理定时任务（复用现有调度方式），每日删除超期索引行。

## 前端

- [x] `BackupView.vue`：新增"会话数据留存"卡片（开关、复用备份 S3、桶/前缀/端点、
      preview 长度、保留天数、spool 上限、队列容量、采样率、测试连接、运行态）。
- [x] `views/admin/ConversationCaptureView.vue`：Beta 风控搜索（账号选择器、时间范围、
      关键词、结果表、查看全文抽屉、封禁用户）。
- [x] `api/conversationCapture.ts`、路由、侧栏入口、i18n zh/en。

## 测试

- [x] 归一化与 SSE 聚合的表驱动测试（四协议 + 截断 + 兜底）。
- [x] 队列满时丢弃且不阻塞。
- [x] spool 滚动 / gzip / 启动恢复。
- [x] 磁盘水位三档行为。
- [x] 脱敏：断言落盘内容不含 Authorization / api key 明文。
- [x] 搜索参数校验：缺账号、缺时间、跨度超限、limit 超限。
- [x] `make test-frontend` 全绿；后端 `go build`/`go vet`（含 unit/integration tag）与 `go test ./internal/convlog/...` 全绿。
      注：`internal/repository` 的 `TestAliyunCaptchaVerifier_TransportError` 在本机干净分支上同样失败，属既有环境问题，与本变更无关。
