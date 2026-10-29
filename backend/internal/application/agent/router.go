package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// maxCommandSubscriptions 是斜杠命令展示订阅的上限，避免消息过长被渠道截断。
const maxCommandSubscriptions = 50

// agentChatDisabled 是该渠道关闭「Agent 对话」时的指引；斜杠命令与番号订阅不受该开关影响。
const agentChatDisabled = "当前渠道已关闭「Agent 对话」，请在设置页「消息渠道」中开启后再提问。\n\n直接发送番号仍会返回影片卡片。"

// Commands 返回全部斜杠命令，顺序即菜单顺序。
// Telegram 用同一份定义注册机器人菜单，两个渠道共享命令语义。
func Commands() []ports.BotCommand {
	return []ports.BotCommand{
		{Command: "start", Description: "获取欢迎信息"},
		{Command: "subscribe", Description: "获取当前订阅列表"},
		{Command: "download_subscribe", Description: "下载订阅"},
		{Command: "version", Description: "查看当前版本"},
		{Command: "clear", Description: "清空对话上下文"},
		{Command: "help", Description: "使用帮助"},
	}
}

// Router 是外部消息渠道的统一业务入口：斜杠命令、番号订阅和 Agent 对话都在这里分流。
// 渠道适配器只负责协议与投递，不重复实现业务规则。
type Router struct {
	orchestrator *Orchestrator
	deps         Deps
	mu           sync.Mutex
	results      map[string]mediaResultSet
}

// NewRouter 绑定编排器与业务依赖。
func NewRouter(orchestrator *Orchestrator, deps Deps) *Router {
	return &Router{orchestrator: orchestrator, deps: deps, results: make(map[string]mediaResultSet)}
}

// Handle 处理一条入站消息并返回要投递的回复。
// 按钮点击与明确番号不经过 AI；文本消息优先处理斜杠命令和番号，再进入 Agent 对话。
// 返回的错误表示渠道或配置层面的失败，调用方应记录日志；空回复表示无需回复。
func (r *Router) Handle(ctx context.Context, msg ports.InboundMessage) (ports.Reply, error) {
	if msg.Action != nil {
		if strings.HasPrefix(msg.Action.Data, "bm:list:") {
			return r.handleMediaResultAction(ctx, msg), nil
		}
		return handleCardAction(ctx, r.deps, *msg.Action), nil
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return ports.Reply{}, nil
	}
	if strings.HasPrefix(text, "/") {
		reply, err := r.command(ctx, msg, text)
		return ports.Reply{Text: reply}, err
	}
	// 番号优先通过统一服务查询本地及远程资料，不消耗模型额度。
	// 未装配远程服务的调用方保留本地卡片与原有对话降级行为。
	if domain.LooksLikeCode(text) {
		if r.deps.Codes != nil {
			return cardForCode(ctx, r.deps, text), nil
		}
		if reply, matched := cardForKnownCode(ctx, r.deps, text); matched {
			return reply, nil
		}
	}
	if r.orchestrator != nil && r.orchestrator.Enabled(ctx) {
		if !r.agentChatEnabled(ctx, msg.Channel) {
			if domain.LooksLikeCode(text) {
				return cardForCode(ctx, r.deps, text), nil
			}
			return ports.Reply{Text: agentChatDisabled}, nil
		}
		collected := &mediaResultCollector{}
		ctx = context.WithValue(ctx, mediaResultsKey{}, collected)
		reply, err := r.orchestrator.Reply(ctx, msg)
		if err == nil {
			if len(collected.codes) > 0 {
				if card := r.newMediaResult(ctx, msg, collected); card.Card != nil || len(card.Cards) > 0 {
					card.FollowUp = reply
					if collected.incomplete {
						card.FollowUp += "\n外站只查询了第一页，结果可能不完整。"
					}
					if msg.Channel != ports.ChannelTelegram && collected.total > len(collected.codes) {
						card.FollowUp += fmt.Sprintf("\n共 %d 条，本次展示前 %d 条。", collected.total, len(collected.codes))
					}
					return card, nil
				}
			}
			return ports.Reply{Text: reply}, nil
		}
		if !errors.Is(err, ErrAgentDisabled) {
			logging.Error(logging.CategoryAgent, "Agent 对话失败，降级处理", "channel", msg.Channel, "error", err.Error())
			if domain.LooksLikeCode(text) {
				return cardForCode(ctx, r.deps, text), nil
			}
			return ports.Reply{Text: "对话失败：" + err.Error()}, nil
		}
	}
	if domain.LooksLikeCode(text) {
		return cardForCode(ctx, r.deps, text), nil
	}
	return ports.Reply{Text: r.disabledHelp()}, nil
}

