package database

import (
	"bytemuse/backend/internal/domain"
	"context"
	"fmt"
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

// TestCheckpointMigration covers fresh databases, version 39 upgrades and idempotent updates.
func TestCheckpointMigration(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprint(upgrade), func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "checkpoints.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if upgrade {
				if err := ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
					t.Fatal(err)
				}
				for _, migration := range MigrationPlan(DialectSQLite) {
					if migration.Version < 40 {
						if err := applyMigration(ctx, store.SQLDB(), DialectSQLite, migration); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			repo := NewScanTaskRepository(store.SQLDB(), DialectSQLite)
			if raw, err := repo.LoadCheckpoint(ctx, "task", "key"); err != nil || raw != nil {
				t.Fatalf("%s %v", raw, err)
			}
			for _, value := range []string{`false`, `true`} {
				if err := repo.SaveCheckpoint(ctx, "task", "key", []byte(value)); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if raw, err := repo.LoadCheckpoint(ctx, "task", "key"); err != nil || string(raw) != "true" {
				t.Fatalf("%s %v", raw, err)
			}
			task := domain.ScanTask{ID: "task", Kind: "strm", State: "running"}
			if err := repo.Save(ctx, task); err != nil {
				t.Fatal(err)
			}
			if err := repo.Interrupt(ctx); err != nil {
				t.Fatal(err)
			}
			restored, err := repo.Latest(ctx, "strm")
			if err != nil || !restored.CanRetry || restored.State != "interrupted" {
				t.Fatalf("%+v %v", restored, err)
			}
		})
	}
}
