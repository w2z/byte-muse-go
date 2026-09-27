package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openCatalogQueryTestStore(t *testing.T) Store {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "catalog.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func insertCatalogTestMedia(t *testing.T, store Store, id, code, title string) {
	t.Helper()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.SQLDB().ExecContext(context.Background(), `INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at) VALUES (?, ?, ?, 'none', 'unknown', ?, ?)`, id, code, title, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogQuerySearchMatchesCodeAndTitle(t *testing.T) {
	ctx := context.Background()
	store := openCatalogQueryTestStore(t)
	defer store.Close()
	insertCatalogTestMedia(t, store, "media-code", "ABC-001", "普通标题")
	insertCatalogTestMedia(t, store, "media-title", "XYZ-002", "目标影片")
	repository := NewCatalogQueryRepository(store.SQLDB(), DialectSQLite)

	byCode, err := repository.Search(ctx, "ABC-001", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if byCode.Total != 1 || len(byCode.Items) != 1 || byCode.Items[0].Code != "ABC-001" {
		t.Fatalf("code search = total %d items %#v", byCode.Total, byCode.Items)
	}
	byTitle, err := repository.Search(ctx, "目标影片", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if byTitle.Total != 1 || len(byTitle.Items) != 1 || byTitle.Items[0].Title != "目标影片" {
		t.Fatalf("title search = total %d items %#v", byTitle.Total, byTitle.Items)
	}
	if _, err := store.SQLDB().ExecContext(ctx, `UPDATE media SET translated_title = ? WHERE id = ?`, "译名目标", "media-code"); err != nil {
		t.Fatal(err)
	}
	byTranslatedTitle, err := repository.Search(ctx, "译名目标", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if byTranslatedTitle.Total != 1 || len(byTranslatedTitle.Items) != 1 || byTranslatedTitle.Items[0].Code != "ABC-001" {
		t.Fatalf("translated title search = total %d items %#v", byTranslatedTitle.Total, byTranslatedTitle.Items)
	}
}

func TestCatalogQueryBatchProjectionPreservesRequestedOrder(t *testing.T) {
	ctx := context.Background()
	store := openCatalogQueryTestStore(t)
	defer store.Close()
	insertCatalogTestMedia(t, store, "media-first", "FIRST-001", "第一部")
	insertCatalogTestMedia(t, store, "media-second", "SECOND-002", "第二部")
	repository := NewCatalogQueryRepository(store.SQLDB(), DialectSQLite)
	items, err := repository.loadMediaProjectionBatch(ctx, []string{"media-second", "media-first"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "media-second" || items[1].ID != "media-first" {
		t.Fatalf("batch order = %#v", items)
	}
}

func TestMigrationAddsCatalogQueryIndexes(t *testing.T) {
	ctx := context.Background()
	store := openCatalogQueryTestStore(t)
	defer store.Close()
	rows, err := store.SQLDB().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name IN ('idx_media_release_date_code', 'idx_media_release_subscription', 'idx_legacy_metadata_status_media') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("catalog indexes = %v", names)
	}
}
