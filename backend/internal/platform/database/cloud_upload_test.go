package database

import (
	"bytemuse/backend/internal/domain"
	"context"
	"path/filepath"
	"testing"
)

// TestUploadMigration covers fresh installs, previous-version upgrades and repeat runs preserving saved state.
func TestUploadMigration(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		ctx := context.Background()
		store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "upload.db")})
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if upgrade {
			if err = ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
				t.Fatal(err)
			}
			for _, m := range MigrationPlan(DialectSQLite) {
				if m.Version < 39 {
					if err = applyMigration(ctx, store.SQLDB(), DialectSQLite, m); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		if err = store.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		repo := NewUploadRepository(store.SQLDB(), DialectSQLite)
		record := domain.UploadRecord{Key: "source", State: "committing", Target: "movie (1).mkv"}
		if err = repo.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
		pending, pendingErr := repo.PendingCommits(ctx)
		if pendingErr != nil || len(pending) != 1 || pending[0].Key != record.Key {
			t.Fatalf("pending=%+v err=%v", pending, pendingErr)
		}
		if err = store.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		loaded, err := repo.Get(ctx, record.Key)
		if err != nil || loaded == nil || loaded.Target != record.Target || loaded.State != record.State {
			t.Fatalf("%+v %v", loaded, err)
		}
		var enabled string
		if err = store.SQLDB().QueryRowContext(ctx, "SELECT setting_value FROM app_settings WHERE setting_key='CLOUD_UPLOAD_ENABLE'").Scan(&enabled); err != nil || enabled != "false" {
			t.Fatalf("enabled=%s err=%v", enabled, err)
		}
	}
}
