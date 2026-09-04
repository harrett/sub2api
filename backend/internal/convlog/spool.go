package convlog

import (
	"bufio"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// spool 文件命名：
//
//	active-<segment>.jsonl   正在写
//	<segment>.jsonl.gz       已压缩，待上传
//	<segment>.key            该段的对象存储 key（sidecar）
//
// sidecar 存在的原因：管理员随时可能改 Prefix，而索引行里的 object_key 是段创建时
// 算定的。把 key 落在段旁边，重启恢复和延迟上传都能拿到与索引一致的那一个 key。
const (
	activePrefix   = "active-"
	segmentSuffix  = ".jsonl"
	archivedSuffix = ".jsonl.gz"
	keySuffix      = ".key"
)

// DiskState 是磁盘保护状态机的三档水位。
type DiskState int32

const (
	// DiskOK 正常写盘。
	DiskOK DiskState = iota
	// DiskSpoolFull spool 触顶或磁盘余量低：停止写盘，只写 PostgreSQL 索引。
	DiskSpoolFull
	// DiskCritical 磁盘濒临耗尽：完全停止捕获。
	DiskCritical
)

// ErrSpoolPaused 表示磁盘保护生效，本条记录不落盘。调用方据此只写索引，不视为故障。
var ErrSpoolPaused = errors.New("conversation capture spool paused by disk guard")

// Spool 是受限的本地滚动写入器。所有导出方法并发安全。
type Spool struct {
	dir        string
	instanceID string

	mu      sync.Mutex
	active  *segment
	prefix  string
	options SpoolOptions

	diskState  atomic.Int32
	spoolBytes atomic.Int64
	diskFree   atomic.Int64
	lastError  atomic.Value

	spooledTotal atomic.Uint64
}

// SpoolOptions 是滚动与保护参数，随设置变更热更新。
type SpoolOptions struct {
	Prefix                string
	RotateBytes           int64
	RotateInterval        time.Duration
	SpoolMaxBytes         int64
	DiskMinFreeBytes      int64
	DiskCriticalFreeBytes int64
}

type segment struct {
	id        string
	objectKey string
	path      string
	file      *os.File
	buf       *bufio.Writer
	written   int64
	openedAt  time.Time
}

// NewSpool 创建 spool 目录并返回写入器。目录创建失败是致命的——调用方应据此不启用捕获。
func NewSpool(dir, instanceID string, options SpoolOptions) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create convlog spool dir: %w", err)
	}
	s := &Spool{dir: dir, instanceID: sanitizeSegmentToken(instanceID), options: options, prefix: options.Prefix}
	s.lastError.Store("")
	s.refreshDiskState()
	return s, nil
}

// SetOptions 热更新滚动与保护参数。Prefix 变更只影响之后新建的段。
func (s *Spool) SetOptions(options SpoolOptions) {
	s.mu.Lock()
	s.options = options
	s.prefix = options.Prefix
	s.mu.Unlock()
	s.refreshDiskState()
}

// Dir 返回 spool 目录。
func (s *Spool) Dir() string { return s.dir }

// DiskState 返回当前水位。
func (s *Spool) DiskState() DiskState { return DiskState(s.diskState.Load()) }

// Stats 返回运行态计数。
func (s *Spool) Stats() (spoolBytes, diskFree int64, spooled uint64, lastError string) {
	errText, _ := s.lastError.Load().(string)
	return s.spoolBytes.Load(), s.diskFree.Load(), s.spooledTotal.Load(), errText
}

// Append 把一行 JSONL 写入当前段，返回该段最终的对象存储 key。
// 磁盘保护生效时返回 ErrSpoolPaused，调用方应只写索引。
func (s *Spool) Append(line []byte) (string, error) {
	switch s.DiskState() {
	case DiskCritical, DiskSpoolFull:
		return "", ErrSpoolPaused
	case DiskOK:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.rotateIfNeededLocked(int64(len(line) + 1)); err != nil {
		return "", err
	}
	if s.active == nil {
		if err := s.openLocked(); err != nil {
			return "", err
		}
	}

	if _, err := s.active.buf.Write(line); err != nil {
		s.recordError(err)
		// 写失败的段可能已半行落盘；立即关段，下一条从新段开始，避免损坏整段。
		_ = s.closeActiveLocked()
		return "", err
	}
	if err := s.active.buf.WriteByte('\n'); err != nil {
		s.recordError(err)
		_ = s.closeActiveLocked()
		return "", err
	}
	written := int64(len(line) + 1)
	s.active.written += written
	s.spoolBytes.Add(written)
	s.spooledTotal.Add(1)
	return s.active.objectKey, nil
}

// Flush 把当前段的缓冲刷到磁盘。定时调用可把崩溃时的数据丢失窗口收敛到一个刷新周期。
func (s *Spool) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return
	}
	if err := s.active.buf.Flush(); err != nil {
		s.recordError(err)
	}
}

