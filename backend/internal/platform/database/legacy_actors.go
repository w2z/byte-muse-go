package database

import (
	"context"
	"database/sql"
	"fmt"
)

// LegacyActorImportResult summarizes an idempotent import from the legacy SQLite actor table.
type LegacyActorImportResult struct {
	Imported int
	Skipped  int
}

// ImportLegacyActors reads the legacy SQLite database in read-only mode and upserts actors by name.
func ImportLegacyActors(ctx context.Context, target Store, legacyPath string) (LegacyActorImportResult, error) {
	source, err := sql.Open("sqlite", "file:"+legacyPath+"?mode=ro")
	if err != nil {
		return LegacyActorImportResult{}, fmt.Errorf("open legacy database: %w", err)
	}
	defer source.Close()
	if err := source.PingContext(ctx); err != nil {
		return LegacyActorImportResult{}, fmt.Errorf("ping legacy database: %w", err)
	}
	rows, err := source.QueryContext(ctx, "SELECT name, photo, limit_date, create_time, update_time FROM actor ORDER BY name")
	if err != nil {
		return LegacyActorImportResult{}, fmt.Errorf("read legacy actors: %w", err)
	}
	defer rows.Close()
	result := LegacyActorImportResult{}
	db := target.SQLDB()
	for rows.Next() {
		var name string
		var photo, limitDate, createdAt, updatedAt sql.NullString
		if err := rows.Scan(&name, &photo, &limitDate, &createdAt, &updatedAt); err != nil {
			return result, fmt.Errorf("scan legacy actor: %w", err)
		}
		if name == "" {
			result.Skipped++
			continue
		}
		_, err := db.ExecContext(ctx, "INSERT INTO actors (name, photo, limit_date, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(name) DO UPDATE SET photo=excluded.photo, limit_date=excluded.limit_date, created_at=excluded.created_at, updated_at=excluded.updated_at", name, nullableString(photo), nullableString(limitDate), nullableString(createdAt), nullableString(updatedAt))
		if err != nil {
			return result, fmt.Errorf("upsert legacy actor %q: %w", name, err)
		}
		result.Imported++
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate legacy actors: %w", err)
	}
	return result, nil
}

func nullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
