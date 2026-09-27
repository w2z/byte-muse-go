package database

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/logging"
)

func TestPersistentLoggerSurvivesReopenAndClearDeletesRows(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "logs.db")
	open := func() Store {
		store, err := Open(ctx, Config{Dialect: DialectSQLite, DSN: databasePath})
		if err != nil {
			t.Fatalf("open database: %v", err)
		}
		if err := store.Migrate(ctx); err != nil {
			_ = store.Close()
			t.Fatalf("migrate database: %v", err)
		}
		return store
	}

	first := open()
	logger := logging.New(&bytes.Buffer{})
	logger.SetStore(NewLogRepository(first.SQLDB(), DialectSQLite))
	logger.Info(logging.CategorySystem, "包含原始属性", "password", "plain-password", "token", "plain-token")
	if err := first.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	second := open()
	defer second.Close()
	reopened := logging.New(&bytes.Buffer{})
	reopened.SetStore(NewLogRepository(second.SQLDB(), DialectSQLite))
	items, total, err := reopened.Search(ctx, logging.Query{Category: logging.CategorySystem, Keyword: "原始", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("search persisted logs: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one persisted log, total=%d items=%d", total, len(items))
	}
	if items[0].Attrs["password"] != "plain-password" || items[0].Attrs["token"] != "plain-token" {
		t.Fatalf("sensitive attributes must be persisted unchanged: %#v", items[0].Attrs)
	}

	deleted, err := reopened.Clear(ctx, logging.Query{})
	if err != nil {
		t.Fatalf("clear persisted logs: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected one deleted row, got %d", deleted)
	}
	_, total, err = reopened.Search(ctx, logging.Query{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("search after clear: %v", err)
	}
	if total != 0 {
		t.Fatalf("database logs must be empty after clear, got %d", total)
	}
}

func TestLogMigrationExistsForEveryDialect(t *testing.T) {
	for _, dialect := range []Dialect{DialectSQLite, DialectPostgres, DialectMySQL} {
		plan := MigrationPlan(dialect)
		latest := plan[len(plan)-1]
		if latest.Version != 11 || latest.Name != "add_catalog_query_indexes" {
			t.Fatalf("%s latest migration = %d/%s", dialect, latest.Version, latest.Name)
		}
	}
}

func TestLogRetentionSettingDefaultsToThirtyDays(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, DSN: filepath.Join(t.TempDir(), "settings.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := store.SQLDB().QueryRowContext(ctx, "SELECT setting_value FROM app_settings WHERE setting_key = 'LOG_RETENTION_DAYS'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "30" {
		t.Fatalf("default retention days = %q, want 30", value)
	}
}

func TestLogRepositoryClearsOnlyMatchingFiltersAndDeletesBefore(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, DSN: filepath.Join(t.TempDir(), "logs.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewLogRepository(store.SQLDB(), DialectSQLite)
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err := repo.Append(ctx, logging.Record{Time: old, Level: logging.LevelError, Category: logging.CategoryDownload, Message: "old download"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Append(ctx, logging.Record{Time: time.Now().UTC(), Level: logging.LevelInfo, Category: logging.CategoryDownload, Message: "new download"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Append(ctx, logging.Record{Time: time.Now().UTC(), Level: logging.LevelInfo, Category: logging.CategorySystem, Message: "new system"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Append(ctx, logging.Record{Time: old, Level: logging.LevelInfo, Category: logging.CategorySystem, Message: "old system"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.Clear(ctx, logging.Query{Category: logging.CategoryDownload})
	if err != nil || deleted != 2 {
		t.Fatalf("filtered clear deleted=%d err=%v", deleted, err)
	}
	_, total, err := repo.Search(ctx, logging.Query{Page: 1, PageSize: 20})
	if err != nil || total != 2 {
		t.Fatalf("expected two remaining logs, total=%d err=%v", total, err)
	}
	deleted, err = repo.DeleteBefore(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("expected one old log delete: %d %v", deleted, err)
	}
}

func TestLogRepositoryFiltersByTimeRange(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, DSN: filepath.Join(t.TempDir(), "logs.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewLogRepository(store.SQLDB(), DialectSQLite)
	first := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	third := second.Add(time.Hour)
	for _, item := range []logging.Record{
		{Time: first, Level: logging.LevelInfo, Category: logging.CategorySystem, Message: "before"},
		{Time: second, Level: logging.LevelInfo, Category: logging.CategorySystem, Message: "inside"},
		{Time: third, Level: logging.LevelInfo, Category: logging.CategorySystem, Message: "after"},
	} {
		if err := repo.Append(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := repo.Search(ctx, logging.Query{StartTime: &first, EndTime: &third, Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("inclusive time range total=%d items=%d, want 3", total, len(items))
	}
	start := first.Add(30 * time.Minute)
	end := second.Add(30 * time.Minute)
	items, total, err = repo.Search(ctx, logging.Query{StartTime: &start, EndTime: &end, Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].Message != "inside" {
		t.Fatalf("narrow time range = total %d items %#v, want inside only", total, items)
	}
	deleted, err := repo.Clear(ctx, logging.Query{StartTime: &start, EndTime: &end})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("time-filtered clear deleted=%d, want 1", deleted)
	}
}

func TestLogRepositoryAcceptsFiveHundredRowsPerPage(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, DSN: filepath.Join(t.TempDir(), "logs.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewLogRepository(store.SQLDB(), DialectSQLite)
	for index := 0; index < 250; index++ {
		if err := repo.Append(ctx, logging.Record{Time: time.Now().UTC(), Level: logging.LevelInfo, Category: logging.CategorySystem, Message: "pagination"}); err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := repo.Search(ctx, logging.Query{Page: 1, PageSize: 500})
	if err != nil {
		t.Fatal(err)
	}
	if total != 250 || len(items) != 250 {
		t.Fatalf("page_size=500 total=%d items=%d, want 250", total, len(items))
	}
}
