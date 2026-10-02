package database

import (
	"bytemuse/backend/internal/domain"
	"context"
	"path/filepath"
	"testing"
)

func TestScanTasksPersistAndRecover(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// 从升级前版本迁移，重复迁移不改变任务或业务数据。
	if err = ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	for _, m := range MigrationPlan(DialectSQLite) {
		if m.Version < 36 {
			if err = applyMigration(ctx, store.SQLDB(), DialectSQLite, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	var before int
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM media").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var after int
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM media").Scan(&after); err != nil || before != after {
		t.Fatalf("media changed: %d -> %d, %v", before, after, err)
	}
	repo := NewScanTaskRepository(store.SQLDB(), DialectSQLite)
	task := domain.ScanTask{ID: "test", Kind: "library", State: "running", Progress: domain.ScanProgress{Phase: "discovering", Processed: 2, Total: 8, Percent: 25}}
	if err = repo.Save(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo = NewScanTaskRepository(store.SQLDB(), DialectSQLite)
	if err = repo.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Latest(ctx, "library")
	if err != nil || got.State != "interrupted" || got.Progress.Total != 8 || got.Progress.Processed != 2 {
		t.Fatalf("restored: %+v %v", got, err)
	}
	if err = repo.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	other, err := repo.Latest(ctx, "strm")
	if err != nil || other != nil {
		t.Fatalf("independent kind: %+v %v", other, err)
	}
}
