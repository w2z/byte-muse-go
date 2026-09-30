package application

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// recordingSender 记录通知内容，用于断言开关、推送目标与发送结果。
type recordingSender struct {
	sent []string
	err  error
}

func (s *recordingSender) SendText(_ context.Context, chatID, text string) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, chatID+"|"+text)
	return nil
}

// SendPhoto 记录图文消息；photoURL 为空时按渠道约定降级为纯文本，与真实适配器一致。
func (s *recordingSender) SendPhoto(_ context.Context, chatID, photoURL, title, text string) error {
	if s.err != nil {
		return s.err
	}
	if strings.TrimSpace(photoURL) == "" {
		return s.SendText(context.Background(), chatID, composeTitleText(title, text))
	}
	s.sent = append(s.sent, chatID+"|photo:"+photoURL+"|"+composeTitleText(title, text))
	return nil
}

// composeTitleText 按渠道口径拼装标题与正文，供断言使用；缺失部分自动省略。
func composeTitleText(title, text string) string {
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

// SendCard 在通知链路上不使用卡片：记录为「标题+正文」的文本，保证断言口径一致。
func (s *recordingSender) SendCard(_ context.Context, chatID string, card ports.OutboundCard) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, chatID+"|"+card.Title+"\n"+card.Text)
	return nil
}

// ReplaceButtons 在通知链路上不适用，按接口约定返回 nil。
func (s *recordingSender) ReplaceButtons(context.Context, string, string, []ports.ActionButton) error {
	return nil
}

// recordingChannels 按渠道返回固定发送器；未登记的渠道视为未配置。
type recordingChannels map[string]*recordingSender

func (c recordingChannels) Sender(channel string) (ports.ChannelSender, bool) {
	sender, ok := c[channel]
	if !ok || sender == nil {
		return nil, false
	}
	return sender, true
}

// settingsFrom 用固定快照构造设置读取器。
func settingsFrom(values map[string]string) func(context.Context) (map[string]string, error) {
	return func(context.Context) (map[string]string, error) { return values, nil }
}

// TestNotificationSettingKeysAreWritableBooleans 约束每个通知开关都在可写配置清单里声明为布尔项。
// 漏声明会让保存请求被拒绝，或在设置页看不到开关。
func TestNotificationSettingKeysAreWritableBooleans(t *testing.T) {
	keys := NotificationSettingKeys()
	if len(keys) != 12 {
		t.Fatalf("通知开关应为 2 渠道 × 6 事件 = 12 个，实际 %d 个：%v", len(keys), keys)
	}
	for _, key := range keys {
		spec, ok := writableSettings[key]
		if !ok {
			t.Errorf("通知开关 %s 未在 writableSettings 中声明", key)
			continue
		}
		if spec.kind != settingBool {
			t.Errorf("通知开关 %s 的类型应为 %s，实际 %s", key, settingBool, spec.kind)
		}
		if spec.secret {
			t.Errorf("通知开关 %s 不应作为敏感值存储", key)
		}
	}
}

// TestNotifyOnlyUsesEnabledChannels 验证两个渠道的开关互不影响，且推送目标取自各渠道自己的设置。
func TestNotifyOnlyUsesEnabledChannels(t *testing.T) {
	telegram := &recordingSender{}
	wechat := &recordingSender{}
	values := map[string]string{
		"TELEGRAM_NOTIFY_SUBSCRIBE": "true",
		"WECHAT_NOTIFY_SUBSCRIBE":   "false",
		"TELEGRAM_CHAT_ID":          "1001",
		"WECHAT_TO_USER":            "@all",
	}
	service := NewNotificationService(settingsFrom(values), recordingChannels{ports.ChannelTelegram: telegram, ports.ChannelWeChat: wechat})

	service.Notify(context.Background(), NotificationSubscribe, NotificationMessage{Text: "订阅成功：SSIS-001"})

	if len(telegram.sent) != 1 || telegram.sent[0] != "1001|订阅成功：SSIS-001" {
		t.Fatalf("Telegram 通知 = %v", telegram.sent)
	}
	if len(wechat.sent) != 0 {
		t.Fatalf("微信开关关闭时不应发送，实际 %v", wechat.sent)
	}
}

// TestNotifySkipsEmptyTargetAndUnconfiguredChannel 验证未配置推送目标或渠道未配置时静默跳过。
func TestNotifySkipsEmptyTargetAndUnconfiguredChannel(t *testing.T) {
	telegram := &recordingSender{}
	values := map[string]string{
		"TELEGRAM_NOTIFY_DOWNLOAD_START": "true",
		"WECHAT_NOTIFY_DOWNLOAD_START":   "true",
		"WECHAT_TO_USER":                 "@all",
	}
	service := NewNotificationService(settingsFrom(values), recordingChannels{ports.ChannelTelegram: telegram})

	service.Notify(context.Background(), NotificationDownloadStart, NotificationMessage{Text: "开始下载：SSIS-001"})

	if len(telegram.sent) != 0 {
		t.Fatalf("推送目标为空时不应发送，实际 %v", telegram.sent)
	}
}

