package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/logging"
)

// LogRepository persists, searches, and clears management logs in the shared application database.
type LogRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewLogRepository binds log persistence to the migrated system_logs table.
func NewLogRepository(db *sql.DB, dialect Dialect) *LogRepository {
	return &LogRepository{db: db, dialect: dialect}
}

// Append stores one log record including all caller-provided attributes without redaction.
func (r *LogRepository) Append(ctx context.Context, record logging.Record) error {
	attrs, err := json.Marshal(record.Attrs)
	if err != nil {
		return fmt.Errorf("encode log attributes: %w", err)
	}
	query := fmt.Sprintf("INSERT INTO system_logs (logged_at, level, category, message, attrs_json) VALUES (%s)", placeholders(r.dialect, 5, 1))
	_, err = r.db.ExecContext(ctx, query, encodeTime(record.Time, r.dialect), record.Level, record.Category, record.Message, string(attrs))
	return err
}

// Search returns matching persistent logs newest first and the filtered total.
func (r *LogRepository) Search(ctx context.Context, query logging.Query) ([]logging.Record, int, error) {
	page, pageSize := query.Page, query.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > logging.MaxPageSize {
		pageSize = 100
	}
	conditions, args := r.filterConditions(query)
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM system_logs"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(
		"SELECT logged_at, level, category, message, attrs_json FROM system_logs%s ORDER BY logged_at DESC, id DESC LIMIT %s OFFSET %s",
		where, placeholder(r.dialect, len(args)-1), placeholder(r.dialect, len(args)),
	), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]logging.Record, 0)
	for rows.Next() {
		var item logging.Record
		var loggedAt any
		var attrs string
		if err := rows.Scan(&loggedAt, &item.Level, &item.Category, &item.Message, &attrs); err != nil {
			return nil, 0, err
		}
		item.Time, err = valueToTime(loggedAt)
		if err != nil {
			return nil, 0, fmt.Errorf("logged_at: %w", err)
		}
		if attrs != "" && attrs != "{}" && attrs != "null" {
			if err := json.Unmarshal([]byte(attrs), &item.Attrs); err != nil {
				return nil, 0, fmt.Errorf("decode log attributes: %w", err)
			}
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// Clear permanently deletes all persisted logs in one database statement.
func (r *LogRepository) Clear(ctx context.Context, query logging.Query) (int, error) {
	conditions, args := r.filterConditions(query)
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	result, err := r.db.ExecContext(ctx, "DELETE FROM system_logs"+where, args...)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

// DeleteBefore removes records older than cutoff.
func (r *LogRepository) DeleteBefore(ctx context.Context, before time.Time) (int, error) {
	result, err := r.db.ExecContext(ctx, "DELETE FROM system_logs WHERE logged_at < "+placeholder(r.dialect, 1), encodeTime(before.UTC(), r.dialect))
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func (r *LogRepository) filterConditions(query logging.Query) ([]string, []any) {
	conditions := make([]string, 0, 5)
	args := make([]any, 0, 5)
	add := func(column string, value any) {
		args = append(args, value)
		conditions = append(conditions, column+" = "+placeholder(r.dialect, len(args)))
	}
	if query.Level != "" {
		add("level", query.Level)
	}
	if query.Category != "" {
		add("category", query.Category)
	}
	if keyword := strings.TrimSpace(query.Keyword); keyword != "" {
		args = append(args, "%"+strings.ToLower(keyword)+"%")
		conditions = append(conditions, "LOWER(message) LIKE "+placeholder(r.dialect, len(args)))
	}
	if query.StartTime != nil {
		args = append(args, encodeTime(query.StartTime.UTC(), r.dialect))
		conditions = append(conditions, "logged_at >= "+placeholder(r.dialect, len(args)))
	}
	if query.EndTime != nil {
		args = append(args, encodeTime(query.EndTime.UTC(), r.dialect))
		conditions = append(conditions, "logged_at <= "+placeholder(r.dialect, len(args)))
	}
	return conditions, args
}
