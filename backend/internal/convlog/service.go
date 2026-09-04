package convlog

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// settingsRefreshPeriod 是跨实例配置同步周期：本进程改配置会立即 Invalidate，
// 别的实例改的配置靠这个轮询兜底。
const settingsRefreshPeriod = time.Minute

// retentionPeriod 是索引保留期清理的执行间隔。
const retentionPeriod = 6 * time.Hour

// CaptureInput 是中间件交给捕获管线的一次请求全貌。
type CaptureInput struct {
	RequestID         string
	StartedAt         time.Time
	Duration          time.Duration
	StatusCode        int
	Endpoint          string
	Stream            bool
	RequestBody       []byte
	ResponseBody      []byte
	ResponseTruncated bool
	Identity          Identity
	RequestedModel    string
	UpstreamModel     string
	IPAddress         string
}

// Service 装配整条会话数据留存管线。
type Service struct {
	settings *SettingStore
	repo     *Repository
	spool    *Spool
	sink     *Sink
	uploader *Uploader
	factory  ObjectStoreFactory

	mu               sync.RWMutex
	current          *Settings
	currentS3        *S3Config
	storeFingerprint string

	lifecycle sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewService 构造服务。spool 目录建不出来时返回 nil 服务而不是错误：捕获是可选能力，
// 不能因为它启动不了就让整个网关起不来。
func NewService(
	settings *SettingStore,
	repo *Repository,
	factory ObjectStoreFactory,
) *Service {
	dir := spoolDir()
	spool, err := NewSpool(dir, instanceID(), SpoolOptions{
		Prefix:                "conversations/",
		RotateBytes:           DefaultRotateBytes,
		RotateInterval:        DefaultRotateInterval,
		SpoolMaxBytes:         DefaultSpoolMaxBytes,
		DiskMinFreeBytes:      DefaultDiskMinFreeBytes,
		DiskCriticalFreeBytes: DefaultDiskCriticalFreeBytes,
	})
	if err != nil {
		logger.L().Error("convlog.spool_init_failed; conversation capture stays disabled", zap.Error(err))
		return &Service{settings: settings, repo: repo, factory: factory}
	}

	uploader := NewUploader(spool)
	return &Service{
		settings: settings,
		repo:     repo,
		spool:    spool,
		uploader: uploader,
		sink:     NewSink(spool, repo, DefaultQueueCapacity, DefaultQueueMaxBytes),
		factory:  factory,
	}
}

// Start 拉起消费、上传、维护循环，并做一次 spool 恢复扫描。
func (s *Service) Start(ctx context.Context) error {
	if s == nil || s.spool == nil {
		return nil
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.cancel != nil {
		return nil
	}

	if err := s.spool.Recover(); err != nil {
		logger.L().Warn("convlog.spool_recover_failed", zap.Error(err))
	}
	s.refreshSettings(ctx)

	s.sink.Start()
	s.uploader.Start()

	loopCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.loop(loopCtx)
	}()

	return nil
}

// Shutdown 优雅停机：停消费 → 排空队列 → 关段压缩 → 尽力上传一轮。
func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil || s.spool == nil {
		return nil
	}
	s.lifecycle.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.lifecycle.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()

	s.sink.Stop(ctx)
	if err := s.spool.Close(); err != nil {
		logger.L().Warn("convlog.spool_close_failed", zap.Error(err))
	}
	s.uploader.FlushOnce(ctx)
	s.uploader.Stop()
	return nil
}

func (s *Service) loop(ctx context.Context) {
	settingsTicker := time.NewTicker(settingsRefreshPeriod)
	defer settingsTicker.Stop()
	retentionTicker := time.NewTicker(retentionPeriod)
	defer retentionTicker.Stop()

	s.runRetention(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-settingsTicker.C:
			s.settings.Invalidate()
			s.refreshSettings(ctx)
		case <-retentionTicker.C:
			s.runRetention(ctx)
		}
	}
}

