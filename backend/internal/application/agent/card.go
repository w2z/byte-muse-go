package agent

import (
	"context"
	"slices"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// 卡片动作载荷：固定为 "bm:<动作>:<番号>"，渠道原样回传，只有本包解析。
// 标识统一使用番号而不是内部 ID，重复点击和过期卡片都只会重新按番号取当前状态。
const (
	cardActionPrefix      = "bm"
	cardActionSubscribe   = "sub"
	cardActionUnsubscribe = "unsub"
	cardActionDownload    = "dl"
	cardActionPause       = "pause"
	cardActionResume      = "resume"
	cardActionRetry       = "retry"
	// cardActionExpired 是载荷无法识别时的统一回复。
	cardActionExpired = "按钮已失效，请重新发送番号"
)

// cardData 组装一个按钮载荷。
func cardData(action, code string) string {
	return cardActionPrefix + ":" + action + ":" + code
}

// parseCardData 解析按钮载荷；前缀、段数或动作名不匹配时返回 false。
func parseCardData(raw string) (action, code string, ok bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 || parts[0] != cardActionPrefix {
		return "", "", false
	}
	action = strings.TrimSpace(parts[1])
	code = NormalizeCode(parts[2])
	if code == "" || !cardActionKnown(action) {
		return "", "", false
	}
	return action, code, true
}

// cardActionKnown 判定动作名是否属于本包定义的动作集合。
func cardActionKnown(action string) bool {
	switch action {
	case cardActionSubscribe, cardActionUnsubscribe, cardActionDownload, cardActionPause, cardActionResume, cardActionRetry:
		return true
	default:
		return false
	}
}

// cardActionLabel 返回动作的中文名，用于按钮文案与结果描述。
func cardActionLabel(action string) string {
	switch action {
	case cardActionSubscribe:
		return "订阅"
	case cardActionUnsubscribe:
		return "取消订阅"
	case cardActionDownload:
		return "开始下载"
	case cardActionPause:
		return "暂停"
	case cardActionResume:
		return "继续"
	case cardActionRetry:
		return "重试"
	default:
		return action
	}
}

// cardForCode 按番号构建状态卡片。
// 番号无效、未入库或没有可执行操作时返回纯文本回复，卡片与文本两条路径由同一份状态判定驱动。
func cardForCode(ctx context.Context, deps Deps, raw string) ports.Reply {
	code := NormalizeCode(raw)
	if code == "" {
		return ports.Reply{Text: "番号无效，请发送类似 SSIS-001 的番号"}
	}
	if deps.Queries == nil {
		return ports.Reply{Text: "订阅服务不可用"}
	}
	media, found := lookupMedia(ctx, deps, code)
	if !found {
		return ports.Reply{Text: "番号 " + code + " 不在媒体库中，请先在采集页入库后再订阅"}
	}
	return mediaCard(ctx, deps, media)
}

// cardForKnownCode 只在番号命中媒体库时返回卡片，用于把已入库番号从 AI 分流中优先截获。
// 未命中时返回 false，由调用方保持原有分流，避免把普通英文文本误判成番号。
func cardForKnownCode(ctx context.Context, deps Deps, raw string) (ports.Reply, bool) {
	if deps.Queries == nil {
		return ports.Reply{}, false
	}
	code := NormalizeCode(raw)
	if code == "" {
		return ports.Reply{}, false
	}
	media, found := lookupMedia(ctx, deps, code)
	if !found {
		return ports.Reply{}, false
	}
	return mediaCard(ctx, deps, media), true
}

// mediaCard 按影片与最近一条下载任务组装回复：有可执行按钮时发卡片，否则只回文本。
func mediaCard(ctx context.Context, deps Deps, media domain.Media) ports.Reply {
	task := latestDownload(ctx, deps, media)
	title, body := cardText(media, task)
	buttons := cardButtons(media, task)
	text := title + "\n" + body
	if len(buttons) == 0 {
		return ports.Reply{Text: text}
	}
	return ports.Reply{
		Card: &ports.OutboundCard{PhotoURL: mediaPoster(media), Title: title, Text: body, Buttons: buttons},
		Text: text,
	}
}

// handleCardAction 执行一次按钮点击：先按番号执行动作，再用执行后的最新状态刷新原卡片按钮，
// 因此用户不需要重新发送番号即可连续操作。
func handleCardAction(ctx context.Context, deps Deps, action ports.InboundAction) ports.Reply {
	name, code, ok := parseCardData(action.Data)
	if !ok {
		return ports.Reply{Text: cardActionExpired}
	}
	reply := ports.Reply{Text: runCardAction(ctx, deps, name, code)}
	if media, found := lookupMedia(ctx, deps, code); found {
		reply.Refresh = &ports.ButtonRefresh{
			MessageID: action.MessageID,
			Buttons:   cardButtons(media, latestDownload(ctx, deps, media)),
		}
	}
	return reply
}

// runCardAction 执行一个已解析的动作并返回面向用户的结果文本。
func runCardAction(ctx context.Context, deps Deps, action, code string) string {
	if action == cardActionSubscribe {
		return SubscribeByCode(ctx, deps, code)
	}
	if deps.Subscriptions == nil {
		return "订阅服务不可用"
	}
	media, found := lookupMedia(ctx, deps, code)
	if !found {
		return "番号 " + code + " 不在媒体库中"
	}
	switch action {
	case cardActionUnsubscribe:
		return unsubscribeCard(ctx, deps, media)
	case cardActionDownload:
		return downloadCard(ctx, deps, media)
	case cardActionPause, cardActionResume, cardActionRetry:
		return controlCard(ctx, deps, media, action)
	default:
		return cardActionExpired
	}
}

// unsubscribeCard 取消番号订阅；没有活动订阅时如实说明，不谎报成功。
func unsubscribeCard(ctx context.Context, deps Deps, media domain.Media) string {
	if media.ActiveSubscription == nil {
		return "番号 " + media.Code + " 当前没有活动订阅"
	}
	if _, err := deps.Subscriptions.Cancel(ctx, media.ActiveSubscription.ID); err != nil {
		return "番号 " + media.Code + " 取消订阅失败：" + err.Error()
	}
	return "番号 " + media.Code + " 已取消订阅"
}

// downloadCard 为活动订阅登记一次资源搜索与下载；已受理不等于下载完成。
func downloadCard(ctx context.Context, deps Deps, media domain.Media) string {
	if media.ActiveSubscription == nil {
		return "番号 " + media.Code + " 当前没有活动订阅，请先订阅"
	}
	if deps.SubscriptionDownloads == nil {
		return "订阅下载服务不可用"
	}
	// 卡片按钮是用户显式点击，失败通知必须推送。
	if _, err := deps.SubscriptionDownloads.Enqueue(ctx, media.ActiveSubscription.ID, ports.DownloadOriginUser); err != nil {
		return "番号 " + media.Code + " 登记下载失败：" + err.Error()
	}
	return "番号 " + media.Code + " 已登记下载，当前状态以搜索与提交结果为准"
}

// controlCard 对当前下载任务执行暂停、继续或重试。
// 动作是否允许由下载任务已有的可用操作决定，卡片不重复实现下载器状态规则。
func controlCard(ctx context.Context, deps Deps, media domain.Media, action string) string {
	if deps.Downloads == nil {
		return "下载服务不可用"
	}
	label := cardActionLabel(action)
	task := latestDownload(ctx, deps, media)
	if task == nil {
		return "番号 " + media.Code + " 当前没有下载任务"
	}
	if !slices.Contains(task.AvailableActions, action) {
		return "番号 " + media.Code + " 当前状态不允许「" + label + "」"
	}
	if err := deps.Downloads.Control(ctx, task.ID, action); err != nil {
		return "番号 " + media.Code + "「" + label + "」执行失败：" + err.Error()
	}
	return "番号 " + media.Code + " 已执行「" + label + "」，下载器状态已核实"
}

// cardButtons 是番号卡片按钮的唯一权威映射：订阅状态与下载任务的可用操作共同决定按钮。
// 顺序即界面顺序，Telegram 每行两个按钮，企业微信按同一顺序渲染。
func cardButtons(media domain.Media, task *domain.DownloadTask) []ports.ActionButton {
	code := NormalizeCode(media.Code)
	if code == "" {
		return nil
	}
	if media.SubscriptionStatus == domain.SubscriptionStatusActive && media.ActiveSubscription != nil {
		buttons := make([]ports.ActionButton, 0, 2)
		switch {
		case task != nil && slices.Contains(task.AvailableActions, cardActionPause):
			buttons = append(buttons, ports.ActionButton{Label: "暂停", Data: cardData(cardActionPause, code)})
		case task != nil && slices.Contains(task.AvailableActions, cardActionResume):
			buttons = append(buttons, ports.ActionButton{Label: "继续", Data: cardData(cardActionResume, code)})
		case task != nil && slices.Contains(task.AvailableActions, cardActionRetry):
			buttons = append(buttons, ports.ActionButton{Label: "重试", Data: cardData(cardActionRetry, code)})
		case task == nil:
			buttons = append(buttons, ports.ActionButton{Label: "开始下载", Data: cardData(cardActionDownload, code)})
		}
		return append(buttons, ports.ActionButton{Label: "取消订阅", Data: cardData(cardActionUnsubscribe, code)})
	}
	if media.LibraryStatus == domain.LibraryStatusPresent {
		return []ports.ActionButton{{Label: "订阅", Data: cardData(cardActionSubscribe, code)}}
	}
	return nil
}

// cardText 生成卡片标题与正文：标题固定为番号，正文包含片名与当前状态。
func cardText(media domain.Media, task *domain.DownloadTask) (string, string) {
	title := strings.TrimSpace(media.Code)
	if title == "" {
		title = media.ID
	}
	lines := make([]string, 0, 2)
	if name := mediaDisplayTitle(media); name != "" {
		lines = append(lines, name)
	}
	lines = append(lines, "状态："+mediaStatusLabel(media, task))
	return title, strings.Join(lines, "\n")
}

// mediaDisplayTitle 优先返回译文标题，其次原标题。
func mediaDisplayTitle(media domain.Media) string {
	if media.TranslatedTitle != nil && strings.TrimSpace(*media.TranslatedTitle) != "" {
		return strings.TrimSpace(*media.TranslatedTitle)
	}
	return strings.TrimSpace(media.Title)
}

// mediaPoster 返回封面地址，缺失时返回空串由渠道自行降级。
func mediaPoster(media domain.Media) string {
	if media.PosterURL == nil {
		return ""
	}
	return strings.TrimSpace(*media.PosterURL)
}

// mediaStatusLabel 把下载器状态、订阅状态与媒体库状态压成一行用户可读文案。
// 下载器实时状态优先于落库状态，避免展示已经过期的阶段。
func mediaStatusLabel(media domain.Media, task *domain.DownloadTask) string {
	if task != nil && task.TransferStatus != nil {
		if label := transferStatusLabel(*task.TransferStatus); label != "" {
			return label
		}
	}
	if media.SubscriptionStatus == domain.SubscriptionStatusActive {
		if media.DownloadStatus != nil {
			if label := downloadStatusLabel(*media.DownloadStatus); label != "" {
				return label
			}
		}
		return "已订阅"
	}
	if media.SubscriptionStatus == domain.SubscriptionStatusCanceled {
		return "已取消订阅"
	}
	if media.LibraryStatus == domain.LibraryStatusPresent {
		return "未订阅"
	}
	return "未入库"
}

// transferStatusLabel 映射下载器上报的传输状态。
func transferStatusLabel(status string) string {
	switch status {
	case "downloading":
		return "下载中"
	case "paused":
		return "已暂停"
	case "stopped":
		return "已停止"
	case "completed":
		return "下载完成"
	case "failed":
		return "下载失败"
	default:
		return ""
	}
}

// downloadStatusLabel 映射落库的下载业务状态。
func downloadStatusLabel(status domain.DownloadStatus) string {
	switch status {
	case domain.DownloadStatusQueued:
		return "排队中"
	case domain.DownloadStatusSearching:
		return "正在搜索资源"
	case domain.DownloadStatusSubmitted:
		return "已提交下载器"
	case domain.DownloadStatusDownloading:
		return "下载中"
	case domain.DownloadStatusCompleted:
		return "下载完成"
	case domain.DownloadStatusFailed:
		return "下载失败"
	case domain.DownloadStatusUnknown:
		return "下载状态待确认"
	default:
		return ""
	}
}

// latestDownload 返回该影片最近一条下载任务；查询失败按无任务处理并记录日志，不阻断卡片展示。
func latestDownload(ctx context.Context, deps Deps, media domain.Media) *domain.DownloadTask {
	if deps.Downloads == nil || strings.TrimSpace(media.ID) == "" {
		return nil
	}
	task, err := deps.Downloads.LatestForMedia(ctx, media.ID)
	if err != nil {
		logging.Error(logging.CategoryAgent, "读取影片下载任务失败", "code", media.Code, "error", err.Error())
		return nil
	}
	return task
}
