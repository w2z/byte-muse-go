package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
}

// NewRouter 绑定编排器与业务依赖。
func NewRouter(orchestrator *Orchestrator, deps Deps) *Router {
	return &Router{orchestrator: orchestrator, deps: deps}
}

// Handle 处理一条入站消息并返回要投递的回复。
// 按钮点击走卡片动作分支，不经过 AI；文本消息按斜杠命令、Agent 对话、番号卡片的顺序分流。
// 返回的错误表示渠道或配置层面的失败，调用方应记录日志；空回复表示无需回复。
func (r *Router) Handle(ctx context.Context, msg ports.InboundMessage) (ports.Reply, error) {
	if msg.Action != nil {
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
	// 已入库番号优先返回状态卡片：订阅与下载是确定性业务，不经过 AI，也不消耗模型额度。
	// 未命中的番号保持原有分流（交给 Agent 对话或提示），避免把普通英文文本误判成番号。
	if LooksLikeCode(text) {
		if reply, matched := cardForKnownCode(ctx, r.deps, text); matched {
			return reply, nil
		}
	}
	if r.orchestrator != nil && r.orchestrator.Enabled(ctx) {
		if !r.agentChatEnabled(ctx, msg.Channel) {
			if LooksLikeCode(text) {
				return cardForCode(ctx, r.deps, text), nil
			}
			return ports.Reply{Text: agentChatDisabled}, nil
		}
		reply, err := r.orchestrator.Reply(ctx, msg)
		if err == nil {
			return ports.Reply{Text: reply}, nil
		}
		if !errors.Is(err, ErrAgentDisabled) {
			logging.Error(logging.CategoryAgent, "Agent 对话失败，降级处理", "channel", msg.Channel, "error", err.Error())
			if LooksLikeCode(text) {
				return cardForCode(ctx, r.deps, text), nil
			}
			return ports.Reply{Text: "对话失败：" + err.Error()}, nil
		}
	}
	if LooksLikeCode(text) {
		return cardForCode(ctx, r.deps, text), nil
	}
	return ports.Reply{Text: r.disabledHelp()}, nil
}

// CardFailed 是卡片投递失败时的降级入口：番号消息回到「直接订阅」的原有行为，
// 保证渠道不支持卡片时用户仍能完成订阅，而不是停在一张发不出去的卡片上。
func (r *Router) CardFailed(ctx context.Context, msg ports.InboundMessage) string {
	if LooksLikeCode(msg.Text) {
		return SubscribeByCode(ctx, r.deps, msg.Text)
	}
	return "卡片消息发送失败，请稍后重试。"
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

// downloadSubscriptions 触发一次订阅下载，只报告受理结果。
func (r *Router) downloadSubscriptions(ctx context.Context) (string, error) {
	if r.deps.SubscriptionDownloads == nil {
		return "订阅下载服务不可用", nil
	}
	// 这是批量扫描全部订阅，来源按 schedule 处理：没找到资源不推送，避免一次命令刷出上百条失败通知。
	count, err := r.deps.SubscriptionDownloads.RunActive(ctx)
	if err != nil {
		return "订阅下载执行失败：" + err.Error(), nil
	}
	if count == 0 {
		return "没有需要下载的订阅", nil
	}
	return fmt.Sprintf("已提交 %d 条订阅下载，请在下载页查看进度", count), nil
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
