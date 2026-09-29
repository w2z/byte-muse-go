package database

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTranslationStore(t *testing.T) Store {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "translation.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}

// TestResetInvalidTranslations 验证只清除无效译文，正常译文与空译文不受影响。
func TestResetInvalidTranslations(t *testing.T) {
	ctx := context.Background()
	store := newTranslationStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	insert := func(id, title string, translated any) {
		if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,translated_title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", id, id, title, translated, "none", "unknown", now, now); err != nil {
			t.Fatal(err)
		}
	}
	insert("ok", "初撮り", "初次拍摄")
	insert("bad", "初撮り", "根据法律法规，无法提供翻译服务。")
	insert("empty", "初撮り", nil)

	result, err := ResetInvalidTranslations(ctx, store, DialectSQLite, func(title, translated string) bool {
		return !strings.Contains(translated, "无法提供")
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 2 || result.Reset != 1 {
		t.Fatalf("unexpected result %+v", result)
	}
	var translated *string
	if err := store.SQLDB().QueryRow("SELECT translated_title FROM media WHERE id = 'bad'").Scan(&translated); err != nil {
		t.Fatal(err)
	}
	if translated != nil {
		t.Fatalf("invalid translation must be reset, got %q", *translated)
	}
	if err := store.SQLDB().QueryRow("SELECT translated_title FROM media WHERE id = 'ok'").Scan(&translated); err != nil {
		t.Fatal(err)
	}
	if translated == nil || *translated != "初次拍摄" {
		t.Fatalf("valid translation must be preserved, got %v", translated)
	}
}

// TestBackfillTranslations 验证补翻译只处理缺失译文，单条失败不阻断其余影片。
func TestBackfillTranslations(t *testing.T) {
	ctx := context.Background()
	store := newTranslationStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", id, id, "标题-"+id, "none", "unknown", now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SQLDB().ExecContext(ctx, "UPDATE media SET translated_title = '已有译文' WHERE id = 'c'"); err != nil {
		t.Fatal(err)
	}
	result, err := BackfillTranslations(ctx, store, DialectSQLite, 0, func(_ context.Context, title string) (string, error) {
		if title == "标题-b" {
			return "", errors.New("provider failed")
		}
		return "译文-" + title, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempted != 2 || result.Translated != 1 || result.Failed != 1 {
		t.Fatalf("unexpected result %+v", result)
	}
	var value *string
	if err := store.SQLDB().QueryRow("SELECT translated_title FROM media WHERE id = 'a'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value == nil || *value != "译文-标题-a" {
		t.Fatalf("a translated_title = %v", value)
	}
	if err := store.SQLDB().QueryRow("SELECT translated_title FROM media WHERE id = 'b'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != nil {
		t.Fatalf("failed translation must stay NULL, got %q", *value)
	}
	if err := store.SQLDB().QueryRow("SELECT translated_title FROM media WHERE id = 'c'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value == nil || *value != "已有译文" {
		t.Fatalf("existing translation must be preserved, got %v", value)
	}
}

// TestIsWriteConflict 只把可重试的写锁竞争判为可重试，其他错误保持原样返回。
func TestIsWriteConflict(t *testing.T) {
	cases := []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{errors.New("database is locked (5) (SQLITE_BUSY)"), true},
		{errors.New("database table is locked"), true},
		{errors.New("Error 1213: Deadlock found when trying to get lock"), true},
		{errors.New("constraint failed: UNIQUE constraint failed: media.id"), false},
		{errors.New("no such table: media"), false},
	}
	for _, tc := range cases {
		if got := isWriteConflict(tc.err); got != tc.expected {
			t.Fatalf("isWriteConflict(%v) = %v, want %v", tc.err, got, tc.expected)
		}
	}
}
