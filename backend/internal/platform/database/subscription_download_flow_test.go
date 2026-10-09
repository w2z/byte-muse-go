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

// flowNotifier 捕获真实搜索、落库和提交链路最终生成的通知。
type flowNotifier struct {
	messages []application.NotificationMessage
}

func (notifier *flowNotifier) Notify(_ context.Context, _ application.NotificationEvent, message application.NotificationMessage) {
	notifier.messages = append(notifier.messages, message)
}

func (notifier *flowNotifier) ChannelEventEnabled(context.Context, string, application.NotificationEvent) bool {
	return true
}

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

func (f *flowPrivate) Download(context.Context, string) ([]byte, string, string, error) {
	f.downloads++
	return []byte("d4:infod4:name4:testee"), "1ade8a1a581f338e4fce4ce784da3f7d03f81f3a", "https://example.test/download/123.torrent?token=test-only", nil
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
	first, e := service.Enqueue(ctx, "sub1", ports.DownloadOriginUser)
	notifier := &flowNotifier{}
	service.SetNotifier(notifier)
	if e != nil {
		t.Fatal(e)
	}
	second, e := service.Enqueue(ctx, "sub1", ports.DownloadOriginUser)
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
	var reference, downloadURL string
	if e = s.SQLDB().QueryRowContext(ctx, "SELECT resource_uri,download_url FROM download_tasks WHERE id=?", tasks.Items[0].ID).Scan(&reference, &downloadURL); e != nil || reference != downloadURL || downloadURL == "" {
		t.Fatalf("BT magnet was not preserved: %v", e)
	}
	if e = service.Process(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if client.submitted != 1 || search.called != 1 {
		t.Fatalf("duplicate submission=%d search=%d", client.submitted, search.called)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("通知数量 = %d，期望 1", len(notifier.messages))
	}
	message := notifier.messages[0]
	if got := application.NotificationPlainText(message.Title, message.Text); got != "番号: SSIS-001\n状态: 开始下载\n站点: Nyaa BT\n来源: BT\n下载链接: magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567\n描述: film" {
		t.Fatalf("下载通知 = %q", got)
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
	if _, e = service.Enqueue(ctx, "sub1", ports.DownloadOriginUser); e != nil {
		t.Fatal(e)
	}
	if e = service.Process(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if client.uploads != 1 || client.submitted != 0 || source.downloads != 1 {
		t.Fatalf("uploads=%d magnets=%d downloads=%d", client.uploads, client.submitted, source.downloads)
	}
	var reference, downloadURL string
	if e = s.SQLDB().QueryRowContext(ctx, "SELECT resource_uri,download_url FROM download_tasks WHERE media_id=?", "m1").Scan(&reference, &downloadURL); e != nil {
		t.Fatal(e)
	}
	if reference != "mteam:123" || downloadURL != "https://example.test/download/123.torrent?token=test-only" {
		t.Fatal("PT resource reference or actual download URL was not preserved")
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
	if _, e = service.Enqueue(ctx, "sub1", ports.DownloadOriginUser); e != nil {
		t.Fatal(e)
	}
	if e = service.Process(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if client.submitted != 0 {
		t.Fatalf("default only_free filter ignored")
	}
	var tasks int
	if e = s.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM download_tasks").Scan(&tasks); e != nil || tasks != 0 {
		t.Fatalf("被默认过滤挡掉的资源不应建立下载任务，实际 %d 条 err=%v", tasks, e)
	}
}
