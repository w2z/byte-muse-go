package database

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"bytemuse/backend/internal/ports"
)

// TestStrmFilesMigrationAndRecovery 验证空库、旧库升级、重复迁移、账号隔离与重启恢复，不回填历史文件。
func TestStrmFilesMigrationAndRecovery(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprint(upgrade), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "strm.db")
			store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: path})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			if upgrade {
				if err := ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
					t.Fatal(err)
				}
				for _, migration := range MigrationPlan(DialectSQLite) {
					if migration.Version < 42 {
						if err := applyMigration(ctx, store.SQLDB(), DialectSQLite, migration); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			repo := NewStrmFileRepository(store.SQLDB(), DialectSQLite)
			items, err := repo.List(ctx, "account1")
			if err != nil || len(items) != 0 {
				t.Fatalf("historical backfill: %v %v", items, err)
			}
			item := ports.StrmFileRecord{Key: "path-key", Scope: "account1", FileID: "42", ParentID: "10", Ancestors: `["10"]`, RelativePath: "movies/movie.strm", SHA256: "digest"}
			for count := 0; count < 2; count++ {
				if err := repo.Save(ctx, item); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: path})
			if err != nil {
				t.Fatal(err)
			}
			repo = NewStrmFileRepository(store.SQLDB(), DialectSQLite)
			items, err = repo.List(ctx, "account1")
			if err != nil || len(items) != 1 || items[0] != item {
				t.Fatalf("lost record: %+v %v", items, err)
			}
			items, err = repo.List(ctx, "account2")
			if err != nil || len(items) != 0 {
				t.Fatalf("account isolation: %+v %v", items, err)
			}
			item.Scope = "account2"
			if err := repo.Save(ctx, item); err != nil {
				t.Fatal(err)
			}
			items, err = repo.List(ctx, "account1")
			if err != nil || len(items) != 0 {
				t.Fatalf("stale owner: %+v %v", items, err)
			}
			for count := 0; count < 2; count++ {
				if err := repo.Delete(ctx, item.Key); err != nil {
					t.Fatal(err)
				}
			}
			items, err = repo.List(ctx, "account2")
			if err != nil || len(items) != 0 {
				t.Fatalf("delete: %+v %v", items, err)
			}
		})
	}
}
