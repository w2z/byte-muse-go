package agent

import (
	"context"
	"strings"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// 订阅来源标识写入订阅过滤条件，便于后台区分人工、渠道与 Agent 发起的订阅。
const subscriptionSourceKey = "source"

// SubscribeByCode 按番号创建订阅并返回面向用户的单行结果。
// 订阅受理与下载是两个独立状态：这里只受理订阅，不承诺已开始下载。
func SubscribeByCode(ctx context.Context, deps Deps, code string) string {
	normalized := domain.NormalizeCode(code)
	if normalized == "" {
		return "番号无效，请发送类似 SSIS-001 的番号"
	}
	if deps.Subscriptions == nil {
		return "订阅服务不可用"
	}
	media, ok := lookupMedia(ctx, deps, normalized)
	if !ok {
		return "番号 " + normalized + " 不在媒体库中，请先在采集页入库后再订阅"
	}
	if media.SubscriptionStatus == domain.SubscriptionStatusActive {
		enqueueUserDownload(ctx, deps, activeSubscriptionID(media), normalized)
		return "番号 " + normalized + " 已在订阅中"
	}
	item, _, err := deps.Subscriptions.Create(ctx, application.CreateSubscriptionCommand{
		IdempotencyKey: "channel:" + normalized,
		MediaID:        media.ID,
		Mode:           domain.SubscriptionModeStrict,
		Filter:         map[string]any{subscriptionSourceKey: "channel"},
		Label:          normalized,
	})
	if err != nil {
		return "番号 " + normalized + " 订阅失败: " + err.Error()
	}
	enqueueUserDownload(ctx, deps, item.ID, normalized)
	title := media.Title
	if media.TranslatedTitle != nil && strings.TrimSpace(*media.TranslatedTitle) != "" {
		title = *media.TranslatedTitle
	}
	return "番号 " + normalized + " 已加入订阅。" + strings.TrimSpace(title)
}

// activeSubscriptionID 取影片当前的有效订阅 ID；没有有效订阅时返回空串。
func activeSubscriptionID(media domain.Media) string {
	if media.ActiveSubscription == nil {
		return ""
	}
	return media.ActiveSubscription.ID
}

// enqueueUserDownload 把用户发番号当作一次显式下载请求立即登记，失败时由下载服务按 user 来源推送通知。
// 对标系统收到番号同样会立即搜索资源，因此这里不等待定时任务扫描。
// 登记失败不影响订阅结果本身，只记日志：订阅已经生效，不能因为排队失败把成功回报成失败。
func enqueueUserDownload(ctx context.Context, deps Deps, subscriptionID, code string) {
	if deps.SubscriptionDownloads == nil || strings.TrimSpace(subscriptionID) == "" {
		return
	}
	if _, err := deps.SubscriptionDownloads.Enqueue(ctx, subscriptionID, ports.DownloadOriginUser); err != nil {
		logging.Error(logging.CategoryDownload, "渠道番号下载登记失败", "code", code, "error", err.Error())
	}
}

// lookupMedia 在本地目录中按番号精确定位影片。
func lookupMedia(ctx context.Context, deps Deps, code string) (domain.Media, bool) {
	if deps.Queries == nil {
		return domain.Media{}, false
	}
	page, err := deps.Queries.Search(ctx, code, 1, 20)
	if err != nil {
		return domain.Media{}, false
	}
	for _, item := range page.Items {
		if domain.NormalizeCode(item.Code) == code {
			return item, true
		}
	}
	return domain.Media{}, false
}
