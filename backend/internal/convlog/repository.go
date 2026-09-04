package convlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrRecordNotFound 表示索引里没有该 request_id。
var ErrRecordNotFound = errors.New("conversation capture record not found")

// 搜索参数校验错误。这些是后端硬边界，不依赖前端自觉。
var (
	ErrAccountRequired  = errors.New("account_id is required")
	ErrTimeRangeInvalid = errors.New("start must be before end")
	ErrRangeTooWide     = errors.New("time range exceeds the maximum allowed span")
)

// 账号名在捕获时拿不到（gin 上下文里只落了 account_id），因此读取时 LEFT JOIN
// accounts 补齐；快照列留作将来回填，非空时优先用快照，账号被删也仍有历史名字。
const indexSelect = `c.id, c.request_id, c.created_at, c.user_id, c.api_key_id, c.account_id, c.group_id,
	c.user_email, c.api_key_name,
	COALESCE(NULLIF(c.account_name, ''), a.name, '') AS account_name,
	c.group_name, c.platform, c.protocol, c.endpoint, c.model,
	c.stream, c.status_code, c.duration_ms, c.ip_address, c.input_preview, c.input_bytes,
	c.output_bytes, c.input_tokens, c.output_tokens, c.object_key`

const indexFrom = ` FROM conversation_capture_index c LEFT JOIN accounts a ON a.id = c.account_id`

// Repository 读写 conversation_capture_index。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// InsertBatch 批量写索引行。整批失败只返回错误由调用方计数，不重试——
// 索引是尽力而为的观测数据，不值得为它占住连接池。
func (r *Repository) InsertBatch(ctx context.Context, rows []IndexRow) error {
	if r == nil || r.db == nil || len(rows) == 0 {
		return nil
	}

	// 列顺序必须与下面 args 的追加顺序严格一致。
	const insertColumns = `request_id, created_at, user_id, api_key_id, account_id, group_id,
		user_email, api_key_name, account_name, group_name, platform, protocol, endpoint, model,
		stream, status_code, duration_ms, ip_address, input_preview, input_bytes, output_bytes,
		input_tokens, output_tokens, object_key`
	const columnCount = 24

	placeholders := make([]string, 0, len(rows))
	args := make([]any, 0, len(rows)*columnCount)
	for i, row := range rows {
		base := i * columnCount
		slots := make([]string, columnCount)
		for j := range slots {
			slots[j] = fmt.Sprintf("$%d", base+j+1)
		}
		placeholders = append(placeholders, "("+strings.Join(slots, ",")+")")
		args = append(args,
			row.RequestID, row.CreatedAt, row.UserID, row.APIKeyID, row.AccountID, row.GroupID,
			row.UserEmail, row.APIKeyName, row.AccountName, row.GroupName, row.Platform,
			row.Protocol, row.Endpoint, row.Model, row.Stream, row.StatusCode, row.DurationMs,
			row.IPAddress, row.InputPreview, row.InputBytes, row.OutputBytes, row.InputTokens,
			row.OutputTokens, row.ObjectKey,
		)
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO conversation_capture_index (`+insertColumns+`) VALUES `+strings.Join(placeholders, ","),
		args...)
	return err
}

// Search 执行 Beta 风控搜索。filter 必须已经过 Normalize。
func (r *Repository) Search(ctx context.Context, filter SearchFilter) ([]IndexRow, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("conversation capture repository is unavailable")
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}

	args := []any{filter.AccountID, filter.Start, filter.End}
	where := "c.account_id = $1 AND c.created_at >= $2 AND c.created_at < $3"
	if filter.UserID != nil {
		args = append(args, *filter.UserID)
		where += fmt.Sprintf(" AND c.user_id = $%d", len(args))
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		args = append(args, "%"+escapeLike(keyword)+"%")
		where += fmt.Sprintf(" AND c.input_preview ILIKE $%d ESCAPE '\\'", len(args))
	}
	args = append(args, filter.Limit)

	query := `SELECT ` + indexSelect + indexFrom + ` WHERE ` + where +
		fmt.Sprintf(" ORDER BY c.created_at DESC, c.id DESC LIMIT $%d", len(args))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanIndexRows(rows)
}

// GetByRequestID 取单行索引，用于"查看全文"前的定位。
func (r *Repository) GetByRequestID(ctx context.Context, requestID string) (*IndexRow, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("conversation capture repository is unavailable")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, ErrRecordNotFound
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+indexSelect+indexFrom+` WHERE c.request_id = $1 ORDER BY c.id DESC LIMIT 1`,
		requestID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	list, err := scanIndexRows(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrRecordNotFound
	}
	return &list[0], nil
}

// DeleteOlderThan 执行保留期清理，返回删除行数。
func (r *Repository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, nil
	}
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM conversation_capture_index WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// AccountSummary 是某账号在时间窗内的聚合统计，供风控页顶部展示。
type AccountSummary struct {
	Total       int64 `json:"total"`
	UserCount   int64 `json:"user_count"`
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
}

// Summary 返回账号 + 时间窗的聚合。走的是与 Search 相同的索引。
func (r *Repository) Summary(ctx context.Context, filter SearchFilter) (*AccountSummary, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("conversation capture repository is unavailable")
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT user_id), COALESCE(SUM(input_bytes),0), COALESCE(SUM(output_bytes),0)
		FROM conversation_capture_index
		WHERE account_id = $1 AND created_at >= $2 AND created_at < $3`,
		filter.AccountID, filter.Start, filter.End)

	var summary AccountSummary
	if err := row.Scan(&summary.Total, &summary.UserCount, &summary.InputBytes, &summary.OutputBytes); err != nil {
		return nil, err
	}
	return &summary, nil
}

// Normalize 补齐默认值并把参数收敛到硬边界内。
func (f *SearchFilter) Normalize() {
	now := time.Now().UTC()
	if f.End.IsZero() {
		f.End = now
	}
	if f.Start.IsZero() {
		f.Start = f.End.Add(-DefaultSearchSpan)
	}
	if f.End.Sub(f.Start) > MaxSearchSpan {
		f.Start = f.End.Add(-MaxSearchSpan)
	}
	if f.Limit <= 0 {
		f.Limit = DefaultSearchLimit
	}
	if f.Limit > MaxSearchLimit {
		f.Limit = MaxSearchLimit
	}
	f.Keyword = strings.TrimSpace(f.Keyword)
}

// Validate 强制"必须指定账号 + 时间范围"，拒绝退化成全表扫描的查询。
func (f *SearchFilter) Validate() error {
	if f.AccountID <= 0 {
		return ErrAccountRequired
	}
	if !f.Start.Before(f.End) {
		return ErrTimeRangeInvalid
	}
	if f.End.Sub(f.Start) > MaxSearchSpan {
		return ErrRangeTooWide
	}
	return nil
}

func scanIndexRows(rows *sql.Rows) ([]IndexRow, error) {
	var out []IndexRow
	for rows.Next() {
		var row IndexRow
		if err := rows.Scan(
			&row.ID, &row.RequestID, &row.CreatedAt, &row.UserID, &row.APIKeyID, &row.AccountID,
			&row.GroupID, &row.UserEmail, &row.APIKeyName, &row.AccountName, &row.GroupName,
			&row.Platform, &row.Protocol, &row.Endpoint, &row.Model, &row.Stream, &row.StatusCode,
			&row.DurationMs, &row.IPAddress, &row.InputPreview, &row.InputBytes, &row.OutputBytes,
			&row.InputTokens, &row.OutputTokens, &row.ObjectKey,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// escapeLike 转义 ILIKE 通配符，避免用户输入的 % / _ 变成模式匹配。
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}
