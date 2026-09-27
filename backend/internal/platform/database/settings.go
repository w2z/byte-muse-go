package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"bytemuse/backend/internal/ports"
)

// SettingsRepository stores application settings in the migrated database.
type SettingsRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewSettingsRepository binds settings persistence to the shared database.
func NewSettingsRepository(db *sql.DB, dialect Dialect) *SettingsRepository {
	return &SettingsRepository{db: db, dialect: dialect}
}

// List returns all persisted settings without interpreting or decrypting values.
func (r *SettingsRepository) List(ctx context.Context) ([]ports.StoredSetting, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT setting_key, setting_value, is_secret FROM app_settings ORDER BY setting_key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ports.StoredSetting, 0)
	for rows.Next() {
		var item ports.StoredSetting
		if err := rows.Scan(&item.Key, &item.Value, &item.IsSecret); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Upsert saves a batch atomically so a partial configuration is never exposed.
func (r *SettingsRepository) Upsert(ctx context.Context, items []ports.StoredSetting) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range items {
		columns := []string{"setting_key", "setting_value", "is_secret", "updated_at"}
		query := fmt.Sprintf("INSERT INTO app_settings (%s) VALUES (%s)%s", "setting_key, setting_value, is_secret, updated_at", placeholders(r.dialect, len(columns), 1), upsertClause(r.dialect, "setting_key", columns[1:]))
		if _, err := tx.ExecContext(ctx, query, item.Key, item.Value, item.IsSecret, encodeTimeNow(r.dialect)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func encodeTimeNow(dialect Dialect) any {
	return encodeTime(time.Now().UTC(), dialect)
}
