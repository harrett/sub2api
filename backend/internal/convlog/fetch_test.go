package convlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// object_key 在段**打开时**就算定并写进索引行，但该段要到滚动、压缩、上传之后才会
// 出现在对象存储里。这段窗口内后台"查看全文"必须能读正在写的本地段——否则本地找不到、
// S3 又 NoSuchKey，就是线上看到的那种偶发 500。
func TestLocalPathFindsStillOpenSegment(t *testing.T) {
	spool := newTestSpool(t, nil)

	objectKey, err := spool.Append([]byte(`{"request_id":"live-1"}`))
	require.NoError(t, err)

	segment, ok := spool.LocalPathForObjectKey(objectKey)
	require.True(t, ok, "the open segment must be readable before it rotates")
	require.False(t, segment.Compressed)
	require.True(t, strings.HasPrefix(filepath.Base(segment.Path), activePrefix))

	line, err := scanSegmentForRequest(segment, "live-1")
	require.NoError(t, err)
	require.JSONEq(t, `{"request_id":"live-1"}`, string(line))
}

// 写缓冲有 64KB，刚发生的请求可能还没落盘；查找时必须先 flush，否则"最新那条"读不到。
func TestLocalPathFlushesPendingWrites(t *testing.T) {
	spool := newTestSpool(t, nil)

	objectKey, err := spool.Append([]byte(`{"request_id":"buffered"}`))
	require.NoError(t, err)

	// 未 flush 时文件还是空的，正是这条记录读不到的原因。
	active := filepath.Join(spool.Dir(), activePrefix+
		strings.TrimSuffix(filepath.Base(objectKey), archivedSuffix)+segmentSuffix)
	info, err := os.Stat(active)
	require.NoError(t, err)
	require.Zero(t, info.Size(), "precondition: the write is still buffered")

	segment, ok := spool.LocalPathForObjectKey(objectKey)
	require.True(t, ok)
	line, err := scanSegmentForRequest(segment, "buffered")
	require.NoError(t, err)
	require.JSONEq(t, `{"request_id":"buffered"}`, string(line))
}

// 滚动之后同一个 object_key 要转而命中已压缩的段。
func TestLocalPathFindsRotatedSegment(t *testing.T) {
	spool := newTestSpool(t, nil)

	objectKey, err := spool.Append([]byte(`{"request_id":"rotated"}`))
	require.NoError(t, err)
	require.NoError(t, spool.Rotate())

	segment, ok := spool.LocalPathForObjectKey(objectKey)
	require.True(t, ok)
	require.True(t, segment.Compressed)

	line, err := scanSegmentForRequest(segment, "rotated")
	require.NoError(t, err)
	require.JSONEq(t, `{"request_id":"rotated"}`, string(line))
}

// 段已上传并从本地删除后，本地查找应干净地miss，由调用方转向对象存储。
func TestLocalPathMissesAfterUpload(t *testing.T) {
	spool := newTestSpool(t, nil)

	objectKey, err := spool.Append([]byte(`{"request_id":"gone"}`))
	require.NoError(t, err)
	require.NoError(t, spool.Rotate())

	archives, err := spool.PendingArchives()
	require.NoError(t, err)
	require.Len(t, archives, 1)
	spool.Remove(archives[0])

	_, ok := spool.LocalPathForObjectKey(objectKey)
	require.False(t, ok)
}