// Rotate 强制关闭并压缩当前段，用于优雅关闭与定时滚动。
func (s *Spool) Rotate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeActiveLocked()
}

// RotateIfDue 在当前段超过滚动周期时关段。低流量时没有新记录触发滚动，
// 段会一直挂着不上传——这个定时检查保证冷流量也能按时归档。
func (s *Spool) RotateIfDue() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || time.Since(s.active.openedAt) < s.options.RotateInterval {
		return nil
	}
	return s.closeActiveLocked()
}

func (s *Spool) rotateIfNeededLocked(incoming int64) error {
	if s.active == nil {
		return nil
	}
	overSize := s.active.written+incoming > s.options.RotateBytes
	overTime := time.Since(s.active.openedAt) >= s.options.RotateInterval
	if !overSize && !overTime {
		return nil
	}
	return s.closeActiveLocked()
}

func (s *Spool) openLocked() error {
	id := newSegmentID(s.instanceID)
	path := filepath.Join(s.dir, activePrefix+id+segmentSuffix)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		s.recordError(err)
		return fmt.Errorf("open convlog segment: %w", err)
	}
	objectKey := buildObjectKey(s.prefix, id, time.Now().UTC())
	if err := os.WriteFile(filepath.Join(s.dir, id+keySuffix), []byte(objectKey), 0o640); err != nil {
		// key sidecar 写不下去说明磁盘已经有问题，直接放弃这个段。
		_ = file.Close()
		_ = os.Remove(path)
		s.recordError(err)
		return fmt.Errorf("write convlog segment key: %w", err)
	}
	s.active = &segment{
		id:        id,
		objectKey: objectKey,
		path:      path,
		file:      file,
		buf:       bufio.NewWriterSize(file, spoolWriteBufferBytes),
		openedAt:  time.Now(),
	}
	return nil
}

// spoolWriteBufferBytes 是每段的写缓冲。缓冲越大 syscall 越少，崩溃丢失窗口越大；
// 64KB 在两者之间，配合定时 Flush 把窗口压到一个刷新周期内。
const spoolWriteBufferBytes = 64 << 10

// closeActiveLocked 关闭当前段并压缩成 .jsonl.gz。压缩失败时保留原始 .jsonl，
// 由启动恢复流程再试，绝不静默丢数据。
func (s *Spool) closeActiveLocked() error {
	active := s.active
	if active == nil {
		return nil
	}
	s.active = nil
	if err := active.buf.Flush(); err != nil {
		s.recordError(err)
	}
	if err := active.file.Close(); err != nil {
		s.recordError(err)
	}
	if active.written == 0 {
		// 空段没有价值，连同 sidecar 一起清掉。
		_ = os.Remove(active.path)
		_ = os.Remove(filepath.Join(s.dir, active.id+keySuffix))
		return nil
	}
	if err := s.compressSegment(active.path, filepath.Join(s.dir, active.id+archivedSuffix)); err != nil {
		s.recordError(err)
		return err
	}
	return nil
}

func (s *Spool) compressSegment(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open convlog segment for compression: %w", err)
	}
	defer func() { _ = src.Close() }()

	tmpPath := dstPath + ".tmp"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("create convlog archive: %w", err)
	}

	writer := gzip.NewWriter(dst)
	_, copyErr := io.Copy(writer, src)
	closeErr := writer.Close()
	syncErr := dst.Sync()
	dstCloseErr := dst.Close()
	if err := firstError(copyErr, closeErr, syncErr, dstCloseErr); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("compress convlog segment: %w", err)
	}

	// 先落成 .tmp 再原子改名：上传器只会看到完整的 .jsonl.gz。
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("finalize convlog archive: %w", err)
	}

	stat, statErr := os.Stat(dstPath)
	srcStat, srcStatErr := os.Stat(srcPath)
	if err := os.Remove(srcPath); err != nil {
		s.recordError(err)
	}
	if statErr == nil && srcStatErr == nil {
		// 用压缩后体积替换原始体积，spool 总量统计才不会长期虚高。
		s.spoolBytes.Add(stat.Size() - srcStat.Size())
	}
	return nil
}

// PendingArchives 列出待上传的 .jsonl.gz 段（按文件名升序，先进先出）。
func (s *Spool) PendingArchives() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), archivedSuffix) {
			continue
		}
		out = append(out, filepath.Join(s.dir, entry.Name()))
	}
	return out, nil
}

// ObjectKeyFor 返回某个已压缩段的对象存储 key：优先读 sidecar，缺失时按段 ID 推导。
func (s *Spool) ObjectKeyFor(archivePath string) string {
	id := strings.TrimSuffix(filepath.Base(archivePath), archivedSuffix)
	if raw, err := os.ReadFile(filepath.Join(s.dir, id+keySuffix)); err == nil {
		if key := strings.TrimSpace(string(raw)); key != "" {
			return key
		}
	}
	s.mu.Lock()
	prefix := s.prefix
	s.mu.Unlock()
	return buildObjectKey(prefix, id, segmentTime(id))
}

