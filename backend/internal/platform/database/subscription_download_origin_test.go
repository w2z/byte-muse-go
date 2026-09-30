package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/torrentsearch"
	"bytemuse/backend/internal/ports"
)

// originNotifier 记录下载服务推送的通知事件，用于断言失败通知是否按发起方分流。
type originNotifier struct{ events []string }

func (n *originNotifier) Notify(_ context.Context, event application.NotificationEvent, _ application.NotificationMessage) {
	n.events = append(n.events, string(event))
}

func (n *originNotifier) ChannelEventEnabled(context.Context, string, application.NotificationEvent) bool {
	return true
}

// emptySearcher 返回空候选，稳定命中「暂未找到符合条件的资源」这条搜索分支。
type emptySearcher struct{}

func (emptySearcher) Search(context.Context, string) ([]torrentsearch.Resource, error) {
	return nil, nil
}

// newOriginFixture 建立一条影片与有效订阅，返回可驱动搜索失败分支的下载服务与仓储。
func newOriginFixture(t *testing.T) (Store, *application.SubscriptionDownloadService, *SubscriptionDownloadRepository, *originNotifier) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "origin.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "TGHH-091", "玻璃跳虫", "active", "absent", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO subscriptions (id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?)", "sub1", "m1", "active", "strict", "{}", "key", "hash", stamp, stamp, 1); err != nil {
		t.Fatal(err)
	}
	repository := NewSubscriptionDownloadRepository(store.SQLDB(), DialectSQLite)
	service := application.NewSubscriptionDownloadService(repository, emptySearcher{}, map[string]application.MagnetDownloader{}, func(context.Context) (map[string]string, error) {
		return map[string]string{"BT_DEFAULT_DOWNLOADER": "qbittorrent"}, nil
	})
	notifier := &originNotifier{}
	service.SetNotifier(notifier)
	return store, service, repository, notifier
}

