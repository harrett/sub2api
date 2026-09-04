// Package convlog 实现会话数据留存：把网关的客户端请求与模型输出归一化后落到
// 本地 spool，滚动压缩成 gzip JSONL 并异步上传对象存储，同时在 PostgreSQL 写一行
// 轻量检索索引，供风控按"账号池账号 + 时间范围"回溯用户输入。
//
// 三条硬约束贯穿整个包：
//  1. 捕获是纯旁路，任何失败只计数与降级，绝不影响网关响应；
//  2. 内存队列有界、磁盘用量有上限，宁可丢日志也不能拖垮主进程或写爆磁盘；
//  3. 完整正文只进对象存储，PostgreSQL 只存元数据与首 1KB 预览。
package convlog

import "time"

// 设置键与协议标识。
const (
	SettingKeyConfig = "conversation_capture_config"

	ProtocolAnthropicMessages = "anthropic_messages"
	ProtocolOpenAIChat        = "openai_chat"
	ProtocolOpenAIResponses   = "openai_responses"
	ProtocolGeminiGenerate    = "gemini_generate"
	ProtocolUnknown           = "unknown"

	// RecordSchemaVersion 随归一化 schema 的不兼容变更递增，训练侧按此分流。
	RecordSchemaVersion = 1
)

// 默认值。数值上限在 normalizeSettings 里强制收敛，配置写错不会击穿保护。
const (
	DefaultQueueCapacity = 10000
	MaxQueueCapacity     = 20000

	// 队列同时受条数与字节数双重限制：只限条数时，2 万条各含 MB 级正文依然能吃光 RAM。
	DefaultQueueMaxBytes = 256 << 20 // 256MB
	MaxQueueMaxBytes     = 1 << 30

	DefaultMaxRequestBytes  = 2 << 20 // 2MB
	MaxMaxRequestBytes      = 8 << 20
	DefaultMaxResponseBytes = 2 << 20 // 2MB
	MaxMaxResponseBytes     = 8 << 20

	DefaultPreviewBytes = 1024
	MaxPreviewBytes     = 2048

	DefaultRotateBytes    = 64 << 20 // 64MB
	DefaultRotateInterval = 5 * time.Minute

	DefaultSpoolMaxBytes         = 2 << 30 // 2GB
	DefaultDiskMinFreeBytes      = 8 << 30 // 低于此水位停止写盘
	DefaultDiskCriticalFreeBytes = 5 << 30 // 低于此水位完全停捕获
	DefaultIndexRetentionDays    = 30
	MaxIndexRetentionDays        = 90

	// DefaultSearchLimit / MaxSearchLimit / MaxSearchSpan 是 Beta 风控搜索的硬边界，
	// 后端强校验，避免"全账号 + 全时间 + 关键词"退化成全表扫描。
	DefaultSearchLimit = 100
	MaxSearchLimit     = 200
	MaxSearchSpan      = 30 * 24 * time.Hour
	DefaultSearchSpan  = 24 * time.Hour
)

// Settings 是后台可编辑的会话数据留存配置。
//
// ReuseBackupS3 为真时不保存自己的凭证，直接借用"数据备份"页已配置的 S3 端点与密钥，
// 只用自己的 Bucket/Prefix 区分对象——与异步生图对象存储的处理方式一致。
type Settings struct {
	Enabled       bool `json:"enabled"`
	ReuseBackupS3 bool `json:"reuse_backup_s3"`

	// SampleRate 为 0 或 1 表示全量捕获；0<r<1 时按请求随机采样。
	SampleRate float64 `json:"sample_rate"`
	// ExcludedGroupIDs 里的分组完全不捕获（例如内部测试分组）。
	ExcludedGroupIDs []int64 `json:"excluded_group_ids"`

	// 对象存储
	Bucket          string `json:"bucket"` // 留空且复用备份时沿用备份桶
	Prefix          string `json:"prefix"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"` //nolint:revive // field name follows AWS convention
	ForcePathStyle  bool   `json:"force_path_style"`

	// 容量与保护
	QueueCapacity         int   `json:"queue_capacity"`
	QueueMaxBytes         int64 `json:"queue_max_bytes"`
	MaxRequestBytes       int   `json:"max_request_bytes"`
	MaxResponseBytes      int   `json:"max_response_bytes"`
	PreviewBytes          int   `json:"preview_bytes"`
	RotateBytes           int64 `json:"rotate_bytes"`
	RotateIntervalSeconds int   `json:"rotate_interval_seconds"`
	SpoolMaxBytes         int64 `json:"spool_max_bytes"`
	DiskMinFreeBytes      int64 `json:"disk_min_free_bytes"`
	DiskCriticalFreeBytes int64 `json:"disk_critical_free_bytes"`
	IndexRetentionDays    int   `json:"index_retention_days"`
}

// Identity 是一条记录的归属维度快照。名字全部就地快照，风控查询不需要联表，
// 关联行被删除后历史记录依然可读。
type Identity struct {
	UserID      int64  `json:"user_id"`
	UserEmail   string `json:"user_email,omitempty"`
	APIKeyID    int64  `json:"api_key_id"`
	APIKeyName  string `json:"api_key_name,omitempty"`
	GroupID     int64  `json:"group_id,omitempty"`
	GroupName   string `json:"group_name,omitempty"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name,omitempty"`
	Platform    string `json:"platform,omitempty"`
}