// refreshSettings 重新解析配置，并把 spool 参数与对象存储客户端同步到最新。
func (s *Service) refreshSettings(ctx context.Context) {
	settings, s3 := s.settings.Effective(ctx)

	s.spool.SetOptions(SpoolOptions{
		Prefix:                settings.Prefix,
		RotateBytes:           settings.RotateBytes,
		RotateInterval:        settings.RotateInterval(),
		SpoolMaxBytes:         settings.SpoolMaxBytes,
		DiskMinFreeBytes:      settings.DiskMinFreeBytes,
		DiskCriticalFreeBytes: settings.DiskCriticalFreeBytes,
	})

	fingerprint := s3Fingerprint(s3)
	s.mu.Lock()
	s.current = settings
	s.currentS3 = s3
	changed := fingerprint != s.storeFingerprint
	s.storeFingerprint = fingerprint
	s.mu.Unlock()

	if !changed {
		return
	}
	if s3 == nil || s.factory == nil {
		s.uploader.SetStore(nil)
		return
	}
	store, err := s.factory(ctx, *s3)
	if err != nil {
		logger.L().Warn("convlog.object_store_build_failed; segments stay on local spool", zap.Error(err))
		s.uploader.SetStore(nil)
		return
	}
	s.uploader.SetStore(store)
}

func (s *Service) runRetention(ctx context.Context) {
	settings := s.snapshot()
	if settings == nil || s.repo == nil {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -settings.IndexRetentionDays)
	deleteCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	deleted, err := s.repo.DeleteOlderThan(deleteCtx, cutoff)
	if err != nil {
		logger.L().Warn("convlog.retention_cleanup_failed", zap.Error(err))
		return
	}
	if deleted > 0 {
		logger.L().Info("convlog.retention_cleanup",
			zap.Int64("deleted", deleted), zap.Time("cutoff", cutoff))
	}
}

func (s *Service) snapshot() *Settings {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	settings := s.current
	s.mu.RUnlock()
	return settings
}

// Enabled 是中间件热路径上的开关判断。未启动或未开启时返回 false，
// 中间件据此在第一条语句就返回，不包 writer、不读 body。
func (s *Service) Enabled() bool {
	if s == nil || s.spool == nil {
		return false
	}
	settings := s.snapshot()
	if settings == nil || !settings.Enabled {
		return false
	}
	// 磁盘濒临耗尽时彻底停捕获——这一档比"只写索引"更狠，因为连索引写入产生的
	// WAL 都可能压垮同一块盘。
	return s.spool.DiskState() != DiskCritical
}

// SampleAllows 是采样判定。它不依赖任何请求上下文，因此中间件在包 writer、
// 读 body 之前就能调用——没被采样的请求完全不付缓冲代价。
func (s *Service) SampleAllows() bool {
	settings := s.snapshot()
	if settings == nil {
		return false
	}
	if settings.SampleRate >= 1 {
		return true
	}
	return rand.Float64() < settings.SampleRate //nolint:gosec // 采样不需要密码学随机
}

// GroupExcluded 判断分组是否在排除名单里。分组要认证之后才知道，
// 所以这一步只能放在 c.Next() 之后。
func (s *Service) GroupExcluded(groupID int64) bool {
	settings := s.snapshot()
	return settings != nil && settings.GroupExcluded(groupID)
}

// Limits 返回中间件需要的请求/响应缓冲上限。
func (s *Service) Limits() (maxRequestBytes, maxResponseBytes int) {
	settings := s.snapshot()
	if settings == nil {
		return DefaultMaxRequestBytes, DefaultMaxResponseBytes
	}
	return settings.MaxRequestBytes, settings.MaxResponseBytes
}