// mediaResultCollector 保存工具返回的本地影片番号，不解析模型生成的文本。
type mediaResultsKey struct{}
type mediaResultCollector struct {
	codes      []string
	total      int
	suppress   bool
	external   map[string]application.ExternalMedia
	incomplete bool
	queryName  string
	queryArgs  map[string]any
}

// mediaResultSet 绑定结果与原渠道、会话，限制按钮有效期和内存占用。
type mediaResultSet struct {
	owner     string
	codes     []string
	total     int
	external  map[string]application.ExternalMedia
	created   time.Time
	queryName string
	queryArgs map[string]any
	page      int
}

func mediaResultOwner(msg ports.InboundMessage) string {
	return msg.Channel + "\x00" + msg.ChatID + "\x00" + msg.UserID
}

func collectMediaResults(ctx context.Context, name string, args map[string]any, result any) {
	collector, ok := ctx.Value(mediaResultsKey{}).(*mediaResultCollector)
	if !ok {
		return
	}
	switch name {
	case "add_subscribe", "cancel_subscribe", "add_actor_subscribe", "cancel_actor_subscribe", "add_tag_subscribe", "cancel_tag_subscribe", "download_subscribe", "run_cron_job", "send_message":
		collector.codes = nil
		collector.suppress = true
		return
	}
	if collector.suppress {
		return
	}
	switch name {
	case "search_media", "search_code", "get_rank", "get_release_today", "get_recommendations", "search_category_media":
	default:
		return
	}
	collector.codes = nil
	collector.external = nil
	collector.incomplete = false
	collector.queryName = ""
	data, ok := result.(map[string]any)
	if !ok {
		return
	}
	if name == "search_code" {
		media := data
		if nested, ok := data["media"].(map[string]any); ok {
			media = nested
		}
		if code, ok := media["code"].(string); ok && domain.NormalizeCode(code) != "" {
			collector.codes = []string{domain.NormalizeCode(code)}
			collector.total = 1
		}
		return
	}
	items, ok := data["items"].([]map[string]any)
	if !ok {
		if external, ok := data["external_media"].([]application.ExternalMedia); ok && len(external) > 0 {
			collector.incomplete, _ = data["external_incomplete"].(bool)
			collector.codes = nil
			collector.external = make(map[string]application.ExternalMedia)
			for _, item := range external {
				code := domain.NormalizeCode(item.Code)
				if code == "" {
					continue
				}
				if _, duplicate := collector.external[code]; duplicate {
					continue
				}
				collector.external[code] = item
				collector.codes = append(collector.codes, code)
			}
			collector.total = len(collector.codes)
		}
		return
	}
	codes := make([]string, 0, len(items))
	for _, item := range items {
		if code, ok := item["code"].(string); ok && domain.NormalizeCode(code) != "" {
			codes = append(codes, domain.NormalizeCode(code))
		}
	}
	collector.codes = codes
	collector.total, _ = data["total"].(int)
	switch name {
	case "search_media", "search_category_media", "get_rank", "get_release_today":
		collector.queryName, collector.queryArgs = name, args
	}
}

func (r *Router) newMediaResult(ctx context.Context, msg ports.InboundMessage, collected *mediaResultCollector) ports.Reply {
	codes, total := collected.codes, collected.total
	if msg.Channel != ports.ChannelTelegram {
		result := ports.Reply{}
		for index := range codes {
			set := mediaResultSet{codes: codes, total: total, external: collected.external}
			if item := r.mediaResultCard(ctx, set, "", index); item.Card != nil {
				// 非 Telegram 渠道使用原有的番号订阅动作。
				for i, button := range item.Card.Buttons {
					if _, _, ok := parseCardData(button.Data); !ok {
						item.Card.Buttons[i].Data = cardData(cardActionSubscribe, codes[index])
					}
				}
				result.Cards = append(result.Cards, *item.Card)
			}
		}
		return result
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ports.Reply{}
	}
	token := hex.EncodeToString(random[:])
	set := mediaResultSet{owner: mediaResultOwner(msg), codes: append([]string(nil), codes...), total: total, external: collected.external, created: time.Now(), queryName: collected.queryName, queryArgs: collected.queryArgs, page: 1}
	r.mu.Lock()
	for key, value := range r.results {
		if time.Since(value.created) > sessionTTL {
			delete(r.results, key)
		}
	}
	if len(r.results) >= 1000 {
		var oldest string
		var when time.Time
		for key, value := range r.results {
			if oldest == "" || value.created.Before(when) {
				oldest, when = key, value.created
			}
		}
		delete(r.results, oldest)
	}
	r.results[token] = set
	r.mu.Unlock()
	return r.mediaResultCard(ctx, set, token, 0)
}

