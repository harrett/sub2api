package convlog

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// uploadBackoff 是单个文件连续失败后的重试间隔（指数退避，封顶 5 分钟）。
// 单文件失败只推迟它自己，不阻塞其它文件。
const (
	uploadBackoffBase = 2 * time.Second
	uploadBackoffMax  = 5 * time.Minute
	uploadScanPeriod  = 15 * time.Second
)

// Uploader 把 spool 里已压缩的段异步搬到对象存储，成功后删除本地文件。
type Uploader struct {
	spool *Spool
	// store 由 Service 在设置变更时热替换；nil 表示对象存储未配置，段留在本地。
	storeMu sync.RWMutex
	store   ObjectStore

	failures map[string]*uploadAttempt
	mu       sync.Mutex

	uploadedTotal atomic.Uint64
	failedTotal   atomic.Uint64
	pending       atomic.Int64
	lastError     atomic.Value

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type uploadAttempt struct {
	failures int
	nextAt   time.Time
}

func NewUploader(spool *Spool) *Uploader {
	u := &Uploader{spool: spool, failures: make(map[string]*uploadAttempt)}
	u.lastError.Store("")
	return u
}

// SetStore 热替换对象存储客户端。传 nil 表示未配置：段继续堆在本地，直到配置补齐。
func (u *Uploader) SetStore(store ObjectStore) {
	u.storeMu.Lock()
	u.store = store
	u.storeMu.Unlock()
}

func (u *Uploader) currentStore() ObjectStore {
	u.storeMu.RLock()
	defer u.storeMu.RUnlock()
	return u.store
}

// Start 起后台扫描循环。重复调用是安全的（已启动则忽略）。
func (u *Uploader) Start() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		ticker := time.NewTicker(uploadScanPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				u.scanOnce(ctx)
			}
		}
	}()
}

// Stop 停止后台循环并等待当前上传结束。
func (u *Uploader) Stop() {
	u.mu.Lock()
	cancel := u.cancel
	u.cancel = nil
	u.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	u.wg.Wait()
}

// FlushOnce 在优雅退出时尽力把剩余段传完。
func (u *Uploader) FlushOnce(ctx context.Context) {
	u.scanOnce(ctx)
}

// Stats 返回运行态计数。
func (u *Uploader) Stats() (pending int, uploaded, failed uint64, lastError string) {
	errText, _ := u.lastError.Load().(string)
	return int(u.pending.Load()), u.uploadedTotal.Load(), u.failedTotal.Load(), errText
}

func (u *Uploader) scanOnce(ctx context.Context) {
	archives, err := u.spool.PendingArchives()
	if err != nil {
		u.recordError(err)
		return
	}
	u.pending.Store(int64(len(archives)))

	store := u.currentStore()
	if store == nil || len(archives) == 0 {
		return
	}

	now := time.Now()
	for _, path := range archives {
		if ctx.Err() != nil {
			return
		}
		if !u.dueForRetry(path, now) {
			continue
		}
		if err := u.uploadOne(ctx, store, path); err != nil {
			u.penalize(path, err)
			continue
		}
		u.clearPenalty(path)
		u.spool.Remove(path)
		u.uploadedTotal.Add(1)
		u.pending.Add(-1)
	}
	u.pruneStalePenalties(archives)
}

func (u *Uploader) uploadOne(ctx context.Context, store ObjectStore, path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 别的实例或上一轮已经处理掉了，不算失败。
			return nil
		}
		return err
	}
	defer func() { _ = file.Close() }()

	stat, err := file.Stat()
	if err != nil {
		return err
	}
	key := u.spool.ObjectKeyFor(path)
	uploadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return store.Put(uploadCtx, key, "application/gzip", file, stat.Size())
}

func (u *Uploader) dueForRetry(path string, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	attempt, ok := u.failures[path]
	return !ok || !now.Before(attempt.nextAt)
}

func (u *Uploader) penalize(path string, err error) {
	u.failedTotal.Add(1)
	u.recordError(err)
	u.mu.Lock()
	attempt, ok := u.failures[path]
	if !ok {
		attempt = &uploadAttempt{}
		u.failures[path] = attempt
	}
	attempt.failures++
	delay := uploadBackoffBase << min(attempt.failures-1, 8)
	if delay > uploadBackoffMax || delay <= 0 {
		delay = uploadBackoffMax
	}
	attempt.nextAt = time.Now().Add(delay)
	failures := attempt.failures
	u.mu.Unlock()

	logger.L().Warn("convlog.upload_failed; will retry with backoff",
		zap.String("archive", path), zap.Int("failures", failures), zap.Duration("retry_in", delay), zap.Error(err))
}

func (u *Uploader) clearPenalty(path string) {
	u.mu.Lock()
	delete(u.failures, path)
	u.mu.Unlock()
}

// pruneStalePenalties 清掉已经不存在的文件的退避记录，避免 map 无限增长。
func (u *Uploader) pruneStalePenalties(existing []string) {
	if len(u.failures) == 0 {
		return
	}
	alive := make(map[string]struct{}, len(existing))
	for _, path := range existing {
		alive[path] = struct{}{}
	}
	u.mu.Lock()
	for path := range u.failures {
		if _, ok := alive[path]; !ok {
			delete(u.failures, path)
		}
	}
	u.mu.Unlock()
}

func (u *Uploader) recordError(err error) {
	if err != nil {
		u.lastError.Store(err.Error())
	}
}
