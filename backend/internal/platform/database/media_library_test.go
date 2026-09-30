package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// TestMediaLibraryMarkIsIdempotentAndPreservesExistingFields 覆盖扫描入库的登记语义：
// 按 UPPER(code) 匹配、命中只刷新媒体库状态、未命中新建，重复登记不重复建行。
func TestMediaLibraryMarkIsIdempotentAndPreservesExistingFields(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "media-library.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	// 已存在的人工维护影片：番号大小写与扫描结果不同，标题、订阅状态与分类都不应被覆盖。
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at, video_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", "existing", "abc-001", "原标题", "active", "absent", stamp, stamp, "censored"); err != nil {
		t.Fatal(err)
	}

	created, err := store.MediaLibrary().MarkLibraryPresent(ctx, []ports.LibraryMediaItem{
		{Code: "ABC-001", Title: "扫描标题", VideoType: "uncensored"},
		{Code: "XYZ-002", Title: "新片", VideoType: "uncensored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("新建行数 %d，期望 1", created)
	}

	var title, subscription, library, videoType string
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT title, subscription_status, library_status, video_type FROM media WHERE id='existing'").Scan(&title, &subscription, &library, &videoType); err != nil {
		t.Fatal(err)
	}
	if title != "原标题" || subscription != "active" || library != string(domain.LibraryStatusPresent) || videoType != "censored" {
		t.Fatalf("已有行被覆盖：title=%q subscription=%q library=%q video_type=%q", title, subscription, library, videoType)
	}

	var newTitle, newSubscription, newLibrary string
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT title, subscription_status, library_status FROM media WHERE code='XYZ-002'").Scan(&newTitle, &newSubscription, &newLibrary); err != nil {
		t.Fatal(err)
	}
	if newTitle != "新片" || newSubscription != string(domain.SubscriptionStatusNone) || newLibrary != string(domain.LibraryStatusPresent) {
		t.Fatalf("新建行不符：title=%q subscription=%q library=%q", newTitle, newSubscription, newLibrary)
	}

	// 重复扫描：不应新建行，也不改变状态。
	created, err = store.MediaLibrary().MarkLibraryPresent(ctx, []ports.LibraryMediaItem{{Code: "ABC-001", Title: "再次扫描"}})
	if err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("重复登记新建行数 %d，期望 0", created)
	}
	var total int
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM media").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("media 行数 %d，期望 2", total)
	}
}

// TestMediaLibraryMarkFallsBackToCodeAsTitle 覆盖标题缺失时用番号兜底，且超长标题按码点截断不报错。
func TestMediaLibraryMarkFallsBackToCodeAsTitle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "media-library-title.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	longTitle := ""
	for index := 0; index < mediaTitleMaxRunes+50; index++ {
		longTitle += "字"
	}
	if _, err = store.MediaLibrary().MarkLibraryPresent(ctx, []ports.LibraryMediaItem{
		{Code: "ABC-003", Title: "   "},
		{Code: "ABC-004", Title: longTitle},
	}); err != nil {
		t.Fatal(err)
	}

	var fallback string
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT title FROM media WHERE code='ABC-003'").Scan(&fallback); err != nil {
		t.Fatal(err)
	}
	if fallback != "ABC-003" {
		t.Fatalf("标题缺失时应用番号兜底，实际 %q", fallback)
	}
	var stored string
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT title FROM media WHERE code='ABC-004'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if runes := []rune(stored); len(runes) != mediaTitleMaxRunes {
		t.Fatalf("超长标题应按码点截断到 %d，实际 %d", mediaTitleMaxRunes, len(runes))
	}
}