// TestNotifyIgnoresSenderFailure 验证渠道发送失败不会把错误抛回业务流程。
func TestNotifyIgnoresSenderFailure(t *testing.T) {
	telegram := &recordingSender{err: errors.New("network down")}
	values := map[string]string{"TELEGRAM_NOTIFY_SUBSCRIBE": "true", "TELEGRAM_CHAT_ID": "1001"}
	service := NewNotificationService(settingsFrom(values), recordingChannels{ports.ChannelTelegram: telegram})

	service.Notify(context.Background(), NotificationSubscribe, NotificationMessage{Text: "订阅成功：SSIS-001"})
}

// TestNotifyIgnoresSettingsFailure 验证设置不可读时通知被跳过而不是中断调用方。
func TestNotifyIgnoresSettingsFailure(t *testing.T) {
	telegram := &recordingSender{}
	service := NewNotificationService(func(context.Context) (map[string]string, error) {
		return nil, errors.New("settings unavailable")
	}, recordingChannels{ports.ChannelTelegram: telegram})

	service.Notify(context.Background(), NotificationSubscribe, NotificationMessage{Text: "订阅成功：SSIS-001"})
	if len(telegram.sent) != 0 {
		t.Fatalf("设置不可读时不应发送，实际 %v", telegram.sent)
	}
}

// TestChannelEventEnabledRequiresTrueValue 验证缺失或非 true 的开关一律视为关闭。
func TestChannelEventEnabledRequiresTrueValue(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{name: "开启", values: map[string]string{"TELEGRAM_NOTIFY_AGENT_CHAT": "true"}, want: true},
		{name: "关闭", values: map[string]string{"TELEGRAM_NOTIFY_AGENT_CHAT": "false"}, want: false},
		{name: "缺失", values: map[string]string{}, want: false},
		{name: "未知渠道", values: map[string]string{"TELEGRAM_NOTIFY_AGENT_CHAT": "true"}, want: false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			channel := ports.ChannelTelegram
			if item.name == "未知渠道" {
				channel = "slack"
			}
			service := NewNotificationService(settingsFrom(item.values), recordingChannels{})
			if got := service.ChannelEventEnabled(context.Background(), channel, NotificationAgentChat); got != item.want {
				t.Fatalf("ChannelEventEnabled = %v, 期望 %v", got, item.want)
			}
		})
	}
}

// TestNotificationHeadlineAlwaysKeepsAction 验证标题始终非空：企业微信图文消息的 title 是必填项，
// 番号缺失时也必须留下动作描述，否则整条图文消息会被平台拒绝。
func TestNotificationHeadlineAlwaysKeepsAction(t *testing.T) {
	for _, item := range []struct{ code, action, want string }{
		{"SSIS-001", "已加入订阅列表", "番号SSIS-001已加入订阅列表"},
		{"SSIS-001", "开始下载", "番号SSIS-001开始下载"},
		{"", "下载失败", "下载失败"},
		{" ", " 下载失败 ", "下载失败"},
	} {
		if got := NotificationHeadline(item.code, item.action); got != item.want {
			t.Errorf("NotificationHeadline(%q,%q) = %q, 期望 %q", item.code, item.action, got, item.want)
		}
	}
}

// TestNotificationPlainTextJoinsTitleAndText 验证无封面时的纯文本降级不会留下空行或多余换行。
func TestNotificationPlainTextJoinsTitleAndText(t *testing.T) {
	for _, item := range []struct{ title, text, want string }{
		{"标题", "正文", "标题\n正文"},
		{"标题", "", "标题"},
		{"", "正文", "正文"},
		{" ", " ", ""},
	} {
		if got := NotificationPlainText(item.title, item.text); got != item.want {
			t.Errorf("NotificationPlainText(%q,%q) = %q, 期望 %q", item.title, item.text, got, item.want)
		}
	}
}

// TestNotificationEventKeysCoverEveryChannel 验证事件与渠道的笛卡尔积都被声明，没有漏键。
func TestNotificationEventKeysCoverEveryChannel(t *testing.T) {
	for _, channel := range notificationChannels {
		for _, event := range notificationEvents {
			key := NotificationSettingKey(channel.Channel, event.Event)
			if !strings.HasPrefix(key, channel.Prefix+"_") {
				t.Fatalf("渠道 %s 的键 %s 缺少前缀 %s_", channel.Channel, key, channel.Prefix)
			}
			if !strings.HasSuffix(key, event.Suffix) {
				t.Fatalf("事件 %s 的键 %s 缺少后缀 %s", event.Event, key, event.Suffix)
			}
		}
	}
}

