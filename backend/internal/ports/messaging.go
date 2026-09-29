package ports

import (
	"context"
	"errors"
	"net/url"
)

// ErrChannelNotConfigured 表示渠道未配置，HTTP 层应返回 503 而不是 400。
var ErrChannelNotConfigured = errors.New("channel callback not configured")

// 外部消息渠道标识。值与设置页分组、接口契约保持一致，不新增同义写法。
const (
	ChannelTelegram = "tg"
	ChannelWeChat   = "wx"
)

// InboundMessage 是外部渠道进入业务层的统一消息表示。
// 渠道适配器只负责协议解析与回复投递，业务分流由 application 层完成。
type InboundMessage struct {
	Channel string // 渠道标识，取值见 ChannelTelegram / ChannelWeChat
	ChatID  string // 回复目标；Telegram 为 chat id，企业微信为发送者 userid
	UserID  string // 发送者标识，用于会话隔离；为空时回退 ChatID
	Text    string // 用户原始文本
	// Action 非空表示这是一次按钮点击而不是文本消息；此时 Text 为空。
	Action *InboundAction
}

// SessionKey 返回该消息的会话标识，同一用户在不同渠道互不共享上下文。
func (m InboundMessage) SessionKey() string {
	user := m.UserID
	if user == "" {
		user = m.ChatID
	}
	return m.Channel + ":" + user
}

// ActionButton 是渠道消息上的一个可点击操作按钮。
// Data 是业务层定义的回调载荷，渠道只负责原样回传，不解析其内容。
type ActionButton struct {
	Label string
	Data  string
}

// OutboundCard 是一张带操作按钮的图文卡片：上方封面、下方按钮。
// 渠道按自身能力渲染；按钮无法渲染时 SendCard 必须返回错误。
type OutboundCard struct {
	PhotoURL string
	Title    string
	Text     string
	Buttons  []ActionButton
}

// InboundAction 是用户点击卡片按钮产生的一次动作事件。
// MessageID 与 CallbackID 由渠道填充，仅用于更新原消息与回执，业务层不解析其格式。
type InboundAction struct {
	Data       string
	MessageID  string
	CallbackID string
}

// ButtonRefresh 用于在动作执行后替换原卡片上的按钮，使按钮与最新状态一致。
type ButtonRefresh struct {
	MessageID string
	Buttons   []ActionButton
}

// Reply 是业务层对一次入站消息的回复。
// Card 非空表示发送一张新卡片；Refresh 非空表示更新原消息按钮；Text 为文本内容。
// 三者可组合：卡片失败时调用方只投递 Text，卡片成功时 Text 作为卡片正文由 Card 携带。
type Reply struct {
	Text    string
	Card    *OutboundCard
	Refresh *ButtonRefresh
}

// ChannelSender 是 Agent 与通知向外部渠道发送内容的统一出口。
// 实现方负责各自协议、分片与错误语义；调用方不感知渠道差异。
type ChannelSender interface {
	// SendText 向 chatID 发送纯文本；超长内容由实现方按渠道上限分片。
	SendText(ctx context.Context, chatID, text string) error
	// SendPhoto 发送图文消息；photoURL 为空时降级为纯文本。
	SendPhoto(ctx context.Context, chatID, photoURL, title, text string) error
	// SendCard 发送带操作按钮的图文卡片：上方封面、下方按钮。
	// 渠道无法保证按钮可点击时必须返回错误而不是静默丢弃按钮，调用方据此降级为文本流程。
	SendCard(ctx context.Context, chatID string, card OutboundCard) error
	// ReplaceButtons 用新按钮替换已发送卡片的按钮，使按钮与最新状态一致。
	// 渠道不支持更新卡片时返回 nil（不视为失败），调用方不依赖该调用成功。
	ReplaceButtons(ctx context.Context, chatID, messageID string, buttons []ActionButton) error
}

// ChannelResolver 按渠道名解析当前配置的发送器。
// 对话回复与业务通知共用这一个出口，配置在执行时读取，保存设置后无需重启。
// 渠道未配置或设置不可读时返回 false，调用方负责降级而不是重试。
type ChannelResolver interface {
	Sender(channel string) (ChannelSender, bool)
}

// BotCommand 是一条机器人命令菜单项，由渠道适配器注册到各自平台的命令菜单。
// Command 不含前导斜杠，Description 面向用户展示。
type BotCommand struct {
	Command     string
	Description string
}

// CallbackVerifier 校验并解析外部渠道的 HTTP 回调。
// 实现方负责各自的签名校验与加解密，HTTP 层不感知渠道协议细节。
type CallbackVerifier interface {
	// Verify 校验回调地址所有权，返回应原样回写给平台的明文。
	Verify(query url.Values) (string, error)
	// Receive 校验并解密一次回调请求，返回解析后的入站消息。
	Receive(query url.Values, body []byte) (InboundMessage, error)
}

// ChannelMessageHandler 接收渠道入站消息并完成回复投递。
// HTTP 回调与轮询适配器共用它，业务分流只实现一次。
type ChannelMessageHandler interface {
	Handle(ctx context.Context, msg InboundMessage) error
}
