package convlog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSearchFilterNormalizeDefaultsToLast24h(t *testing.T) {
	filter := SearchFilter{AccountID: 7}
	filter.Normalize()

	require.Equal(t, DefaultSearchLimit, filter.Limit)
	require.WithinDuration(t, time.Now().UTC(), filter.End, time.Minute)
	require.InDelta(t, DefaultSearchSpan.Seconds(), filter.End.Sub(filter.Start).Seconds(), 1)
}

// 超过 30 天的跨度必须被压回上限，而不是原样打到数据库上。
func TestSearchFilterNormalizeClampsSpanAndLimit(t *testing.T) {
	end := time.Now().UTC()
	filter := SearchFilter{
		AccountID: 7,
		Start:     end.Add(-90 * 24 * time.Hour),
		End:       end,
		Limit:     10000,
	}
	filter.Normalize()

	require.Equal(t, MaxSearchLimit, filter.Limit)
	require.InDelta(t, MaxSearchSpan.Seconds(), filter.End.Sub(filter.Start).Seconds(), 1)
	require.NoError(t, filter.Validate())
}

// 账号与时间范围是后端硬边界：缺任何一个都必须拒绝，避免退化成全表扫描。
func TestSearchFilterValidateRejectsUnboundedQueries(t *testing.T) {
	now := time.Now().UTC()

	noAccount := SearchFilter{Start: now.Add(-time.Hour), End: now}
	require.ErrorIs(t, noAccount.Validate(), ErrAccountRequired)

	invertedRange := SearchFilter{AccountID: 1, Start: now, End: now.Add(-time.Hour)}
	require.ErrorIs(t, invertedRange.Validate(), ErrTimeRangeInvalid)

	tooWide := SearchFilter{AccountID: 1, Start: now.Add(-60 * 24 * time.Hour), End: now}
	require.ErrorIs(t, tooWide.Validate(), ErrRangeTooWide)
}

// 关键词里的 % / _ 是用户内容，不能变成 ILIKE 通配符。
func TestEscapeLikeNeutralizesWildcards(t *testing.T) {
	require.Equal(t, `100\%\_off`, escapeLike("100%_off"))
	require.Equal(t, `a\\b`, escapeLike(`a\b`))
}

func TestNormalizeSettingsClampsProtectionLimits(t *testing.T) {
	settings := &Settings{
		QueueCapacity:         1 << 20,
		QueueMaxBytes:         1 << 40,
		PreviewBytes:          1 << 20,
		IndexRetentionDays:    3650,
		SpoolMaxBytes:         1 << 50,
		DiskCriticalFreeBytes: 100 << 30,
		DiskMinFreeBytes:      8 << 30,
	}
	normalizeSettings(settings)

	require.Equal(t, MaxQueueCapacity, settings.QueueCapacity)
	require.EqualValues(t, MaxQueueMaxBytes, settings.QueueMaxBytes)
	require.Equal(t, MaxPreviewBytes, settings.PreviewBytes)
	require.Equal(t, MaxIndexRetentionDays, settings.IndexRetentionDays)
	// critical 水位必须比 min 更靠近磁盘耗尽，否则两档水位互相覆盖。
	require.LessOrEqual(t, settings.DiskCriticalFreeBytes, settings.DiskMinFreeBytes)
	require.Equal(t, "conversations/", settings.Prefix)
	require.EqualValues(t, 1, settings.SampleRate)
}

func TestSettingsGroupExcluded(t *testing.T) {
	settings := &Settings{ExcludedGroupIDs: []int64{3, 3, 0, -1, 5}}
	normalizeSettings(settings)

	require.Equal(t, []int64{3, 5}, settings.ExcludedGroupIDs)
	require.True(t, settings.GroupExcluded(5))
	require.False(t, settings.GroupExcluded(9))
}