// TestNotifySendsPhotoOnlyWhenCoverPresent 验证带封面的通知走图文消息，没有封面时回落到纯文本。
// 两个图片开关（TG 防剧透、微信封面推送）只在图文消息上生效，因此这条分流必须被约束。
func TestNotifySendsPhotoOnlyWhenCoverPresent(t *testing.T) {
	telegram := &recordingSender{}
	values := map[string]string{"TELEGRAM_NOTIFY_SUBSCRIBE": "true", "TELEGRAM_CHAT_ID": "1001"}
	service := NewNotificationService(settingsFrom(values), recordingChannels{ports.ChannelTelegram: telegram})

	service.Notify(context.Background(), NotificationSubscribe, NotificationMessage{
		Title:    "番号SSIS-001已加入订阅列表",
		Text:     "标题",
		CoverURL: "https://img.example/banner.jpg",
	})
	if len(telegram.sent) != 1 || telegram.sent[0] != "1001|photo:https://img.example/banner.jpg|番号SSIS-001已加入订阅列表\n标题" {
		t.Fatalf("带封面通知 = %v", telegram.sent)
	}

	// 只有标题没有正文时也必须发送：这是图文消息标题的兜底口径。
	service.Notify(context.Background(), NotificationSubscribe, NotificationMessage{Title: "番号SSIS-002已加入订阅列表"})
	if len(telegram.sent) != 2 || telegram.sent[1] != "1001|番号SSIS-002已加入订阅列表" {
		t.Fatalf("无封面通知 = %v", telegram.sent)
	}
}

// TestMediaCoverPrefersBannerThenPoster 验证推送封面优先横幅图、其次海报图、都没有时为空串。
func TestMediaCoverPrefersBannerThenPoster(t *testing.T) {
	banner, poster, blank := "https://img.example/banner.jpg", "https://img.example/poster.jpg", " "
	for _, item := range []struct {
		name string
		item domain.Media
		want string
	}{
		{name: "横幅优先", item: domain.Media{BannerURL: &banner, PosterURL: &poster}, want: banner},
		{name: "回退海报", item: domain.Media{PosterURL: &poster}, want: poster},
		{name: "空白横幅回退海报", item: domain.Media{BannerURL: &blank, PosterURL: &poster}, want: poster},
		{name: "都没有", item: domain.Media{}, want: ""},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := MediaCover(item.item); got != item.want {
				t.Fatalf("MediaCover = %q，期望 %q", got, item.want)
			}
		})
	}
}

// TestWechatCoverUsesExternalDomainCacheEntry 验证配置「外网访问地址」后，微信封面指向本机缓存入口，
// 与页面显示同一地址；其他渠道不受影响，仍使用原图地址。
func TestWechatCoverUsesExternalDomainCacheEntry(t *testing.T) {
	telegram, wechat := &recordingSender{}, &recordingSender{}
	values := map[string]string{
		"TELEGRAM_NOTIFY_SUBSCRIBE": "true", "TELEGRAM_CHAT_ID": "1001",
		"WECHAT_NOTIFY_SUBSCRIBE": "true", "WECHAT_TO_USER": "@all",
		"EXTERNAL_DOMAIN": "https://muse.example.com/",
	}
	service := NewNotificationService(settingsFrom(values), recordingChannels{ports.ChannelTelegram: telegram, ports.ChannelWeChat: wechat})

	service.Notify(context.Background(), NotificationSubscribe, NotificationMessage{
		Title: "番号SSIS-001已加入订阅列表", CoverURL: "https://img.example/banner.jpg", Code: "SSIS-001",
	})

	wantWechat := "@all|photo:https://muse.example.com/api/v1/covers/SSIS-001?source=" + url.QueryEscape("https://img.example/banner.jpg") + "|番号SSIS-001已加入订阅列表"
	if len(wechat.sent) != 1 || wechat.sent[0] != wantWechat {
		t.Fatalf("微信封面地址 = %v，期望 %s", wechat.sent, wantWechat)
	}
	if len(telegram.sent) != 1 || telegram.sent[0] != "1001|photo:https://img.example/banner.jpg|番号SSIS-001已加入订阅列表" {
		t.Fatalf("其他渠道应保持原图地址，实际 %v", telegram.sent)
	}
}

// TestWechatCoverURLKeepsSourceWhenIncomplete 验证外网地址、番号或可下载封面缺失时不改写地址，
// 避免把封面换成必然取不到的链接。
func TestWechatCoverURLKeepsSourceWhenIncomplete(t *testing.T) {
	const cover = "https://img.example/banner.jpg"
	for _, item := range []struct{ name, domain, code, cover, want string }{
		{name: "完整", domain: "https://muse.example.com", code: "SSIS-001", cover: cover, want: "https://muse.example.com/api/v1/covers/SSIS-001?source=" + url.QueryEscape(cover)},
		{name: "外网地址为空", code: "SSIS-001", cover: cover, want: cover},
		{name: "番号为空", domain: "https://muse.example.com", cover: cover, want: cover},
		{name: "封面不是绝对地址", domain: "https://muse.example.com", code: "SSIS-001", cover: "/cover.jpg", want: "/cover.jpg"},
		{name: "封面缺失", domain: "https://muse.example.com", code: "SSIS-001", cover: " ", want: " "},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := wechatCoverURL(item.domain, item.code, item.cover); got != item.want {
				t.Fatalf("wechatCoverURL(%q,%q,%q) = %q，期望 %q", item.domain, item.code, item.cover, got, item.want)
			}
		})
	}
}
