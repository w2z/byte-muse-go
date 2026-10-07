package database

import (
	"bytemuse/backend/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// cloudUploadMigration creates only upload-owned state. Existing media and settings are not rewritten.
// record_key 为 SHA256 主键；state 为非空提交阶段及恢复查询索引；snapshot 为非空 JSON。
// 三列均无默认值，Save 同时维护状态索引与快照，不接受缺失值。
// Rollback keeps this additive table and disables the feature; no historical data restoration is necessary.
func cloudUploadMigration(dialect Dialect) Migration {
	payload := "TEXT"
	if dialect == DialectMySQL {
		payload = "LONGTEXT"
	}
	statements := []string{fmt.Sprintf("CREATE TABLE cloud_upload_records (record_key VARCHAR(64) NOT NULL PRIMARY KEY, state VARCHAR(16) NOT NULL, snapshot %s NOT NULL)", payload), "CREATE INDEX idx_cloud_upload_state ON cloud_upload_records(state)"}
	for _, seed := range []struct{ key, value string }{{"CLOUD_UPLOAD_PATHS", "[]"}, {"CLOUD_UPLOAD_ENABLE", "false"}, {"CLOUD_UPLOAD_CONFLICT", "skip"}} {
		query := fmt.Sprintf("INSERT INTO app_settings (setting_key,setting_value,is_secret,updated_at) VALUES (%s,%s,FALSE,%s)", sqlLiteral(seed.key), sqlLiteral(seed.value), currentTimestampExpression(dialect))
		if dialect == DialectMySQL {
			query = strings.Replace(query, "INSERT INTO", "INSERT IGNORE INTO", 1)
		} else {
			query += " ON CONFLICT(setting_key) DO NOTHING"
		}
		statements = append(statements, query)
	}
	return Migration{Version: 39, Name: "cloud_upload", Statements: statements}
}

// UploadRepository stores upload records in the application's versioned database.
type UploadRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewUploadRepository uses an already migrated database and performs no implicit DDL.
func NewUploadRepository(db *sql.DB, dialect Dialect) *UploadRepository {
	return &UploadRepository{db, dialect}
}

// Get returns nil for a source version without a prior upload attempt.
func (r *UploadRepository) Get(ctx context.Context, key string) (*domain.UploadRecord, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, (&CollectionRepository{dialect: r.dialect}).q("SELECT snapshot FROM cloud_upload_records WHERE record_key=?"), key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record domain.UploadRecord
	err = json.Unmarshal([]byte(raw), &record)
	return &record, err
}

// Save atomically replaces the whole record so target selection and state cannot diverge.
func (r *UploadRepository) Save(ctx context.Context, record domain.UploadRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	query := "INSERT INTO cloud_upload_records(record_key,state,snapshot) VALUES(?,?,?) ON CONFLICT(record_key) DO UPDATE SET state=excluded.state,snapshot=excluded.snapshot"
	if r.dialect == DialectMySQL {
		query = "INSERT INTO cloud_upload_records(record_key,state,snapshot) VALUES(?,?,?) ON DUPLICATE KEY UPDATE state=VALUES(state),snapshot=VALUES(snapshot)"
	}
	_, err = r.db.ExecContext(ctx, (&CollectionRepository{dialect: r.dialect}).q(query), record.Key, record.State, string(raw))
	return err
}

// PendingCommits returns unfinished transfers and recovery work independently of the source snapshot.
func (r *UploadRepository) PendingCommits(ctx context.Context) ([]domain.UploadRecord, error) {
	return r.readRecords(ctx, "SELECT snapshot FROM cloud_upload_records WHERE state NOT IN ('completed','skipped','stopped','discarded','control')")
}

// List restores all task records, including hidden deduplication receipts and queue control.
func (r *UploadRepository) List(ctx context.Context) ([]domain.UploadRecord, error) {
	return r.readRecords(ctx, "SELECT snapshot FROM cloud_upload_records ORDER BY record_key")
}
func (r *UploadRepository) readRecords(ctx context.Context, query string) ([]domain.UploadRecord, error) {
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.UploadRecord{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var record domain.UploadRecord
		if err = json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}