// downloadTaskCount 统计订阅关联的下载任务；搜索没找到资源必须为 0，下载页不能被失败记录污染。
func downloadTaskCount(t *testing.T, store Store, ctx context.Context) int {
	t.Helper()
	var count int
	if err := store.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM download_tasks WHERE subscription_id='sub1'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// scanCount 统计订阅残留的待执行搜索；一次搜索结束后必须清空。
func scanCount(t *testing.T, store Store, ctx context.Context) int {
	t.Helper()
	var count int
	if err := store.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM subscription_scans WHERE subscription_id='sub1'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// scanOrigin 读取待执行搜索的发起方，校验用户请求不会被定时任务降级。
func scanOrigin(t *testing.T, store Store, ctx context.Context) ports.DownloadOrigin {
	t.Helper()
	var origin string
	if err := store.SQLDB().QueryRowContext(ctx, "SELECT origin FROM subscription_scans WHERE subscription_id='sub1'").Scan(&origin); err != nil {
		t.Fatal(err)
	}
	return ports.DownloadOrigin(origin)
}

// TestScheduledSearchFailureStaysSilent 验证定时任务没找到资源时不推送、不建立下载任务，避免批量扫描刷屏。
func TestScheduledSearchFailureStaysSilent(t *testing.T) {
	store, service, _, notifier := newOriginFixture(t)
	ctx := context.Background()
	if _, err := service.Enqueue(ctx, "sub1", ports.DownloadOriginSchedule); err != nil {
		t.Fatal(err)
	}
	if err := service.Process(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 0 {
		t.Fatalf("定时任务搜索失败不应推送通知，实际 %v", notifier.events)
	}
	if n := downloadTaskCount(t, store, ctx); n != 0 {
		t.Fatalf("搜索没找到资源不应建立下载任务，实际 %d 条", n)
	}
	if n := scanCount(t, store, ctx); n != 0 {
		t.Fatalf("搜索结束后不应残留队列项，实际 %d 条", n)
	}
}

// TestUserSearchFailureNotifies 验证聊天渠道发起的搜索没找到资源时仍推送失败通知，且不建立下载任务。
func TestUserSearchFailureNotifies(t *testing.T) {
	store, service, _, notifier := newOriginFixture(t)
	ctx := context.Background()
	if _, err := service.Enqueue(ctx, "sub1", ports.DownloadOriginUser); err != nil {
		t.Fatal(err)
	}
	if err := service.Process(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 || notifier.events[0] != string(application.NotificationDownloadFailed) {
		t.Fatalf("用户发起搜索失败应推送一次失败通知，实际 %v", notifier.events)
	}
	if n := downloadTaskCount(t, store, ctx); n != 0 {
		t.Fatalf("搜索没找到资源不应建立下载任务，实际 %d 条", n)
	}
	if n := scanCount(t, store, ctx); n != 0 {
		t.Fatalf("搜索结束后不应残留队列项，实际 %d 条", n)
	}
}

// TestUserRequestUpgradesQueuedScheduledTask 覆盖关键场景：定时任务已排队时用户再发番号，
// 来源必须升级为 user，否则用户显式请求会被定时任务登记的搜索吸收而永远收不到失败通知。
func TestUserRequestUpgradesQueuedScheduledTask(t *testing.T) {
	store, service, _, notifier := newOriginFixture(t)
	ctx := context.Background()
	if _, err := service.Enqueue(ctx, "sub1", ports.DownloadOriginSchedule); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enqueue(ctx, "sub1", ports.DownloadOriginUser); err != nil {
		t.Fatal(err)
	}
	if origin := scanOrigin(t, store, ctx); origin != ports.DownloadOriginUser {
		t.Fatalf("用户请求命中已排队搜索后 origin = %q，期望 user", origin)
	}
	// 定时任务随后再次登记同一订阅，不得把来源降级回 schedule。
	if _, err := service.Enqueue(ctx, "sub1", ports.DownloadOriginSchedule); err != nil {
		t.Fatal(err)
	}
	if origin := scanOrigin(t, store, ctx); origin != ports.DownloadOriginUser {
		t.Fatalf("定时任务重复登记后 origin = %q，期望保持 user", origin)
	}
	if err := service.Process(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 || notifier.events[0] != string(application.NotificationDownloadFailed) {
		t.Fatalf("升级为 user 的搜索失败应推送通知，实际 %v", notifier.events)
	}
	if n := downloadTaskCount(t, store, ctx); n != 0 {
		t.Fatalf("搜索没找到资源不应建立下载任务，实际 %d 条", n)
	}
}

// TestBatchRunActiveStaysSilent 验证批量扫描（定时任务与聊天里的批量命令）统一按 schedule 处理：
// 一条订阅的失败就推送一次通知，批量执行必须保持静默，否则会变成通知风暴。
func TestBatchRunActiveStaysSilent(t *testing.T) {
	store, service, _, notifier := newOriginFixture(t)
	ctx := context.Background()
	if _, err := service.RunActiveScans(ctx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 0 {
		t.Fatalf("批量扫描搜索失败不应推送通知，实际 %v", notifier.events)
	}
	if n := downloadTaskCount(t, store, ctx); n != 0 {
		t.Fatalf("批量扫描没找到资源不应建立下载任务，实际 %d 条", n)
	}
	if n := scanCount(t, store, ctx); n != 0 {
		t.Fatalf("批量扫描结束后不应残留队列项，实际 %d 条", n)
	}
}

// TestBatchRunActivePreservesUserOrigin 验证批量扫描命中用户已登记的搜索时不得把来源降级回 schedule。
func TestBatchRunActivePreservesUserOrigin(t *testing.T) {
	store, service, _, notifier := newOriginFixture(t)
	ctx := context.Background()
	if _, err := service.Enqueue(ctx, "sub1", ports.DownloadOriginUser); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunActiveScans(ctx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 || notifier.events[0] != string(application.NotificationDownloadFailed) {
		t.Fatalf("用户发起的搜索失败仍应推送一次通知，实际 %v", notifier.events)
	}
	if n := downloadTaskCount(t, store, ctx); n != 0 {
		t.Fatalf("搜索没找到资源不应建立下载任务，实际 %d 条", n)
	}
}
