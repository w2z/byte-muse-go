package database

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/torrentsearch"
	"bytemuse/backend/internal/ports"
)

type flowSearcher struct{ called int }

func (f *flowSearcher) Search(_ context.Context, code string) ([]torrentsearch.Resource, error) {
	f.called++
	return []torrentsearch.Resource{{Kind: "bt", Site: "Nyaa BT", Title: code + " 中文字幕", URI: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", InfoHash: "0123456789abcdef0123456789abcdef01234567", Seeders: 12, Chinese: true}}, nil
}

type flowDownloader struct {
	existing  bool
	submitted int
}

type flowPTSearcher struct{}

func (flowPTSearcher) Search(context.Context, string) ([]torrentsearch.Resource, error) {
	return []torrentsearch.Resource{{Kind: "pt", Site: "馒头", Title: "SSIS-001", URI: "mteam:123", Seeders: 20}}, nil
}

type flowPrivate struct{ downloads int }

func (f *flowPrivate) Download(context.Context, string) ([]byte, string, error) {
	f.downloads++
	return []byte("d4:infod4:name4:testee"), "1ade8a1a581f338e4fce4ce784da3f7d03f81f3a", nil
}

type flowPTDownloader struct {
	flowDownloader
	uploads int
}

func (f *flowPTDownloader) SubmitTorrent(_ context.Context, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty file")
	}
	f.uploads++
	f.existing = true
	return nil
}

func (f *flowDownloader) HasHash(context.Context, string) (bool, error) { return f.existing, nil }
func (f *flowDownloader) Submit(context.Context, string) error {
	f.submitted++
	f.existing = true
	return nil
}

func TestSubscriptionDownloadFlowSubmitsOnceAndExposesSource(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "flow.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "SSIS-001", "film", "active", "absent", stamp, stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO subscriptions (id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?)", "sub1", "m1", "active", "strict", "{\"only_chinese\":true,\"min_size\":null,\"max_size\":null}", "key", "hash", stamp, stamp, 1)
	if e != nil {
		t.Fatal(e)
	}
	search := &flowSearcher{}
	client := &flowDownloader{}
	taskRepo := NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite)
	service := application.NewSubscriptionDownloadService(taskRepo, search, map[string]application.MagnetDownloader{"qbittorrent": client}, func(context.Context) (map[string]string, error) {
		return map[string]string{"BT_DEFAULT_DOWNLOADER": "qbittorrent", "DEFAULT_SORT": "seeders"}, nil
	})
	first, e := service.Enqueue(ctx, "sub1")
	if e != nil {
		t.Fatal(e)
	}
	second, e := service.Enqueue(ctx, "sub1")
	if e != nil || second != first {
		t.Fatalf("duplicate task %s %s %v", first, second, e)
	}
	if e = service.Process(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if client.submitted != 1 || search.called != 1 {
		t.Fatalf("search=%d submit=%d", search.called, client.submitted)
	}
	tasks, e := s.Downloads().List(ctx, ports.DownloadListQuery{Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	if len(tasks.Items) != 1 || tasks.Items[0].Status != "submitted" || tasks.Items[0].SourceSite == nil || *tasks.Items[0].SourceSite != "Nyaa BT" {
		t.Fatalf("task=%#v", tasks.Items)
	}
	if e = service.Process(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if client.submitted != 1 {
		t.Fatalf("duplicate submission=%d", client.submitted)
	}
}

func TestPrivateTorrentFlowUploadsFileOnlyOnce(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "pt.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "SSIS-001", "film", "active", "absent", stamp, stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO subscriptions (id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?)", "sub1", "m1", "active", "strict", "{}", "key", "hash", stamp, stamp, 1)
	if e != nil {
		t.Fatal(e)
	}
	client := &flowPTDownloader{}
	source := &flowPrivate{}
	service := application.NewSubscriptionDownloadService(NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite), flowPTSearcher{}, map[string]application.MagnetDownloader{"qbittorrent": client}, func(context.Context) (map[string]string, error) {
		return map[string]string{"PT_DEFAULT_DOWNLOADER": "qbittorrent"}, nil
	})
	service.SetPrivateTorrentSource(source)
	if _, e = service.Enqueue(ctx, "sub1"); e != nil {
		t.Fatal(e)
	}
	if e = service.Process(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if client.uploads != 1 || client.submitted != 0 || source.downloads != 1 {
		t.Fatalf("uploads=%d magnets=%d downloads=%d", client.uploads, client.submitted, source.downloads)
	}
}

func TestDefaultFilterAppliesWhenSubscriptionFilterEmpty(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "filter.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "SSIS-001", "film", "active", "absent", stamp, stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO subscriptions (id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?)", "sub1", "m1", "active", "strict", "{}", "key", "hash", stamp, stamp, 1)
	if e != nil {
		t.Fatal(e)
	}
	client := &flowDownloader{}
	search := &flowSearcher{}
	service := application.NewSubscriptionDownloadService(NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite), search, map[string]application.MagnetDownloader{"qbittorrent": client}, func(context.Context) (map[string]string, error) {
		return map[string]string{"DEFAULT_FILTER": "{\"only_free\":true}", "BT_DEFAULT_DOWNLOADER": "qbittorrent"}, nil
	})
	if _, e = service.Enqueue(ctx, "sub1"); e != nil {
		t.Fatal(e)
	}
	if e = service.Process(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if client.submitted != 0 {
		t.Fatalf("default only_free filter ignored")
	}
}
