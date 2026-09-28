package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

func TestMediaRepositoryListFiltersCatalogState(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "media-filter.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, item := range []struct{ id, code, title, subscription, library string }{
		{"one", "ABC-001", "第一部", "active", "present"},
		{"two", "XYZ-002", "第二部", "none", "absent"},
	} {
		if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)", item.id, item.code, item.title, item.subscription, item.library, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO download_tasks (id, media_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", "download-one", "one", domain.DownloadStatusCompleted, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	repository := store.Media()
	result, err := repository.List(ctx, ports.MediaListQuery{Limit: 20, Search: "ABC", SubscriptionStatus: "active", DownloadStatus: string(domain.DownloadStatusCompleted), LibraryStatus: string(domain.LibraryStatusPresent)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].Code != "ABC-001" {
		t.Fatalf("filtered media = total %d items %#v", result.Total, result.Items)
	}
}
