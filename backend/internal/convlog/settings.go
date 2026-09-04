package convlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// ErrIncomplete 表示开关已打开但对象存储凭证不全。此时捕获仍可运行（只写本地 spool
// 与索引），但不会上传，后台需要显式提示而不是静默假装健康。
var ErrIncomplete = errors.New("conversation capture object storage is enabled but bucket/access_key_id/secret_access_key are incomplete")

// ErrEncryptionKeyNotConfigured 与备份/生图配置一致：拒绝用每次启动都变化的临时密钥
// 加密，否则重启后密文再也解不开。
var ErrEncryptionKeyNotConfigured = errors.New("secret encryption key is not configured; refusing to persist an unrecoverable secret")

// S3Config 是解析后的对象存储运行时配置（SecretAccessKey 已解密）。
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string //nolint:revive // field name follows AWS convention
	Prefix          string
	ForcePathStyle  bool
}

// IsConfigured 判断凭证是否齐全到可以建客户端。
func (c *S3Config) IsConfigured() bool {
	return c != nil && c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// ObjectStore 是 convlog 需要的最小对象存储能力，由 repository 层实现。
type ObjectStore interface {
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// ObjectStoreFactory 由 repository 层注入，把配置变成一个可用的对象存储实现。
// 与 service.ImageStorageFactory 同样的注入方式，避免本包反向依赖 repository。
type ObjectStoreFactory func(ctx context.Context, cfg S3Config) (ObjectStore, error)

// BackupCredentials 是复用"数据备份"页 S3 凭证所需的最小能力，由 *service.BackupService 实现。
type BackupCredentials interface {
	ResolveS3Credentials(ctx context.Context) (*service.BackupS3Config, error)
	EncryptionKeyConfigured() bool
}

// SettingStore 读写后台设置，并把结果解析成运行时配置。
//
// 解析结果带缓存：网关每次请求都要判断功能是否开启，不能每次都查库。保存设置时
// Invalidate 清缓存，下一次请求即重建——后台开关立即生效、无需重启。
type SettingStore struct {
	settingRepo service.SettingRepository
	encryptor   service.SecretEncryptor
	backup      BackupCredentials

	mu       sync.RWMutex
	resolved bool
	cached   *Settings
	cachedS3 *S3Config
}

func NewSettingStore(settingRepo service.SettingRepository, encryptor service.SecretEncryptor, backup BackupCredentials) *SettingStore {
	return &SettingStore{settingRepo: settingRepo, encryptor: encryptor, backup: backup}
}

// Effective 返回归一化后的设置与解析后的 S3 配置（可能为 nil，表示未配置对象存储）。
func (s *SettingStore) Effective(ctx context.Context) (*Settings, *S3Config) {
	if s == nil {
		return defaultSettings(), nil
	}
	s.mu.RLock()
	if s.resolved {
		settings, s3 := s.cached, s.cachedS3
		s.mu.RUnlock()
		return settings, s3
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolved {
		return s.cached, s.cachedS3
	}

	settings, err := s.load(ctx)
	if err != nil {
		logger.L().Warn("convlog.settings_load_failed; capture stays disabled", zap.Error(err))
		settings = defaultSettings()
	}
	if settings == nil {
		settings = defaultSettings()
	}
	normalizeSettings(settings)

	var s3 *S3Config
	if settings.Enabled {
		resolved, resolveErr := s.resolveS3(ctx, settings)
		switch {
		case resolveErr != nil:
			logger.L().Warn("convlog.object_store_unavailable; records stay on local spool only", zap.Error(resolveErr))
		case resolved.IsConfigured():
			s3 = resolved
		default:
			logger.L().Warn("convlog.object_store_incomplete; records stay on local spool only")
		}
	}

	s.resolved = true
	s.cached = settings
	s.cachedS3 = s3
	return settings, s3
}

// Invalidate 丢弃缓存，使下一次调用按最新设置重新解析。
func (s *SettingStore) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.resolved = false
	s.cached = nil
	s.cachedS3 = nil
	s.mu.Unlock()
}

// Get 返回后台设置（SecretAccessKey 已脱敏）。
func (s *SettingStore) Get(ctx context.Context) (*Settings, error) {
	settings, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		settings = defaultSettings()
	}
	normalizeSettings(settings)
	settings.SecretAccessKey = ""
	return settings, nil
}

// SecretConfigured 供前端展示"已配置"占位符。
func (s *SettingStore) SecretConfigured(ctx context.Context) bool {
	settings, err := s.load(ctx)
	if err != nil || settings == nil {
		return false
	}
	if settings.ReuseBackupS3 {
		cfg, err := s.backupCredentials(ctx)
		return err == nil && cfg != nil && cfg.SecretAccessKey != ""
	}
	return settings.SecretAccessKey != ""
}

// Update 保存设置并立即生效。SecretAccessKey 留空表示沿用已保存的值。
func (s *SettingStore) Update(ctx context.Context, in Settings) (*Settings, error) {
	normalizeSettings(&in)

	switch {
	case in.ReuseBackupS3:
		// 复用备份凭证时不落自己的密钥，避免同一份密钥在库里存两份。
		in.Endpoint, in.Region, in.AccessKeyID, in.SecretAccessKey = "", "", "", ""
		in.ForcePathStyle = false
	case in.SecretAccessKey == "":
		if old, err := s.load(ctx); err == nil && old != nil {
			in.SecretAccessKey = old.SecretAccessKey
		}
	default:
		if s.backup == nil || !s.backup.EncryptionKeyConfigured() {
			return nil, ErrEncryptionKeyNotConfigured
		}
		encrypted, err := s.encryptor.Encrypt(in.SecretAccessKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt secret: %w", err)
		}
		in.SecretAccessKey = encrypted
	}

	data, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshal conversation capture settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyConfig, string(data)); err != nil {
		return nil, fmt.Errorf("save conversation capture settings: %w", err)
	}
	s.Invalidate()

	in.SecretAccessKey = ""
	return &in, nil
}