func (r *Router) mediaResultCard(ctx context.Context, set mediaResultSet, token string, index int) ports.Reply {
	if index < 0 || index >= len(set.codes) {
		return ports.Reply{Text: "结果已失效，请重新查询"}
	}
	media, found := lookupMedia(ctx, r.deps, set.codes[index])
	if !found {
		item, ok := set.external[set.codes[index]]
		if !ok {
			return ports.Reply{Text: "影片资料不可用，请重新查询"}
		}
		title := item.Title
		if item.TranslatedTitle != nil && strings.TrimSpace(*item.TranslatedTitle) != "" {
			title = *item.TranslatedTitle
		}
		card := &ports.OutboundCard{Title: set.codes[index], Text: title + "\n外站资料，尚未入库", PhotoURL: item.PosterURL, Buttons: []ports.ActionButton{{Label: "订阅", Data: cardData(cardActionSubscribe, set.codes[index])}}}
		return r.decorateMediaResult(ports.Reply{Card: card, Text: card.Title + "\n" + card.Text}, set, token, index)
	}
	reply := mediaCard(ctx, r.deps, media)
	return r.decorateMediaResult(reply, set, token, index)
}

// decorateMediaResult 复用原卡片动作，并为 Telegram 查询结果绑定受限的翻页回调。
func (r *Router) decorateMediaResult(reply ports.Reply, set mediaResultSet, token string, index int) ports.Reply {
	if reply.Card == nil {
		return reply
	}
	if token == "" {
		return reply
	}
	for i, button := range reply.Card.Buttons {
		name, _, ok := parseCardData(button.Data)
		if ok {
			reply.Card.Buttons[i].Data = fmt.Sprintf("bm:list:%s:%d:%s", token, index, name)
		}
	}
	if len(set.codes) > 1 || (set.queryName != "" && len(set.codes) < set.total) {
		reply.Card.Text += fmt.Sprintf("\n%d / %d", index+1, len(set.codes))
		if set.total > len(set.codes) {
			reply.Card.Text = strings.TrimSuffix(reply.Card.Text, fmt.Sprintf("%d / %d", index+1, len(set.codes))) + fmt.Sprintf("%d / %d", index+1, set.total)
		}
		if index > 0 {
			reply.Card.Buttons = append(reply.Card.Buttons, ports.ActionButton{Label: "◀ 上一张", Data: fmt.Sprintf("bm:list:%s:%d:show", token, index-1), NewRow: true})
		}
		if index+1 < len(set.codes) || (set.queryName != "" && len(set.codes) < set.total) {
			reply.Card.Buttons = append(reply.Card.Buttons, ports.ActionButton{Label: "下一张 ▶", Data: fmt.Sprintf("bm:list:%s:%d:show", token, index+1), NewRow: index == 0})
		}
	}
	return reply
}

func (r *Router) handleMediaResultAction(ctx context.Context, msg ports.InboundMessage) ports.Reply {
	parts := strings.Split(msg.Action.Data, ":")
	if len(parts) != 5 {
		return ports.Reply{Text: "结果已失效，请重新查询"}
	}
	index, err := strconv.Atoi(parts[3])
	r.mu.Lock()
	set, ok := r.results[parts[2]]
	r.mu.Unlock()
	if err != nil || !ok || time.Since(set.created) > sessionTTL || set.owner != mediaResultOwner(msg) || index < 0 {
		return ports.Reply{Text: "结果已失效，请重新查询"}
	}
	if parts[4] == "show" {
		if index == len(set.codes) && set.queryName != "" && len(set.codes) < set.total {
			set, err = r.loadMoreMediaResults(ctx, parts[2], set)
			if err != nil {
				return ports.Reply{Text: "加载更多影片失败，请稍后重试"}
			}
		}
		reply := r.mediaResultCard(ctx, set, parts[2], index)
		reply.EditMessageID = msg.Action.MessageID
		reply.EditHasPhoto = msg.Action.HasPhoto
		return reply
	}
	if index >= len(set.codes) {
		return ports.Reply{Text: "结果已失效，请重新查询"}
	}
	if !cardActionKnown(parts[4]) {
		return ports.Reply{Text: cardActionExpired}
	}
	text := runCardAction(ctx, r.deps, parts[4], set.codes[index])
	reply := r.mediaResultCard(ctx, set, parts[2], index)
	if reply.Card != nil {
		reply.Refresh = &ports.ButtonRefresh{MessageID: msg.Action.MessageID, Buttons: reply.Card.Buttons}
	}
	reply.Card = nil
	reply.Text = text
	return reply
}