// Capture 归一化并提交一条记录。整个方法不返回错误：捕获是纯旁路，
// 任何失败都只计数与降级，绝不冒泡到网关。
func (s *Service) Capture(input CaptureInput) {
	if s == nil || s.sink == nil {
		return
	}
	settings := s.snapshot()
	if settings == nil || !settings.Enabled {
		return
	}

	protocol := DetectProtocol(input.Endpoint, input.RequestBody)
	aggregate := AggregateResponse(protocol, input.ResponseBody, input.ResponseTruncated)

	conversation := NormalizeRequest(protocol, input.RequestBody)
	conversation.Output = &aggregate.Output

	record := Record{
		SchemaVersion: RecordSchemaVersion,
		RequestID:     input.RequestID,
		CreatedAt:     input.StartedAt.UTC(),
		DurationMs:    int(input.Duration / time.Millisecond),
		StatusCode:    input.StatusCode,
		Stream:        input.Stream,
		Endpoint:      input.Endpoint,
		Protocol:      protocol,
		Identity:      input.Identity,
		Model: ModelInfo{
			Requested: input.RequestedModel,
			Upstream:  input.UpstreamModel,
			Response:  aggregate.ResponseModel,
		},
		Conversation: conversation,
		Usage:        aggregate.Usage,
		RawRequest:   redactJSON(decodeJSONBytes(input.RequestBody)),
	}

	line, err := json.Marshal(&record)
	if err != nil {
		logger.L().Warn("convlog.marshal_failed", zap.Error(err))
		return
	}

	row := IndexRow{
		RequestID:    input.RequestID,
		CreatedAt:    record.CreatedAt,
		UserID:       optionalID(input.Identity.UserID),
		APIKeyID:     optionalID(input.Identity.APIKeyID),
		AccountID:    optionalID(input.Identity.AccountID),
		GroupID:      optionalID(input.Identity.GroupID),
		UserEmail:    input.Identity.UserEmail,
		APIKeyName:   input.Identity.APIKeyName,
		AccountName:  input.Identity.AccountName,
		GroupName:    input.Identity.GroupName,
		Platform:     input.Identity.Platform,
		Protocol:     protocol,
		Endpoint:     truncateUTF8(input.Endpoint, 128),
		Model:        truncateUTF8(firstNonEmpty(input.RequestedModel, aggregate.ResponseModel), 255),
		Stream:       input.Stream,
		StatusCode:   input.StatusCode,
		DurationMs:   record.DurationMs,
		IPAddress:    truncateUTF8(input.IPAddress, 45),
		InputPreview: ExtractPreview(protocol, input.RequestBody, settings.PreviewBytes),
		InputBytes:   int64(len(input.RequestBody)),
		OutputBytes:  int64(len(input.ResponseBody)),
		InputTokens:  aggregate.Usage.InputTokens,
		OutputTokens: aggregate.Usage.OutputTokens,
	}

	s.sink.Submit(&queuedRecord{line: line, row: row})
}

// Search 执行 Beta 风控搜索。
func (s *Service) Search(ctx context.Context, filter SearchFilter) ([]IndexRow, *AccountSummary, error) {
	if s == nil || s.repo == nil {
		return nil, nil, fmt.Errorf("conversation capture is unavailable")
	}
	filter.Normalize()
	if err := filter.Validate(); err != nil {
		return nil, nil, err
	}
	rows, err := s.repo.Search(ctx, filter)
	if err != nil {
		return nil, nil, err
	}
	summary, err := s.repo.Summary(ctx, filter)
	if err != nil {
		// 聚合失败不该挡住主查询结果。
		logger.L().Warn("convlog.summary_failed", zap.Error(err))
		summary = nil
	}
	return rows, summary, nil
}

// FetchFullRecord 取回单条完整记录：优先读本地未上传的段，否则从对象存储下载。
func (s *Service) FetchFullRecord(ctx context.Context, requestID string) (json.RawMessage, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("conversation capture is unavailable")
	}
	row, err := s.repo.GetByRequestID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.ObjectKey) == "" {
		return nil, fmt.Errorf("%w: record was indexed while disk protection was active, full text was not persisted", ErrRecordNotFound)
	}

	if local := s.spool.LocalPathForObjectKey(row.ObjectKey); local != "" {
		if line, err := scanArchiveForRequest(local, requestID); err == nil {
			return line, nil
		}
	}

	store := s.uploader.currentStore()
	if store == nil {
		return nil, fmt.Errorf("%w: object storage is not configured", ErrRecordNotFound)
	}
	body, err := store.Get(ctx, row.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return scanReaderForRequest(body, requestID)
}