// TestConnection 用给定设置试建一次客户端，用于后台的"测试连接"按钮。
func (s *SettingStore) TestConnection(ctx context.Context, in Settings, factory ObjectStoreFactory) error {
	normalizeSettings(&in)
	if !in.ReuseBackupS3 && in.SecretAccessKey == "" {
		if old, err := s.load(ctx); err == nil && old != nil {
			in.SecretAccessKey = old.SecretAccessKey
		}
	}
	cfg, err := s.resolveS3(ctx, &in)
	if err != nil {
		return err
	}
	if !cfg.IsConfigured() {
		return ErrIncomplete
	}
	if factory == nil {
		return errors.New("object store factory is unavailable")
	}
	_, err = factory(ctx, *cfg)
	return err
}

func (s *SettingStore) resolveS3(ctx context.Context, in *Settings) (*S3Config, error) {
	cfg := &S3Config{
		Endpoint:        in.Endpoint,
		Region:          in.Region,
		Bucket:          in.Bucket,
		AccessKeyID:     in.AccessKeyID,
		SecretAccessKey: in.SecretAccessKey,
		Prefix:          in.Prefix,
		ForcePathStyle:  in.ForcePathStyle,
	}

	if in.ReuseBackupS3 {
		backupCfg, err := s.backupCredentials(ctx)
		if err != nil {
			return nil, err
		}
		if backupCfg == nil {
			return nil, errors.New("conversation capture is set to reuse the backup S3 configuration, but no backup S3 configuration exists")
		}
		cfg.Endpoint = backupCfg.Endpoint
		cfg.Region = backupCfg.Region
		cfg.AccessKeyID = backupCfg.AccessKeyID
		cfg.SecretAccessKey = backupCfg.SecretAccessKey
		cfg.ForcePathStyle = backupCfg.ForcePathStyle
		if cfg.Bucket == "" {
			cfg.Bucket = backupCfg.Bucket
		}
		return cfg, nil
	}

	if cfg.SecretAccessKey != "" {
		decrypted, err := s.encryptor.Decrypt(cfg.SecretAccessKey)
		if err != nil {
			// 兼容未加密的旧数据，与备份配置的处理保持一致。
			logger.L().Warn("convlog secret decrypt failed; treating the stored value as plaintext", zap.Error(err))
		} else {
			cfg.SecretAccessKey = decrypted
		}
	}
	return cfg, nil
}

