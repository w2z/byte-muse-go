package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// recordingDownloadRepository 只记录 Enqueue 调用；其余方法在渠道订阅测试中不会被触发。
type recordingDownloadRepository struct {
	subscriptionIDs []string
	origins         []ports.DownloadOrigin
}

func (r *recordingDownloadRepository) Enqueue(_ context.Context, subscriptionID string, origin ports.DownloadOrigin) (domain.DownloadTask, error) {
	r.subscriptionIDs = append(r.subscriptionIDs, subscriptionID)
	r.origins = append(r.origins, origin)
	return domain.DownloadTask{ID: "task-1", MediaID: "m1", Status: domain.DownloadStatusQueued, CreatedAt: time.Now().UTC()}, nil
}

func (r *recordingDownloadRepository) EnqueueActive(context.Context) (int, error) {
	return 0, nil
}

func (r *recordingDownloadRepository) Claim(context.Context, time.Time) (*ports.SubscriptionDownloadAttempt, error) {
	return nil, nil
}

func (r *recordingDownloadRepository) SetCandidate(context.Context, ports.SubscriptionDownloadAttempt, string, string, string, string, string, bool) error {
	return nil
}

func (r *recordingDownloadRepository) FinishSearch(context.Context, ports.SubscriptionDownloadAttempt, string) error {
	return nil
}

func (r *recordingDownloadRepository) ClaimPending(context.Context, time.Time) (*ports.PendingSubmission, error) {
	return nil, nil
}

func (r *recordingDownloadRepository) FinishSubmission(context.Context, ports.PendingSubmission, bool, string) error {
	return nil
}

func (r *recordingDownloadRepository) ReleasePending(context.Context, ports.PendingSubmission, string) error {
	return nil
}

// TestSubscribeByCodeEnqueuesUserDownload 验证聊天渠道发番号会立即按 user 来源登记下载：
// 新建订阅和重复发送已订阅番号都要登记，否则用户显式请求拿不到失败通知。
func TestSubscribeByCodeEnqueuesUserDownload(t *testing.T) {
	media := testMedia("m1", "TGHH-091", "玻璃跳虫")
	catalog := &fakeCatalogQueryRepository{media: []domain.Media{media}}
	subscriptions := newFakeSubscriptionRepository(media)
	downloads := &recordingDownloadRepository{}
	deps := Deps{
		Queries:               application.NewCatalogQueryService(catalog),
		Subscriptions:         application.NewSubscriptionService(subscriptions),
		SubscriptionDownloads: application.NewSubscriptionDownloadService(downloads, nil, nil, nil),
	}
	ctx := context.Background()

	if out := SubscribeByCode(ctx, deps, "tghh 091"); !strings.Contains(out, "已加入订阅") {
		t.Fatalf("新建订阅文案 = %q", out)
	}
	if len(downloads.subscriptionIDs) != 1 || downloads.subscriptionIDs[0] != "sub-1" || downloads.origins[0] != ports.DownloadOriginUser {
		t.Fatalf("新建订阅应登记一次 user 下载，实际 %v %v", downloads.subscriptionIDs, downloads.origins)
	}

	// 模拟真实搜索投影：订阅生效后影片带回有效订阅，重复发送番号才能命中「已在订阅中」分支。
	active := domain.Subscription{ID: "sub-1", MediaID: "m1", Status: domain.SubscriptionStatusActive, Mode: domain.SubscriptionModeStrict}
	media.ActiveSubscription = &active
	media.SubscriptionStatus = domain.SubscriptionStatusActive
	catalog.media = []domain.Media{media}

	if out := SubscribeByCode(ctx, deps, "TGHH-091"); !strings.Contains(out, "已在订阅中") {
		t.Fatalf("已订阅文案 = %q", out)
	}
	if len(downloads.subscriptionIDs) != 2 || downloads.subscriptionIDs[1] != "sub-1" || downloads.origins[1] != ports.DownloadOriginUser {
		t.Fatalf("重复发送已订阅番号应再登记一次 user 下载，实际 %v %v", downloads.subscriptionIDs, downloads.origins)
	}
}
