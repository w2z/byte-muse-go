package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestImportLegacyActorsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "legacy.db")
	source, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.ExecContext(ctx, "CREATE TABLE actor (name TEXT PRIMARY KEY, photo TEXT, limit_date TEXT, create_time TEXT, update_time TEXT); INSERT INTO actor VALUES ('甲', 'p', '2026-01-01', '2025-01-01', NULL), ('乙', NULL, NULL, '2025-01-02', '2025-02-02')")
	if err != nil {
		t.Fatal(err)
	}
	_ = source.Close()

	targetPath := filepath.Join(t.TempDir(), "target.db")
	target, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: targetPath})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := ImportLegacyActors(ctx, target, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if first.Imported != 2 || first.Skipped != 0 {
		t.Fatalf("first result = %+v", first)
	}
	second, err := ImportLegacyActors(ctx, target, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if second.Imported != 2 {
		t.Fatalf("second result = %+v", second)
	}
	var count int
	if err := target.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM actors").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}
