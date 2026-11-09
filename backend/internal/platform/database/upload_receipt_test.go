package database

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"bytemuse/backend/internal/ports"
)

func TestUploadReceiptMigrationPreservesSnapshotsAndCompletion(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprint(upgrade), func(t *testing.T) {
			ctx := context.Background()
			name := filepath.Join(t.TempDir(), "receipts.db")
			db, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: name})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			if upgrade {
				if err = ensureMigrationTable(ctx, db.SQLDB(), DialectSQLite); err != nil {
					t.Fatal(err)
				}
				for _, m := range MigrationPlan(DialectSQLite) {
					if m.Version < 48 {
						if err = applyMigration(ctx, db.SQLDB(), DialectSQLite, m); err != nil {
							t.Fatal(err)
						}
					}
				}
				if _, err = db.SQLDB().Exec("INSERT INTO cloud_upload_records VALUES('history','completed','{\"unknown_future_field\":true}')"); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err = db.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			r := NewUploadReceiptRepository(db.SQLDB(), DialectSQLite)
			v := ports.UploadReceipt{Key: "task", Account: "a", FileID: "42", ParentID: "10", Info: `{"PickCode":"pc42"}`, LocalRoot: "/input", Relative: "movie.mkv", SHA1: "abc", Size: 3, Modified: 10, VerifiedAt: 20}
			if err = r.Insert(ctx, v); err != nil {
				t.Fatal(err)
			}
			if err = r.Complete(ctx, v.Key); err != nil {
				t.Fatal(err)
			}
			v.Info = "replacement"
			if err = r.Insert(ctx, v); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: name})
			if err != nil {
				t.Fatal(err)
			}
			r = NewUploadReceiptRepository(db.SQLDB(), DialectSQLite)
			got, err := r.Find(ctx, "a", "42")
			if err != nil || got == nil || got.State != "completed" || got.Info != `{"PickCode":"pc42"}` {
				t.Fatalf("receipt=%+v err=%v", got, err)
			}
			if got, err = r.Find(ctx, "other", "42"); err != nil || got != nil {
				t.Fatalf("account leak %+v %v", got, err)
			}
			if upgrade {
				var raw string
				if err = db.SQLDB().QueryRow("SELECT snapshot FROM cloud_upload_records WHERE record_key='history'").Scan(&raw); err != nil || raw != `{"unknown_future_field":true}` {
					t.Fatalf("history changed: %s %v", raw, err)
				}
			}
		})
	}
}
