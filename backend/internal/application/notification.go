package application

import (
	"context"
	"strings"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// NotificationEvent 是业务通知事件的封闭取值。
// 每个事件在设置页「消息渠道」中对应一个渠道开关，事件语义只在这里定义一次。
type NotificationEvent string

const (
	// NotificationSubscribe 订阅创建成功。
	NotificationSubscribe NotificationEvent = "subscribe"
	// NotificationSubscribeFailed 订阅创建失败。
	NotificationSubscribeFailed NotificationEvent = "subscribe_failed"
	// NotificationDownloadStart 订阅下载已提交到下载器。
	NotificationDownloadStart NotificationEvent = "download_start"
	// NotificationDownloadComplete 下载任务已完成。
	NotificationDownloadComplete NotificationEvent = "download_complete"
	// NotificationDownloadFailed 下载任务失败。
	NotificationDownloadFailed NotificationEvent = "download_failed"
	// NotificationAgentChat 渠道内的自然语言对话；它是入站交互开关，不是推送通知。
	NotificationAgentChat NotificationEvent = "agent_chat"
)

// notificationChannel 描述一个可推送的通知渠道。
type notificationChannel struct {
	// Channel 是 ports 中定义的渠道标识。
	Channel string
	// Prefix 是该渠道通知开关的设置键前缀。
	Prefix string
	// TargetKey 是推送目标设置键；企业微信沿用 | 分隔的多接收者写法。
	TargetKey string
}

// notificationChannels 是通知渠道的唯一权威定义，顺序即推送顺序。
var notificationChannels = []notificationChannel{
	{Channel: ports.ChannelTelegram, Prefix: "TELEGRAM", TargetKey: "TELEGRAM_CHAT_ID"},
	{Channel: ports.ChannelWeChat, Prefix: "WECHAT", TargetKey: "WECHAT_TO_USER"},
}

// notificationEvents 是通知事件的唯一权威定义，顺序即设置页复选框顺序。
// 键名 = 渠道前缀 + "_" + 后缀；开关默认值由数据库迁移种子决定，
// 推送类通知默认关闭（新增行为，未经确认不向渠道推送），Agent 对话默认开启（沿用历史行为）。
var notificationEvents = []struct {
	Event  NotificationEvent
	Suffix string
}{
	{Event: NotificationSubscribe, Suffix: "NOTIFY_SUBSCRIBE"},
	{Event: NotificationSubscribeFailed, Suffix: "NOTIFY_SUBSCRIBE_FAILED"},
	{Event: NotificationDownloadStart, Suffix: "NOTIFY_DOWNLOAD_START"},
	{Event: NotificationDownloadComplete, Suffix: "NOTIFY_DOWNLOAD_COMPLETE"},
	{Event: NotificationDownloadFailed, Suffix: "NOTIFY_DOWNLOAD_FAILED"},
	{Event: NotificationAgentChat, Suffix: "NOTIFY_AGENT_CHAT"},
}

// NotificationSettingKey 返回某渠道某事件的设置键；渠道或事件未知时返回空串。
func NotificationSettingKey(channel string, event NotificationEvent) string {
	suffix := ""
	for _, item := range notificationEvents {
		if item.Event == event {
			suffix = item.Suffix
			break
		}
	}
	if suffix == "" {
		return ""
	}
	for _, item := range notificationChannels {
		if item.Channel == channel {
			return item.Prefix + "_" + suffix
		}
	}
	return ""
}

// NotificationSettingKeys 返回全部通知开关的设置键，供数据库迁移种子与测试核对。
func NotificationSettingKeys() []string {
	keys := make([]string, 0, len(notificationChannels)*len(notificationEvents))
	for _, channel := range notificationChannels {
		for _, event := range notificationEvents {
			keys = append(keys, NotificationSettingKey(channel.Channel, event.Event))
		}
	}
	return keys
}

// Notifier 是业务通知的统一出口：调用方只提供事件与文本，渠道开关、推送目标与发送方式由实现方负责。
// 通知失败不得影响业务流程，因此 Notify 不返回错误，只记录日志。
type Notifier interface {
	// Notify 向所有启用该事件的渠道推送一条通知；未启用或未配置的渠道被跳过。
	Notify(ctx context.Context, event NotificationEvent, message NotificationMessage)
	// ChannelEventEnabled 报告某渠道是否启用了某事件，用于 Agent 对话这类入站交互。
	ChannelEventEnabled(ctx context.Context, channel string, event NotificationEvent) bool
}

// NotificationMessage 是一条待推送的业务通知。
// Title 是通知标题，正文为空时也必须有它：企业微信图文消息的 title 是必填项，
// 空标题会被平台拒绝，因此标题由业务侧生成而不是交给渠道兜底。
// Text 是通知正文；CoverURL 是可选封面，渠道按自身能力渲染，缺失时降级为纯文本。
type NotificationMessage struct {
	Title    string
	Text     string
	CoverURL string
}

// NotificationService 按设置中的渠道开关推送业务通知。
// 它复用与对话回复完全相同的渠道解析器，不另建第二套发送逻辑。
type NotificationService struct {
	settings func(context.Context) (map[string]string, error)
	channels ports.ChannelResolver
}

// NewNotificationService 绑定设置读取器与渠道解析器；两者都为 nil 时所有通知被静默跳过。
func NewNotificationService(settings func(context.Context) (map[string]string, error), channels ports.ChannelResolver) *NotificationService {
	return &NotificationService{settings: settings, channels: channels}
}

// Notify 实现 Notifier；开关未启用、推送目标为空或渠道未配置时跳过。
// 带封面的通知走渠道的图文消息（Telegram 受「图片防剧透」控制是否打码，企业微信受「微信封面推送」控制用哪张图），
// 没有封面时回落到纯文本；发送方式由渠道适配器决定，通知层不重复实现渠道差异。
// 跳过与发送都必须留痕：设置是否真正接线只能靠日志区分，静默跳过与「开关没生效」在界面上无法分辨。
func (n *NotificationService) Notify(ctx context.Context, event NotificationEvent, message NotificationMessage) {
	if n == nil || n.settings == nil || n.channels == nil {
		return
	}
	if strings.TrimSpace(message.Title) == "" && strings.TrimSpace(message.Text) == "" {
		return
	}
	values, err := n.settings(ctx)
	if err != nil {
		logging.Error(logging.CategoryNotification, "读取通知设置失败", "event", string(event), "error", err.Error())
		return
	}
	for _, channel := range notificationChannels {
		skip := func(reason string) {
			logging.Info(logging.CategoryNotification, "消息通知已跳过", "channel", channel.Channel, "event", string(event), "reason", reason)
		}
		if !notificationEnabled(values, channel.Channel, event) {
			skip("事件开关未启用")
			continue
		}
		target := strings.TrimSpace(values[channel.TargetKey])
		if target == "" {
			skip("推送目标未配置")
			continue
		}
		sender, ok := n.channels.Sender(channel.Channel)
		if !ok {
			skip("渠道未配置")
			continue
		}
		if err := sendNotification(ctx, sender, target, message); err != nil {
			logging.Error(logging.CategoryNotification, "消息通知发送失败", "channel", channel.Channel, "event", string(event), "error", err.Error())
		} else {
			// 成功也留痕：开关是否真正生效只能靠这条日志判断，静默成功无法与「开关没接线」区分。
			logging.Info(logging.CategoryNotification, "消息通知已发送", "channel", channel.Channel, "event", string(event), "with_cover", strings.TrimSpace(message.CoverURL) != "")
		}
	}
}

// sendNotification 按是否带封面选择发送方式：有封面发图文消息，没有封面发纯文本。
// 标题与正文分开交给渠道，由渠道按各自协议渲染（Telegram 拼 caption，企业微信填 news.title/description），
// 通知层不出现第二套文案拼装口径。
func sendNotification(ctx context.Context, sender ports.ChannelSender, target string, message NotificationMessage) error {
	title := strings.TrimSpace(message.Title)
	text := strings.TrimSpace(message.Text)
	if cover := strings.TrimSpace(message.CoverURL); cover != "" {
		return sender.SendPhoto(ctx, target, cover, title, text)
	}
	return sender.SendText(ctx, target, NotificationPlainText(title, text))
}

// NotificationPlainText 把标题与正文合成纯文本，供无封面时降级发送；缺失部分自动省略。
func NotificationPlainText(title, text string) string {
	title = strings.TrimSpace(title)
	text = strings.TrimSpace(text)
	switch {
	case title == "":
		return text
	case text == "":
		return title
	default:
		return title + "\n" + text
	}
}

// NotificationHeadline 生成「番号X + 动作」形式的通知标题，与对标站文案一致；
// 番号缺失时只保留动作，保证标题始终非空。
func NotificationHeadline(code, action string) string {
	code = strings.TrimSpace(code)
	action = strings.TrimSpace(action)
	if code == "" {
		return action
	}
	return "番号" + code + action
}

// ChannelEventEnabled 实现 Notifier；设置不可读或键未声明时按关闭处理，不在未知状态下发消息。
func (n *NotificationService) ChannelEventEnabled(ctx context.Context, channel string, event NotificationEvent) bool {
	if n == nil || n.settings == nil {
		return false
	}
	if NotificationSettingKey(channel, event) == "" {
		return false
	}
	values, err := n.settings(ctx)
	if err != nil {
		logging.Error(logging.CategoryNotification, "读取通知开关失败", "event", string(event), "error", err.Error())
		return false
	}
	return notificationEnabled(values, channel, event)
}

// notificationEnabled 判断某渠道某事件是否开启；缺失或非 true 一律视为关闭。
func notificationEnabled(values map[string]string, channel string, event NotificationEvent) bool {
	key := NotificationSettingKey(channel, event)
	return key != "" && strings.TrimSpace(values[key]) == "true"
}