// ModelInfo 记录模型名在链路上的三种形态，训练时可据此过滤映射噪声。
type ModelInfo struct {
	Requested string `json:"requested,omitempty"`
	Upstream  string `json:"upstream,omitempty"`
	Response  string `json:"response,omitempty"`
}

// Message 是归一化后的一轮对话。Content 保留结构化块（text / image / tool_result），
// 不拍平成字符串，避免丢掉多模态与工具调用信息。
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// Output 是归一化后的模型输出。
type Output struct {
	Role       string `json:"role"`
	Text       string `json:"text,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
	ToolCalls  []any  `json:"tool_calls,omitempty"`
	Content    any    `json:"content,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
	// Truncated 表示响应缓冲达到 MaxResponseBytes 上限被截断，训练时应排除。
	Truncated bool `json:"truncated,omitempty"`
}

// Conversation 是训练直接消费的归一化视图。
type Conversation struct {
	System   string    `json:"system,omitempty"`
	Messages []Message `json:"messages,omitempty"`
	Tools    any       `json:"tools,omitempty"`
	Output   *Output   `json:"output,omitempty"`
}

// Usage 是 token 计数快照，来源于响应体自身，不查计费库。
type Usage struct {
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
}

// Record 是落盘 JSONL 的一行。
type Record struct {
	SchemaVersion int       `json:"schema_version"`
	RequestID     string    `json:"request_id"`
	CreatedAt     time.Time `json:"created_at"`
	DurationMs    int       `json:"duration_ms"`
	StatusCode    int       `json:"status_code"`
	Stream        bool      `json:"stream"`
	Endpoint      string    `json:"endpoint"`
	Protocol      string    `json:"protocol"`
	Identity      Identity  `json:"identity"`
	Model         ModelInfo `json:"model"`

	Conversation Conversation `json:"conversation"`
	Usage        Usage        `json:"usage"`

	// RawRequest 是脱敏后的客户端原始请求 JSON，作为归一化 schema 漏字段时的回溯底本。
	// 原始响应流不落盘：SSE 冗余约 3~5x，归一化输出已覆盖训练需要。
	RawRequest any `json:"raw_request,omitempty"`

	// 以下字段只服务索引行与磁盘保护，不参与训练语义。
	IPAddress   string `json:"-"`
	InputBytes  int64  `json:"-"`
	OutputBytes int64  `json:"-"`
	Preview     string `json:"-"`
	ObjectKey   string `json:"-"`
}

// IndexRow 是 conversation_capture_index 的一行。
type IndexRow struct {
	ID         int64     `json:"id"`
	RequestID  string    `json:"request_id"`
	CreatedAt  time.Time `json:"created_at"`
	UserID     *int64    `json:"user_id,omitempty"`
	APIKeyID   *int64    `json:"api_key_id,omitempty"`
	AccountID  *int64    `json:"account_id,omitempty"`
	GroupID    *int64    `json:"group_id,omitempty"`
	UserEmail  string    `json:"user_email"`
	APIKeyName string    `json:"api_key_name"`

	AccountName  string `json:"account_name"`
	GroupName    string `json:"group_name"`
	Platform     string `json:"platform"`
	Protocol     string `json:"protocol"`
	Endpoint     string `json:"endpoint"`
	Model        string `json:"model"`
	Stream       bool   `json:"stream"`
	StatusCode   int    `json:"status_code"`
	DurationMs   int    `json:"duration_ms"`
	IPAddress    string `json:"ip_address"`
	InputPreview string `json:"input_preview"`
	InputBytes   int64  `json:"input_bytes"`
	OutputBytes  int64  `json:"output_bytes"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	ObjectKey    string `json:"object_key"`
}

// SearchFilter 是 Beta 风控搜索的查询条件。AccountID 与时间范围为必填，
// 由 Normalize 强制补齐与收敛。
type SearchFilter struct {
	AccountID int64
	UserID    *int64
	Start     time.Time
	End       time.Time
	Keyword   string
	Limit     int
}

// RuntimeStats 是后台运行态面板的数据源。
type RuntimeStats struct {
	Enabled            bool   `json:"enabled"`
	Degraded           bool   `json:"degraded"`
	DegradedReason     string `json:"degraded_reason,omitempty"`
	QueueDepth         int    `json:"queue_depth"`
	QueueCapacity      int    `json:"queue_capacity"`
	QueueBytes         int64  `json:"queue_bytes"`
	QueueMaxBytes      int64  `json:"queue_max_bytes"`
	DroppedTotal       uint64 `json:"dropped_total"`
	CapturedTotal      uint64 `json:"captured_total"`
	SpooledTotal       uint64 `json:"spooled_total"`
	SpoolBytes         int64  `json:"spool_bytes"`
	SpoolMaxBytes      int64  `json:"spool_max_bytes"`
	DiskFreeBytes      int64  `json:"disk_free_bytes"`
	PendingUploads     int    `json:"pending_uploads"`
	UploadedTotal      uint64 `json:"uploaded_total"`
	UploadFailedTotal  uint64 `json:"upload_failed_total"`
	IndexWriteFailed   uint64 `json:"index_write_failed_total"`
	LastError          string `json:"last_error,omitempty"`
	ObjectStoreEnabled bool   `json:"object_store_enabled"`
}