func (s *SettingStore) backupCredentials(ctx context.Context) (*service.BackupS3Config, error) {
	if s.backup == nil {
		return nil, errors.New("backup service is unavailable")
	}
	return s.backup.ResolveS3Credentials(ctx)
}

func (s *SettingStore) load(ctx context.Context) (*Settings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, nil //nolint:nilnil // no repository means no stored settings
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyConfig)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, nil //nolint:nilnil // never configured is a valid state
	}
	var settings Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Errorf("parse conversation capture settings: %w", err)
	}
	return &settings, nil
}

func defaultSettings() *Settings {
	s := &Settings{}
	normalizeSettings(s)
	return s
}

// normalizeSettings 收敛所有数值到安全区间。配置写错不能击穿磁盘/内存保护，
// 因此这里做的是钳制而不是报错。
func normalizeSettings(in *Settings) {
	in.Bucket = strings.TrimSpace(in.Bucket)
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.AccessKeyID = strings.TrimSpace(in.AccessKeyID)
	in.SecretAccessKey = strings.TrimSpace(in.SecretAccessKey)

	in.Region = strings.TrimSpace(in.Region)
	if in.Region == "" {
		in.Region = "auto"
	}
	in.Prefix = strings.TrimSpace(in.Prefix)
	if in.Prefix == "" {
		in.Prefix = "conversations/"
	}
	if !strings.HasSuffix(in.Prefix, "/") {
		in.Prefix += "/"
	}

	if in.SampleRate <= 0 || in.SampleRate > 1 {
		in.SampleRate = 1
	}

	in.QueueCapacity = clampInt(in.QueueCapacity, DefaultQueueCapacity, 100, MaxQueueCapacity)
	in.QueueMaxBytes = clampInt64(in.QueueMaxBytes, DefaultQueueMaxBytes, 8<<20, MaxQueueMaxBytes)
	in.MaxRequestBytes = clampInt(in.MaxRequestBytes, DefaultMaxRequestBytes, 1024, MaxMaxRequestBytes)
	in.MaxResponseBytes = clampInt(in.MaxResponseBytes, DefaultMaxResponseBytes, 1024, MaxMaxResponseBytes)
	in.PreviewBytes = clampInt(in.PreviewBytes, DefaultPreviewBytes, 64, MaxPreviewBytes)
	in.IndexRetentionDays = clampInt(in.IndexRetentionDays, DefaultIndexRetentionDays, 1, MaxIndexRetentionDays)
	in.RotateIntervalSeconds = clampInt(in.RotateIntervalSeconds, int(DefaultRotateInterval/time.Second), 30, 3600)

	in.RotateBytes = clampInt64(in.RotateBytes, DefaultRotateBytes, 1<<20, 512<<20)
	in.SpoolMaxBytes = clampInt64(in.SpoolMaxBytes, DefaultSpoolMaxBytes, 64<<20, 64<<30)
	in.DiskMinFreeBytes = clampInt64(in.DiskMinFreeBytes, DefaultDiskMinFreeBytes, 1<<30, 1<<40)
	in.DiskCriticalFreeBytes = clampInt64(in.DiskCriticalFreeBytes, DefaultDiskCriticalFreeBytes, 512<<20, 1<<40)
	if in.DiskCriticalFreeBytes > in.DiskMinFreeBytes {
		// critical 必须比 min 更靠近磁盘耗尽，否则两档水位会互相覆盖。
		in.DiskCriticalFreeBytes = in.DiskMinFreeBytes
	}

	in.ExcludedGroupIDs = dedupePositive(in.ExcludedGroupIDs)
}

// RotateInterval 返回滚动周期。
func (s *Settings) RotateInterval() time.Duration {
	return time.Duration(s.RotateIntervalSeconds) * time.Second
}

// GroupExcluded 判断分组是否在排除名单里。
func (s *Settings) GroupExcluded(groupID int64) bool {
	for _, id := range s.ExcludedGroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

func clampInt(value, fallback, minValue, maxValue int) int {
	if value <= 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampInt64(value, fallback, minValue, maxValue int64) int64 {
	if value <= 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func dedupePositive(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