// LocalPathForObjectKey 在 spool 目录里找到某个对象 key 对应的本地段。
// 尚未上传时后台"查看全文"可以直接读本地，不必等上传完成。
func (s *Spool) LocalPathForObjectKey(objectKey string) string {
	base := filepath.Base(objectKey)
	if !strings.HasSuffix(base, archivedSuffix) {
		return ""
	}
	path := filepath.Join(s.dir, base)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// Remove 删除已成功上传的段及其 sidecar。
func (s *Spool) Remove(archivePath string) {
	if stat, err := os.Stat(archivePath); err == nil {
		s.spoolBytes.Add(-stat.Size())
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		s.recordError(err)
	}
	id := strings.TrimSuffix(filepath.Base(archivePath), archivedSuffix)
	_ = os.Remove(filepath.Join(s.dir, id+keySuffix))
}

// Recover 在启动时扫描 spool 目录：遗留的 active-*.jsonl 补压缩，
// 半截的 *.tmp 清掉，并重算 spool 总量。上一次进程被 kill 的数据不会丢。
func (s *Spool) Recover() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(s.dir, name)
		switch {
		case strings.HasSuffix(name, ".tmp"):
			_ = os.Remove(path)
		case strings.HasPrefix(name, activePrefix) && strings.HasSuffix(name, segmentSuffix):
			id := strings.TrimSuffix(strings.TrimPrefix(name, activePrefix), segmentSuffix)
			if err := s.compressSegment(path, filepath.Join(s.dir, id+archivedSuffix)); err != nil {
				s.recordError(err)
				continue
			}
			if info, statErr := os.Stat(filepath.Join(s.dir, id+archivedSuffix)); statErr == nil {
				total += info.Size()
			}
		default:
			if info, statErr := entry.Info(); statErr == nil {
				total += info.Size()
			}
		}
	}
	s.spoolBytes.Store(total)
	s.refreshDiskState()
	return nil
}

// refreshDiskState 重算水位。由后台定时器每 30s 调用一次——不能在每条记录上做 syscall。
func (s *Spool) refreshDiskState() {
	s.mu.Lock()
	options := s.options
	s.mu.Unlock()

	free := diskFreeBytes(s.dir)
	s.diskFree.Store(free)

	state := DiskOK
	switch {
	case free >= 0 && free < options.DiskCriticalFreeBytes:
		state = DiskCritical
	case free >= 0 && free < options.DiskMinFreeBytes:
		state = DiskSpoolFull
	case s.spoolBytes.Load() >= options.SpoolMaxBytes:
		state = DiskSpoolFull
	}
	s.diskState.Store(int32(state))
}

func (s *Spool) recordError(err error) {
	if err != nil {
		s.lastError.Store(err.Error())
	}
}

// Close 关闭当前段（压缩落盘），供优雅退出调用。
func (s *Spool) Close() error {
	return s.Rotate()
}

// buildObjectKey 生成 Hive 风格分区 key，便于后续 Athena/DuckDB 按分区裁剪。
func buildObjectKey(prefix, segmentID string, at time.Time) string {
	prefix = strings.TrimLeft(prefix, "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return fmt.Sprintf("%syear=%04d/month=%02d/day=%02d/hour=%02d/%s%s",
		prefix, at.Year(), int(at.Month()), at.Day(), at.Hour(), segmentID, archivedSuffix)
}

// newSegmentID 形如 20260904T100512Z-<instance>-<rand>，时间前缀让 key 推导与
// 文件名排序都能直接用。
func newSegmentID(instanceID string) string {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		// 随机数不可用时退化为纳秒，仍能保证同实例内唯一。
		return fmt.Sprintf("%s-%s-%d", time.Now().UTC().Format("20060102T150405Z"), instanceID, time.Now().UnixNano()%1e9)
	}
	return fmt.Sprintf("%s-%s-%s", time.Now().UTC().Format("20060102T150405Z"), instanceID, hex.EncodeToString(suffix))
}

// segmentTime 从段 ID 还原创建时间；解析不了就用当前时间（只影响推导出的分区路径）。
func segmentTime(segmentID string) time.Time {
	parts := strings.SplitN(segmentID, "-", 2)
	if len(parts) == 0 {
		return time.Now().UTC()
	}
	at, err := time.Parse("20060102T150405Z", parts[0])
	if err != nil {
		return time.Now().UTC()
	}
	return at
}

// sanitizeSegmentToken 保证实例标识不会把 '-' 或路径分隔符带进段 ID。
func sanitizeSegmentToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "node"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
