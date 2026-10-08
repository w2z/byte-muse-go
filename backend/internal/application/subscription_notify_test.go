package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// stubSubscriptionRepository 是内存仓储：Create/Update/Cancel 统一返回预置结果，List 不被这些测试调用。
type stubSubscriptionRepository struct {
	item    domain.Subscription
	created bool
	err     error
}

func (r *stubSubscriptionRepository) Create(context.Context, ports.CreateSubscription) (domain.Subscription, bool, error) {
	return r.item, r.created, r.err
}

func (r *stubSubscriptionRepository) Update(context.Context, ports.UpdateSubscription) (domain.Subscription, error) {
	if r.err != nil {
		return domain.Subscription{}, r.err
	}
	return r.item, nil
}

func (r *stubSubscriptionRepository) Cancel(context.Context, string) (domain.Subscription, bool, error) {
	if r.err != nil {
		return domain.Subscription{}, false, r.err
	}
	return r.item, true, nil
}

func (r *stubSubscriptionRepository) List(context.Context, ports.SubscriptionListQuery) (domain.SubscriptionPage, error) {
	return domain.SubscriptionPage{}, errors.New("not used")
}

// captureNotifier 记录被推送的事件、标题、正文与封面。
type captureNotifier struct{ events []string }

func (c *captureNotifier) Notify(_ context.Context, event NotificationEvent, message NotificationMessage) {
	c.events = append(c.events, string(event)+"|"+message.Title+"|"+message.Text+"|"+message.CoverURL)
}

func (c *captureNotifier) ChannelEventEnabled(context.Context, string, NotificationEvent) bool {
	return true
}

// TestSubscriptionServiceNotifiesCreateOutcome 验证新建订阅通知一次、幂等重放不重复通知、创建失败通知原因。
func TestSubscriptionServiceNotifiesCreateOutcome(t *testing.T) {
	translated := "标题"
	banner := "https://img.example/banner.jpg"
	media := &domain.Media{ID: "m1", Code: "SSIS-001", Title: "原标题", TranslatedTitle: &translated, BannerURL: &banner}
	repository := &stubSubscriptionRepository{item: domain.Subscription{ID: "s1", MediaID: "m1", Media: media}, created: true}
	notifier := &captureNotifier{}
	service := NewSubscriptionService(repository)
	service.SetNotifier(notifier)
	command := CreateSubscriptionCommand{IdempotencyKey: "channel:SSIS-001", MediaID: "m1", Mode: domain.SubscriptionModeStrict, Label: "SSIS-001"}
	ctx := context.Background()

	if _, _, err := service.Create(ctx, command); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 || notifier.events[0] != string(NotificationSubscribe)+"|番号: SSIS-001|状态: 已加入订阅列表\n描述: 标题|"+banner {
		t.Fatalf("新建订阅通知 = %v", notifier.events)
	}

	repository.created = false
	if _, _, err := service.Create(ctx, command); err != nil {
		t.Fatal(err)
	}
	if len(notifier.events) != 1 {
		t.Fatalf("幂等重放不应重复通知: %v", notifier.events)
	}

	repository.err = errors.New("媒体不存在")
	if _, _, err := service.Create(ctx, command); err == nil {
		t.Fatal("期望订阅创建失败")
	}
	if len(notifier.events) != 2 || !strings.Contains(notifier.events[1], string(NotificationSubscribeFailed)+"|番号: SSIS-001|状态: 订阅失败\n描述: 原因：媒体不存在") {
		t.Fatalf("订阅失败通知 = %v", notifier.events)
	}
}

// TestSubscriptionServiceWithoutNotifierStillCreates 验证未注入通知服务时订阅流程完全不受影响。
func TestSubscriptionServiceWithoutNotifierStillCreates(t *testing.T) {
	repository := &stubSubscriptionRepository{item: domain.Subscription{ID: "s1", MediaID: "m1"}, created: true}
	service := NewSubscriptionService(repository)
	if _, created, err := service.Create(context.Background(), CreateSubscriptionCommand{IdempotencyKey: "channel:SSIS-001", MediaID: "m1", Mode: domain.SubscriptionModeStrict}); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
}

// TestDownloadNotificationsCarryTitleAndCover 约束下载通知始终带标题与封面。
// 标题非空是企业微信图文消息的硬要求；封面则决定两个图片开关（TG 防剧透、微信封面推送）是否有作用对象。
func TestDownloadNotificationsCarryTitleAndCover(t *testing.T) {
	notifier := &captureNotifier{}
	service := &SubscriptionDownloadService{}
	service.SetNotifier(notifier)
	ctx := context.Background()
	cover := "https://img.example/banner.jpg"

	service.notifyDownloadStart(ctx, ports.PendingSubmission{Code: "SSIS-001", Title: "标题", Site: "站点A", Kind: "bt", URI: "https://example.com/download", Cover: cover})
	service.notifyDownloadFailed(ctx, "SSIS-001", "标题", cover, "资源站搜索失败")

	want := []string{
		string(NotificationDownloadStart) + "|番号: SSIS-001|状态: 开始下载\n站点: 站点A\n来源: BT\n下载链接: https://example.com/download\n描述: 标题|" + cover,
		string(NotificationDownloadFailed) + "|番号: SSIS-001|状态: 下载失败\n站点: 暂无\n来源: 暂无\n下载链接: 暂无\n描述: 标题 原因：资源站搜索失败|" + cover,
	}
	if len(notifier.events) != len(want) {
		t.Fatalf("下载通知数量 = %v", notifier.events)
	}
	for i, expected := range want {
		if notifier.events[i] != expected {
			t.Errorf("下载通知[%d] = %q，期望 %q", i, notifier.events[i], expected)
		}
	}
}
