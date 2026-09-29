package bootstrap

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/application/agent"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/telegram"
	"bytemuse/backend/internal/platform/torrentsearch"
	"bytemuse/backend/internal/platform/wechat"
	"bytemuse/backend/internal/ports"
)

// channelRegistry 按渠道名解析当前配置的发送器。
// 配置在执行时读取，保存设置后无需重启即可生效；凭据只存在于内存与数据库，不写日志。
type channelRegistry struct {
	settings  *application.SettingsService
	mu        sync.Mutex
	senders   map[string]ports.ChannelSender
	signature map[string]string
}

// newChannelRegistry 绑定设置服务。
func newChannelRegistry(settings *application.SettingsService) *channelRegistry {
	return &channelRegistry{settings: settings, senders: map[string]ports.ChannelSender{}, signature: map[string]string{}}
}

// Sender 返回指定渠道的发送器；渠道未配置或设置读取失败时返回 false。
func (r *channelRegistry) Sender(channel string) (ports.ChannelSender, bool) {
	if r == nil || r.settings == nil {
		return nil, false
	}
	values, err := r.values()
	if err != nil {
		logging.Error(logging.CategoryNotification, "读取消息渠道设置失败", "error", err.Error())
		return nil, false
	}
	switch channel {
	case ports.ChannelTelegram:
		token := strings.TrimSpace(values["TELEGRAM_BOT_TOKEN"])
		if token == "" {
			return nil, false
		}
		proxy := strings.TrimSpace(values["PROXY"])
		spoiler := strings.TrimSpace(values["TELEGRAM_SPOILER"]) == "true"
		signature := strings.Join([]string{token, proxy, values["TELEGRAM_SPOILER"]}, "\x00")
		return r.cached(channel, signature, func() ports.ChannelSender {
			return telegram.NewClient(token, proxy, spoiler, nil)
		}), true
	case ports.ChannelWeChat:
		corpID := strings.TrimSpace(values["WECHAT_CORP_ID"])
		corpSecret := strings.TrimSpace(values["WECHAT_CORP_SECRET"])
		agentID := strings.TrimSpace(values["WECHAT_AGENT_ID"])
		if corpID == "" || corpSecret == "" || agentID == "" {
			return nil, false
		}
		photo := strings.TrimSpace(values["WECHAT_PHOTO"])
		banner := strings.TrimSpace(values["WECHAT_BANNER"]) == "true"
		signature := strings.Join([]string{corpID, corpSecret, agentID, photo, values["WECHAT_BANNER"], strings.TrimSpace(values["WECHAT_PROXY"])}, "\x00")
		return r.cached(channel, signature, func() ports.ChannelSender {
			return wechat.NewClient(corpID, corpSecret, agentID, strings.TrimSpace(values["WECHAT_PROXY"]), photo, banner, nil)
		}), true
	default:
		return nil, false
	}
}

// values 读取当前设置快照。
func (r *channelRegistry) values() (map[string]string, error) {
	current, err := r.settings.Get(context.Background())
	if err != nil {
		return nil, err
	}
	return current.Values, nil
}

// cached 复用同一配置下的发送器，避免每次调用都丢失渠道侧令牌缓存。
func (r *channelRegistry) cached(channel, signature string, build func() ports.ChannelSender) ports.ChannelSender {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.signature[channel] == signature {
		if sender, ok := r.senders[channel]; ok {
			return sender
		}
	}
	sender := build()
	r.senders[channel] = sender
	r.signature[channel] = signature
	return sender
}

// settingsResourceSearcher 按当前设置构造资源搜索器，使 Agent 与订阅下载复用同一套站点配置。
type settingsResourceSearcher struct{ settings *application.SettingsService }

// Search 读取最新设置后执行一次资源搜索。
func (s settingsResourceSearcher) Search(ctx context.Context, code string) ([]torrentsearch.Resource, error) {
	values, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	searcher, _ := newResourceSearcher(values.Values)
	return searcher.Search(ctx, code)
}

// newResourceSearcher 构造公开站点与已配置私有站点合并后的资源搜索器。
// 订阅下载与 Agent 搜种必须共用这里，避免两处站点配置漂移。
func newResourceSearcher(values map[string]string) (application.ResourceSearcher, map[string]application.PrivateTorrentSource) {
	sources := []application.ResourceSearcher{torrentsearch.NewNyaaSearcher(nil, "")}
	privateSearchers, privateSources := configuredPrivateSites(values, nil)
	sources = append(sources, privateSearchers...)
	return application.MultiResourceSearcher{Sources: sources}, privateSources
}

// channelSupervisorInterval 是重新对齐渠道配置的间隔。
const channelSupervisorInterval = 15 * time.Second

