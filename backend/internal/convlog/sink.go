package convlog

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	indexBatchSize     = 100
	indexFlushInterval = time.Second
	maintenancePeriod  = 30 * time.Second
	spoolFlushPeriod   = 5 * time.Second

	// indexRowOverheadBytes 是每条记录在队列里除 JSONL 行以外的粗略开销
	// （索引行的字符串字段）。用于字节预算核算，不需要精确。
	indexRowOverheadBytes = 4096
)

// queuedRecord 是已序列化、可直接落盘的一条记录。
//
// 序列化发生在请求 goroutine 里（响应已写完，不影响用户可见延迟），这样队列持有的
// 是最终字节而不是原始请求/响应体——内存占用可预测，也不会把 SSE 原文一路带进队列。
type queuedRecord struct {
	line []byte
	row  IndexRow
}

func (q *queuedRecord) size() int64 {
	return int64(len(q.line)) + indexRowOverheadBytes
}

// Sink 是有界的异步落盘管线：队列 → spool 段 → 批量写索引。
//
// 入队永不阻塞：队列满或字节预算耗尽时直接丢弃并计数。日志可以丢，网关不能卡。
type Sink struct {
	spool *Spool
	repo  *Repository

	mu       sync.Mutex
	queue    chan *queuedRecord
	capacity int
	maxBytes int64
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	queueBytes   atomic.Int64
	droppedTotal atomic.Uint64
	indexFailed  atomic.Uint64
}

func NewSink(spool *Spool, repo *Repository, capacity int, maxBytes int64) *Sink {
	if capacity <= 0 {
		capacity = DefaultQueueCapacity
	}
	if maxBytes <= 0 {
		maxBytes = DefaultQueueMaxBytes
	}
	return &Sink{
		spool:    spool,
		repo:     repo,
		queue:    make(chan *queuedRecord, capacity),
		capacity: capacity,
		maxBytes: maxBytes,
	}
}

// Submit 非阻塞入队。返回 false 表示已丢弃。
func (s *Sink) Submit(rec *queuedRecord) bool {
	if s == nil || rec == nil {
		return false
	}
	s.mu.Lock()
	queue := s.queue
	s.mu.Unlock()
	if queue == nil {
		return false
	}

	size := rec.size()
	// 先占字节预算再尝试入队：只限条数的话，2 万条各含 MB 级正文照样能吃光 RAM。
	if s.queueBytes.Add(size) > s.maxBytes {
		s.queueBytes.Add(-size)
		s.droppedTotal.Add(1)
		return false
	}
	select {
	case queue <- rec:
		return true
	default:
		s.queueBytes.Add(-size)
		s.droppedTotal.Add(1)
		return false
	}
}

// Stats 返回队列运行态。
func (s *Sink) Stats() (depth int, capacity int, bytes, maxBytes int64, dropped, indexFailed uint64) {
	s.mu.Lock()
	queue := s.queue
	s.mu.Unlock()
	if queue != nil {
		depth = len(queue)
	}
	return depth, s.capacity, s.queueBytes.Load(), s.maxBytes, s.droppedTotal.Load(), s.indexFailed.Load()
}

// Start 起消费循环与维护定时器。
func (s *Sink) Start() {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.consume(ctx)
	}()
	go func() {
		defer s.wg.Done()
		s.maintain(ctx)
	}()
}

// Stop 停止消费并把队列里剩下的记录尽力落盘。
func (s *Sink) Stop(ctx context.Context) {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.drain(ctx)
}

func (s *Sink) consume(ctx context.Context) {
	pending := make([]IndexRow, 0, indexBatchSize)
	ticker := time.NewTicker(indexFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.flushIndex(context.Background(), pending)
			return
		case rec := <-s.queue:
			s.queueBytes.Add(-rec.size())
			if row, ok := s.persist(rec); ok {
				pending = append(pending, row)
			}
			if len(pending) >= indexBatchSize {
				s.flushIndex(ctx, pending)
				pending = pending[:0]
			}
		case <-ticker.C:
			if len(pending) > 0 {
				s.flushIndex(ctx, pending)
				pending = pending[:0]
			}
		}
	}
}

// drain 在停机时把队列里残留的记录写完，避免优雅重启丢掉最后一批语料。
func (s *Sink) drain(ctx context.Context) {
	pending := make([]IndexRow, 0, indexBatchSize)
	for {
		select {
		case rec := <-s.queue:
			s.queueBytes.Add(-rec.size())
			if row, ok := s.persist(rec); ok {
				pending = append(pending, row)
			}
		default:
			s.flushIndex(ctx, pending)
			return
		}
	}
}

// persist 把一条记录写进 spool 段，并回填索引行的 object_key。
// 磁盘保护生效时 object_key 留空——索引照写，全文这次不落盘。
func (s *Sink) persist(rec *queuedRecord) (IndexRow, bool) {
	objectKey, err := s.spool.Append(rec.line)
	switch {
	case err == nil:
		rec.row.ObjectKey = objectKey
	case errors.Is(err, ErrSpoolPaused):
		rec.row.ObjectKey = ""
	default:
		rec.row.ObjectKey = ""
		logger.L().Warn("convlog.spool_append_failed; index row keeps an empty object_key", zap.Error(err))
	}
	return rec.row, true
}

func (s *Sink) flushIndex(ctx context.Context, rows []IndexRow) {
	if len(rows) == 0 || s.repo == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.repo.InsertBatch(writeCtx, rows); err != nil {
		s.indexFailed.Add(uint64(len(rows)))
		logger.L().Warn("convlog.index_write_failed", zap.Int("rows", len(rows)), zap.Error(err))
	}
}

// maintain 承担所有周期性工作：磁盘水位、缓冲刷新、到期滚动。
// 集中在一个 goroutine 里，避免为每件小事各起一个定时器。
func (s *Sink) maintain(ctx context.Context) {
	disk := time.NewTicker(maintenancePeriod)
	defer disk.Stop()
	flush := time.NewTicker(spoolFlushPeriod)
	defer flush.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-disk.C:
			s.spool.refreshDiskState()
			if err := s.spool.RotateIfDue(); err != nil {
				logger.L().Warn("convlog.rotate_failed", zap.Error(err))
			}
			if state := s.spool.DiskState(); state != DiskOK {
				logger.L().Warn("convlog.disk_guard_active; capture degraded",
					zap.Int("disk_state", int(state)))
			}
		case <-flush.C:
			s.spool.Flush()
		}
	}
}
