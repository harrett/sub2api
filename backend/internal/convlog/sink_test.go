package convlog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestRecord(size int) *queuedRecord {
	line := make([]byte, size)
	for i := range line {
		line[i] = 'x'
	}
	return &queuedRecord{line: line, row: IndexRow{RequestID: "r", CreatedAt: time.Now()}}
}

// 队列满时必须直接丢弃并计数，绝不阻塞写入方——日志可以丢，网关不能卡。
func TestSinkDropsWhenQueueIsFull(t *testing.T) {
	sink := NewSink(nil, nil, 2, DefaultQueueMaxBytes)

	require.True(t, sink.Submit(newTestRecord(8)))
	require.True(t, sink.Submit(newTestRecord(8)))
	require.False(t, sink.Submit(newTestRecord(8)), "third submit must be dropped")

	depth, capacity, _, _, dropped, _ := sink.Stats()
	require.Equal(t, 2, depth)
	require.Equal(t, 2, capacity)
	require.EqualValues(t, 1, dropped)
}

// 只限条数不够：字节预算必须独立生效，否则少量大记录就能吃光 RAM。
func TestSinkDropsWhenByteBudgetExhausted(t *testing.T) {
	// 预算刚好容纳一条（每条计入 indexRowOverheadBytes 的固定开销）。
	budget := int64(indexRowOverheadBytes + 16)
	sink := NewSink(nil, nil, 100, budget)

	require.True(t, sink.Submit(newTestRecord(16)))
	require.False(t, sink.Submit(newTestRecord(16)), "byte budget must reject the second record")

	_, _, bytes, maxBytes, dropped, _ := sink.Stats()
	require.Equal(t, budget, maxBytes)
	require.LessOrEqual(t, bytes, maxBytes)
	require.EqualValues(t, 1, dropped)
}

// 丢弃后必须把预留的字节还回去，否则队列会因为记账泄漏而永久卡死。
func TestSinkReleasesBudgetOnDrop(t *testing.T) {
	sink := NewSink(nil, nil, 1, DefaultQueueMaxBytes)

	require.True(t, sink.Submit(newTestRecord(16)))
	_, _, beforeBytes, _, _, _ := sink.Stats()
	require.False(t, sink.Submit(newTestRecord(16)))
	_, _, afterBytes, _, _, _ := sink.Stats()

	require.Equal(t, beforeBytes, afterBytes)
}

func TestSinkSubmitNilIsSafe(t *testing.T) {
	sink := NewSink(nil, nil, 4, DefaultQueueMaxBytes)
	require.False(t, sink.Submit(nil))

	var nilSink *Sink
	require.False(t, nilSink.Submit(newTestRecord(8)))
}

// persist 在磁盘保护生效时留空 object_key：索引照写，全文这次不落盘。
func TestSinkPersistLeavesEmptyObjectKeyWhenSpoolPaused(t *testing.T) {
	spool := newTestSpool(t, func(o *SpoolOptions) { o.SpoolMaxBytes = 64 << 20 })
	spool.spoolBytes.Store(64 << 20)
	spool.refreshDiskState()

	sink := NewSink(spool, nil, 4, DefaultQueueMaxBytes)
	row, ok := sink.persist(newTestRecord(8))
	require.True(t, ok)
	require.Empty(t, row.ObjectKey)
}

func TestSinkPersistFillsObjectKey(t *testing.T) {
	spool := newTestSpool(t, nil)
	sink := NewSink(spool, nil, 4, DefaultQueueMaxBytes)

	row, ok := sink.persist(newTestRecord(8))
	require.True(t, ok)
	require.NotEmpty(t, row.ObjectKey)
}
