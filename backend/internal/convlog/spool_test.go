package convlog

import (
	"bufio"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestSpool(t *testing.T, mutate func(*SpoolOptions)) *Spool {
	t.Helper()
	options := SpoolOptions{
		Prefix:                "conversations/",
		RotateBytes:           1 << 20,
		RotateInterval:        time.Hour,
		SpoolMaxBytes:         1 << 30,
		DiskMinFreeBytes:      0,
		DiskCriticalFreeBytes: 0,
	}
	if mutate != nil {
		mutate(&options)
	}
	spool, err := NewSpool(t.TempDir(), "test-node", options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = spool.Close() })
	return spool
}

func readArchiveLines(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	reader, err := gzip.NewReader(file)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	var lines []string
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	require.NoError(t, scanner.Err())
	return lines
}

func TestSpoolAppendRotateProducesGzipArchive(t *testing.T) {
	spool := newTestSpool(t, nil)

	key, err := spool.Append([]byte(`{"request_id":"a"}`))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(key, "conversations/year="), "key must be hive-partitioned: %s", key)
	require.True(t, strings.HasSuffix(key, ".jsonl.gz"))

	secondKey, err := spool.Append([]byte(`{"request_id":"b"}`))
	require.NoError(t, err)
	require.Equal(t, key, secondKey, "both records land in the same open segment")

	require.NoError(t, spool.Rotate())

	archives, err := spool.PendingArchives()
	require.NoError(t, err)
	require.Len(t, archives, 1)
	require.Equal(t, key, spool.ObjectKeyFor(archives[0]))
	require.Equal(t, []string{`{"request_id":"a"}`, `{"request_id":"b"}`}, readArchiveLines(t, archives[0]))
}

// 段的 object key 在创建时算定并落 sidecar；即使之后管理员改了 Prefix，
// 已写入索引的那个 key 也必须保持不变，否则"查看全文"会找不到对象。
func TestSpoolObjectKeyStableAcrossPrefixChange(t *testing.T) {
	spool := newTestSpool(t, nil)

	key, err := spool.Append([]byte(`{"request_id":"a"}`))
	require.NoError(t, err)
	require.NoError(t, spool.Rotate())

	spool.SetOptions(SpoolOptions{
		Prefix:         "changed/",
		RotateBytes:    1 << 20,
		RotateInterval: time.Hour,
		SpoolMaxBytes:  1 << 30,
	})

	archives, err := spool.PendingArchives()
	require.NoError(t, err)
	require.Len(t, archives, 1)
	require.Equal(t, key, spool.ObjectKeyFor(archives[0]))
}

func TestSpoolRotatesOnSizeLimit(t *testing.T) {
	spool := newTestSpool(t, func(o *SpoolOptions) { o.RotateBytes = 32 })

	line := []byte(strings.Repeat("x", 40))
	_, err := spool.Append(line)
	require.NoError(t, err)
	_, err = spool.Append(line)
	require.NoError(t, err)
	require.NoError(t, spool.Rotate())

	archives, err := spool.PendingArchives()
	require.NoError(t, err)
	require.Len(t, archives, 2, "each oversized record should close the previous segment")
}

// 进程被 kill 时会留下未压缩的 active-*.jsonl；启动扫描必须把它补压缩，不能丢数据。
func TestSpoolRecoverCompressesOrphanedActiveSegment(t *testing.T) {
	spool := newTestSpool(t, nil)

	_, err := spool.Append([]byte(`{"request_id":"orphan"}`))
	require.NoError(t, err)
	spool.Flush()
	// 模拟崩溃：丢掉内存里的段句柄，磁盘上留下 active- 文件。
	spool.active = nil

	require.NoError(t, spool.Recover())

	archives, err := spool.PendingArchives()
	require.NoError(t, err)
	require.Len(t, archives, 1)
	require.Equal(t, []string{`{"request_id":"orphan"}`}, readArchiveLines(t, archives[0]))

	entries, err := os.ReadDir(spool.Dir())
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), activePrefix), "orphaned active segment must be gone")
	}
}

func TestSpoolRemoveDeletesArchiveAndSidecar(t *testing.T) {
	spool := newTestSpool(t, nil)
	_, err := spool.Append([]byte(`{"request_id":"a"}`))
	require.NoError(t, err)
	require.NoError(t, spool.Rotate())

	archives, err := spool.PendingArchives()
	require.NoError(t, err)
	require.Len(t, archives, 1)

	spool.Remove(archives[0])

	remaining, err := spool.PendingArchives()
	require.NoError(t, err)
	require.Empty(t, remaining)

	id := strings.TrimSuffix(filepath.Base(archives[0]), archivedSuffix)
	_, err = os.Stat(filepath.Join(spool.Dir(), id+keySuffix))
	require.True(t, os.IsNotExist(err), "sidecar key file must be removed with the archive")
}

// spool 触顶后必须停止写盘并返回 ErrSpoolPaused，调用方据此只写索引。
func TestSpoolPausesWhenOverCapacity(t *testing.T) {
	spool := newTestSpool(t, func(o *SpoolOptions) { o.SpoolMaxBytes = 64 << 20 })

	spool.spoolBytes.Store(64 << 20)
	spool.refreshDiskState()
	require.Equal(t, DiskSpoolFull, spool.DiskState())

	_, err := spool.Append([]byte(`{"request_id":"a"}`))
	require.ErrorIs(t, err, ErrSpoolPaused)
}

func TestSpoolDiskGuardWatermarks(t *testing.T) {
	spool := newTestSpool(t, nil)

	// 磁盘余量取不到（-1，例如 Windows）时不应误判为告急。
	spool.diskFree.Store(-1)
	spool.SetOptions(SpoolOptions{
		Prefix: "p/", RotateBytes: 1 << 20, RotateInterval: time.Hour,
		SpoolMaxBytes: 1 << 30, DiskMinFreeBytes: 8 << 30, DiskCriticalFreeBytes: 5 << 30,
	})
	require.NotEqual(t, DiskCritical, spool.DiskState())
}

func TestBuildObjectKeyPartitions(t *testing.T) {
	at := time.Date(2026, 9, 4, 10, 30, 0, 0, time.UTC)
	key := buildObjectKey("conversations/", "seg1", at)
	require.Equal(t, "conversations/year=2026/month=09/day=04/hour=10/seg1.jsonl.gz", key)
}