// loadMoreMediaResults 按原查询逐页加载，错误时保留已有结果并允许重试。
func (r *Router) loadMoreMediaResults(ctx context.Context, token string, set mediaResultSet) (mediaResultSet, error) {
	pageNumber := set.page + 1
	var page application.Page[domain.Media]
	var err error
	switch set.queryName {
	case "search_media":
		page, err = r.deps.Queries.Search(ctx, stringArg(set.queryArgs, "keyword"), pageNumber, mediaSearchLimit)
	case "search_category_media":
		if tag := stringArg(set.queryArgs, "tag"); tag != "" {
			page, err = r.deps.Tags.SearchMedia(ctx, tag, pageNumber, mediaSearchLimit)
		} else {
			page, err = r.deps.Catalog.List(ctx, pageNumber, mediaSearchLimit, ports.MediaListQuery{VideoType: stringArg(set.queryArgs, "video_type")})
		}
	case "get_rank":
		page, err = r.deps.Queries.Rank(ctx, normalizeRankType(stringArg(set.queryArgs, "type")), pageNumber, mediaSearchLimit, ports.MediaListQuery{})
	case "get_release_today":
		page, err = r.deps.Queries.ReleaseToday(ctx, pageNumber, mediaSearchLimit, ports.MediaListQuery{})
	default:
		return set, errors.New("不支持分页")
	}
	if err != nil {
		return set, err
	}
	codes := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		if code := domain.NormalizeCode(item.Code); code != "" {
			codes = append(codes, code)
		}
	}
	if len(codes) == 0 {
		return set, errors.New("没有更多有效影片")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.results[token]
	if !ok || current.created != set.created {
		return set, errors.New("查询已失效")
	}
	if current.page == set.page {
		current.codes = append(append([]string(nil), current.codes...), codes...)
		current.page = pageNumber
		current.total = page.Total
		r.results[token] = current
	}
	return current, nil
}

// agentChatEnabled 报告该渠道是否允许自然语言对话。
// 未注入通知服务时直接放行，保持渠道对话的历史行为不变。
func (r *Router) agentChatEnabled(ctx context.Context, channel string) bool {
	if r.deps.Notifier == nil {
		return true
	}
	return r.deps.Notifier.ChannelEventEnabled(ctx, channel, application.NotificationAgentChat)
}

// command 执行斜杠命令；未知命令返回可用命令列表，不静默忽略。
func (r *Router) command(ctx context.Context, msg ports.InboundMessage, text string) (string, error) {
	fields := strings.Fields(text)
	name := strings.TrimPrefix(fields[0], "/")
	if index := strings.Index(name, "@"); index >= 0 {
		name = name[:index]
	}
	switch name {
	case "start":
		return r.welcomeText(ctx), nil
	case "help":
		return r.helpText(), nil
	case "subscribe":
		return r.subscriptionList(ctx)
	case "download_subscribe":
		return r.downloadSubscriptions(ctx)
	case "version":
		return "当前版本：" + versionOf(r.deps), nil
	case "clear":
		if r.orchestrator != nil {
			r.orchestrator.ClearSession(msg.SessionKey())
		}
		return "当前对话上下文已清空", nil
	default:
		return "未知命令：/" + name + "\n\n" + r.helpText(), nil
	}
}

