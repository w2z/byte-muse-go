package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestBackfillVideoTypes 验证按证据回填、保持 NULL、幂等且不覆盖已有分类。
func TestBackfillVideoTypes(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "video-type.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	insert := func(id, code, title, genres, existing string) {
		var videoType any
		if existing != "" {
			videoType = existing
		}
		if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at,video_type) VALUES (?,?,?,?,?,?,?,?)", id, code, title, "none", "unknown", now, now, videoType); err != nil {
			t.Fatal(err)
		}
		if genres != "" {
			if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO legacy_media_metadata (media_id, code, genres, casts, legacy_status, legacy_mode) VALUES (?,?,?,?,?,?)", id, code, genres, "", "UN_SUBSCRIBE", "STRICT"); err != nil {
				t.Fatal(err)
			}
		}
	}
	insert("m1", "SSIS-001", "普通有码作品", "巨乳,单体作品", "")
	insert("m2", "ABC-002", "某破解作品", "无码破解", "")
	insert("m3", "n1234", "东京热作品", "", "")
	insert("m4", "", "", "", "")
	insert("m5", "SSIS-005", "已分类作品", "", "leaked")

	result, err := BackfillVideoTypes(ctx, store, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 4 || result.Classified != 3 || result.Unclassified != 1 {
		t.Fatalf("unexpected result %+v", result)
	}
	for id, want := range map[string]string{"m1": "censored", "m2": "uncensored_cracked", "m3": "uncensored", "m5": "leaked"} {
		var got *string
		if err := store.SQLDB().QueryRow("SELECT video_type FROM media WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got == nil || *got != want {
			t.Fatalf("%s video_type = %v want %s", id, got, want)
		}
	}
	var unclassified *string
	if err := store.SQLDB().QueryRow("SELECT video_type FROM media WHERE id = 'm4'").Scan(&unclassified); err != nil {
		t.Fatal(err)
	}
	if unclassified != nil {
		t.Fatalf("m4 must stay NULL, got %q", *unclassified)
	}
	second, err := BackfillVideoTypes(ctx, store, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned != 1 || second.Classified != 0 {
		t.Fatalf("backfill must be idempotent, got %+v", second)
	}
}