// Runtime 汇总运行态供后台展示。
func (s *Service) Runtime() RuntimeStats {
	if s == nil || s.spool == nil {
		return RuntimeStats{}
	}
	settings := s.snapshot()
	depth, capacity, queueBytes, queueMax, dropped, indexFailed := s.sink.Stats()
	spoolBytes, diskFree, spooled, spoolErr := s.spool.Stats()
	pending, uploaded, uploadFailed, uploadErr := s.uploader.Stats()

	stats := RuntimeStats{
		Enabled:            settings != nil && settings.Enabled,
		QueueDepth:         depth,
		QueueCapacity:      capacity,
		QueueBytes:         queueBytes,
		QueueMaxBytes:      queueMax,
		DroppedTotal:       dropped,
		SpooledTotal:       spooled,
		SpoolBytes:         spoolBytes,
		DiskFreeBytes:      diskFree,
		PendingUploads:     pending,
		UploadedTotal:      uploaded,
		UploadFailedTotal:  uploadFailed,
		IndexWriteFailed:   indexFailed,
		ObjectStoreEnabled: s.uploader.currentStore() != nil,
		LastError:          firstNonEmpty(uploadErr, spoolErr),
	}
	if settings != nil {
		stats.SpoolMaxBytes = settings.SpoolMaxBytes
	}
	switch s.spool.DiskState() {
	case DiskCritical:
		stats.Degraded = true
		stats.DegradedReason = "disk_critical"
	case DiskSpoolFull:
		stats.Degraded = true
		stats.DegradedReason = "spool_full"
	case DiskOK:
	}
	return stats
}

// Settings 暴露给管理 API。
func (s *Service) Settings() *SettingStore { return s.settings }

// Factory 暴露给管理 API 的"测试连接"。
func (s *Service) Factory() ObjectStoreFactory { return s.factory }

// ApplySettings 在后台保存配置后立即重建运行时。
func (s *Service) ApplySettings(ctx context.Context) {
	if s == nil || s.spool == nil {
		return
	}
	s.refreshSettings(ctx)
}

// scanArchiveForRequest 在本地 gzip 段里按 request_id 找到那一行。
func scanArchiveForRequest(path, requestID string) (json.RawMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return scanReaderForRequest(file, requestID)
}

func scanReaderForRequest(reader io.Reader, requestID string) (json.RawMessage, error) {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()

	scanner := bufio.NewScanner(gz)
	// 单条记录可能很大（长上下文 + 长输出），默认 64KB 的行上限不够。
	scanner.Buffer(make([]byte, 0, 64<<10), 32<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if gjson.GetBytes(line, "request_id").String() != requestID {
			continue
		}
		out := make(json.RawMessage, len(line))
		copy(out, line)
		return out, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, ErrRecordNotFound
}

func decodeJSONBytes(body []byte) any {
	if len(body) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}
	return out
}

func optionalID(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	value := id
	return &value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func s3Fingerprint(cfg *S3Config) string {
	if cfg == nil {
		return ""
	}
	return strings.Join([]string{cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.AccessKeyID, cfg.Prefix,
		fmt.Sprintf("%t", cfg.ForcePathStyle), fmt.Sprintf("%d", len(cfg.SecretAccessKey))}, "|")
}

// spoolDir 把 spool 放在 DATA_DIR 下，与 plugins/ 等本地状态同一位置
// （对齐 service.resolvePluginRootDir 的解析方式）。
func spoolDir() string {
	base := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if base == "" {
		base = "./data"
	}
	return filepath.Join(base, "convlog")
}

func instanceID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "node"
	}
	return host
}