// RunChannelSupervisor 持续把 Telegram 长轮询对齐到当前设置，直到 ctx 结束。
// 入站消息交给与 HTTP 回调共用的渠道处理器，回复投递不在本函数内重复实现。
// Bot Token 变化时重建轮询器；未配置时保持空闲，不产生任何外网请求。
func RunChannelSupervisor(ctx context.Context, settings *application.SettingsService, handler ports.ChannelMessageHandler) {
	ticker := time.NewTicker(channelSupervisorInterval)
	defer ticker.Stop()

	var (
		cancel context.CancelFunc
		done   chan struct{}
		active string
	)
	stop := func() {
		if cancel == nil {
			return
		}
		cancel()
		<-done
		cancel, done, active = nil, nil, ""
	}
	defer stop()

	for {
		values, err := settings.Get(ctx)
		if err != nil {
			logging.Error(logging.CategoryNotification, "读取 Telegram 设置失败", "error", err.Error())
		} else {
			token := strings.TrimSpace(values.Values["TELEGRAM_BOT_TOKEN"])
			proxy := strings.TrimSpace(values.Values["PROXY"])
			whitelist := parseTelegramWhitelist(values.Values["TELEGRAM_WHITELIST"])
			signature := strings.Join([]string{token, proxy, strings.Join(whitelist, ",")}, "\x00")
			switch {
			case token == "":
				stop()
			case signature != active:
				stop()
				// 轮询只读更新与回执，不发送图文消息，因此不携带防剧透配置。
				client := telegram.NewClient(token, proxy, false, nil)
				if err := client.SetMyCommands(ctx, agent.Commands()); err != nil {
					logging.Error(logging.CategoryNotification, "注册 Telegram 命令菜单失败", "error", err.Error())
				}
				// 复用进程日志格式，让渠道日志与管理页日志保持同一种结构化输出。
				pollerLogger := logging.Default.Slog().With("category", string(logging.CategoryNotification))
				poller := telegram.NewPoller(client, whitelist, handler, pollerLogger)
				pollCtx, cancelFn := context.WithCancel(ctx)
				finished := make(chan struct{})
				cancel, done, active = cancelFn, finished, signature
				go func() {
					defer close(finished)
					if err := poller.Run(pollCtx); err != nil {
						logging.Error(logging.CategoryNotification, "Telegram 长轮询退出", "error", err.Error())
					}
				}()
				logging.Info(logging.CategoryNotification, "Telegram 长轮询已启动")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// parseTelegramWhitelist 解析英文逗号分隔的白名单用户 ID，忽略空白项。
func parseTelegramWhitelist(raw string) []string {
	items := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// weChatCallbackProvider 按当前设置实时解析企业微信回调处理器。
// 回调端点不随保存设置而重启，因此每次请求都重新读取 token、AES Key 与 corpid；
// 未配置时返回 ErrChannelNotConfigured，由 HTTP 层转成 503。
type weChatCallbackProvider struct{ settings *application.SettingsService }

// newWeChatCallback 构造按设置实时解析的企业微信回调处理器。
func newWeChatCallback(settings *application.SettingsService) ports.CallbackVerifier {
	return weChatCallbackProvider{settings: settings}
}

// Verify 实现 ports.CallbackVerifier。
func (p weChatCallbackProvider) Verify(query url.Values) (string, error) {
	callback, err := p.callback()
	if err != nil {
		return "", err
	}
	return callback.Verify(query)
}

// Receive 实现 ports.CallbackVerifier。
func (p weChatCallbackProvider) Receive(query url.Values, body []byte) (ports.InboundMessage, error) {
	callback, err := p.callback()
	if err != nil {
		return ports.InboundMessage{}, err
	}
	return callback.Receive(query, body)
}

// callback 用当前设置构造一次回调处理器；任一必填项缺失都视为未配置。
func (p weChatCallbackProvider) callback() (*wechat.Callback, error) {
	if p.settings == nil {
		return nil, ports.ErrChannelNotConfigured
	}
	values, err := p.settings.Get(context.Background())
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(values.Values["WECHAT_TOKEN"])
	aesKey := strings.TrimSpace(values.Values["WECHAT_ENCODING_AES_KEY"])
	corpID := strings.TrimSpace(values.Values["WECHAT_CORP_ID"])
	if token == "" || aesKey == "" || corpID == "" {
		return nil, ports.ErrChannelNotConfigured
	}
	crypto, err := wechat.NewCrypto(token, aesKey, corpID)
	if err != nil {
		return nil, err
	}
	return wechat.NewCallback(crypto), nil
}