// subscriptionList 输出当前有效订阅。
func (r *Router) subscriptionList(ctx context.Context) (string, error) {
	if r.deps.Subscriptions == nil {
		return "订阅服务不可用", nil
	}
	page, err := r.deps.Subscriptions.List(ctx, 1, maxCommandSubscriptions, domain.SubscriptionStatusActive)
	if err != nil {
		return "订阅查询失败：" + err.Error(), nil
	}
	if len(page.Items) == 0 {
		return "当前没有订阅", nil
	}
	lines := make([]string, 0, len(page.Items)+2)
	lines = append(lines, fmt.Sprintf("当前订阅 %d 条：", page.Total))
	for index, item := range page.Items {
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, subscriptionLabel(item)))
	}
	if page.Total > len(page.Items) {
		lines = append(lines, fmt.Sprintf("（仅显示前 %d 条）", len(page.Items)))
	}
	return strings.Join(lines, "\n"), nil
}

// downloadSubscriptions 触发一次订阅资源搜索，只报告受理结果。
func (r *Router) downloadSubscriptions(ctx context.Context) (string, error) {
	if r.deps.SubscriptionDownloads == nil {
		return "订阅下载服务不可用", nil
	}
	// 这是批量扫描全部订阅，来源按 schedule 处理：没找到资源不推送，避免一次命令刷出上百条失败通知。
	count, err := r.deps.SubscriptionDownloads.RunActiveScans(ctx)
	if err != nil {
		return "订阅下载执行失败：" + err.Error(), nil
	}
	if count == 0 {
		return "没有需要搜索的订阅", nil
	}
	return fmt.Sprintf("已登记 %d 条订阅搜索，有资源才会建立下载任务", count), nil
}

// subscriptionLabel 生成订阅的一行展示，优先使用译文标题。
func subscriptionLabel(item domain.Subscription) string {
	if item.Media == nil {
		return item.MediaID
	}
	code := strings.TrimSpace(item.Media.Code)
	title := strings.TrimSpace(item.Media.Title)
	if item.Media.TranslatedTitle != nil && strings.TrimSpace(*item.Media.TranslatedTitle) != "" {
		title = strings.TrimSpace(*item.Media.TranslatedTitle)
	}
	if code == "" {
		return title
	}
	if title == "" {
		return code
	}
	return code + " " + title
}

// welcomeText 是 /start 的欢迎信息：说明能做什么并列出可用命令。
// Agent 是否可用取决于当前配置，这里读取实时配置，避免给出做不到的指引。
func (r *Router) welcomeText(ctx context.Context) string {
	lines := []string{
		"欢迎使用 ByteMuse 助手！",
		"",
		"直接发送番号即可查看影片信息，卡片上的按钮可直接订阅或管理下载，例如 SSIS-001。",
	}
	if r.orchestrator != nil && r.orchestrator.Enabled(ctx) {
		lines = append(lines, "也可以直接用中文提问，例如：今天有什么新片、查看下载进度。")
	} else {
		lines = append(lines, "在设置页启用「对话 Agent」后，还可以直接用中文提问。")
	}
	lines = append(lines, "", "斜杠命令：")
	for _, command := range Commands() {
		lines = append(lines, "/"+command.Command+" - "+command.Description)
	}
	return strings.Join(lines, "\n")
}

// helpText 是 /help 的使用帮助；与欢迎信息分开，避免 /start 只回一份命令清单。
func (r *Router) helpText() string {
	return strings.Join([]string{
		"ByteMuse 助手使用帮助：",
		"",
		"1. 直接发送番号返回影片卡片，点按钮即可订阅、取消订阅或暂停下载，例如 SSIS-001",
		"2. 使用 /start 查看欢迎信息",
		"3. 使用 /subscribe 查看当前订阅列表",
		"4. 使用 /download_subscribe 立即下载全部订阅",
		"5. 使用 /version 查看当前版本",
		"6. 使用 /clear 清空对话上下文",
		"7. 直接用中文提问即可与 Agent 对话（需在设置页启用）",
	}, "\n")
}

// disabledHelp 在 Agent 未启用且输入不是番号时给出可执行指引。
func (r *Router) disabledHelp() string {
	return "直接发送番号即可查看影片并订阅。\n\n如需自然语言对话，请在设置页启用「对话 Agent」并填写 OpenAI 接口地址、模型和 API Key。"
}

// versionOf 返回运行版本；未注入时回退 dev。
func versionOf(deps Deps) string {
	if strings.TrimSpace(deps.Version) == "" {
		return "dev"
	}
	return strings.TrimSpace(deps.Version)
}
