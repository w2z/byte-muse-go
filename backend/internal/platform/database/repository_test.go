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

// TestMediaDetailsReadsSavedMetadata 验证详情只读已有资料，缺失元数据与未知分类不伪造字段。
func TestMediaDetailsReadsSavedMetadata(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "media-detail.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range []string{"with", "empty"} {
		if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media(id,code,title,created_at,updated_at) VALUES(?,?,?,?,?)", id, id, "原标题", stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO legacy_media_metadata(media_id,code,casts,genres,producer,publisher,series,legacy_status,legacy_mode) VALUES(?,?,?,?,?,?,?,?,?)", "with", "with", "演员甲, 演员乙", "标签一,标签二", "制作商", "发行商", "系列一", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind                    string
		known, censored, mosaic bool
	}{
		{"censored", true, true, true}, {"uncensored", true, false, false}, {"uncensored_cracked", true, true, false}, {"leaked", false, false, false},
	} {
		if _, err = store.SQLDB().ExecContext(ctx, "UPDATE media SET video_type=? WHERE id='with'", tc.kind); err != nil {
			t.Fatal(err)
		}
		item, e := store.Media().Get(ctx, "with")
		if e != nil {
			t.Fatal(e)
		}
		d := item.Details
		if d == nil || len(d.Actors) != 2 || d.Actors[1] != "演员乙" || len(d.Tags) != 2 || d.Producer == nil || *d.Producer != "制作商" || d.Publisher == nil || *d.Publisher != "发行商" || d.Series == nil || *d.Series != "系列一" {
			t.Fatalf("details=%+v", d)
		}
		if tc.known {
			if d.Censored == nil || *d.Censored != tc.censored || d.Mosaic == nil || *d.Mosaic != tc.mosaic {
				t.Fatalf("classification=%+v", d)
			}
		} else if d.Censored != nil || d.Mosaic != nil {
			t.Fatal("unknown classification was guessed")
		}
		if d.Rating != nil || d.WantCount != nil || d.TranslationEngine != nil || d.Resolution != nil || d.ReleaseCode != nil || d.Plot != nil {
			t.Fatal("missing metadata was invented")
		}
	}
	item, err := store.Media().Get(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if item.Details == nil || item.Details.Actors == nil || len(item.Details.Actors) != 0 || item.Details.Producer != nil {
		t.Fatalf("empty details=%+v", item.Details)
	}
	page, err := store.Media().List(ctx, ports.MediaListQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].Details != nil {
		t.Fatal("list unexpectedly loaded detail")
	}
}
