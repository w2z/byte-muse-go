package agent

import (
	"context"
	"fmt"
	"strings"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// Dispatcher 是渠道适配器的统一回调：处理一条入站消息并把回复投递回原渠道。
// Telegram 轮询与企业微信 HTTP 回调共用它，业务分流与回复投递都只实现一次。
type Dispatcher struct {
	router   *Router
	channels ChannelResolver
}

// NewDispatcher 绑定路由与渠道解析器。
func NewDispatcher(router *Router, channels ChannelResolver) *Dispatcher {
	return &Dispatcher{router: router, channels: channels}
}

// Handle 处理一条入站消息并按回复形态投递：卡片、按钮刷新或文本，空回复表示无需回应。
// 卡片投递失败时降级为文本并交由 Router 决定降级内容，避免用户停在无法点击的卡片上。
func (d *Dispatcher) Handle(ctx context.Context, msg ports.InboundMessage) error {
	if d.router == nil {
		return nil
	}
	reply, err := d.router.Handle(ctx, msg)
	if err != nil {
		return err
	}
	if d.channels == nil {
		return fmt.Errorf("渠道 %s 未配置，无法发送回复", msg.Channel)
	}
	sender, ok := d.channels.Sender(msg.Channel)
	if !ok {
		return fmt.Errorf("渠道 %s 未配置，无法发送回复", msg.Channel)
	}
	if reply.Card != nil {
		if err := sender.SendCard(ctx, msg.ChatID, *reply.Card); err != nil {
			logging.Error(logging.CategoryAgent, "渠道卡片发送失败，降级为文本", "channel", msg.Channel, "error", err.Error())
			fallback := strings.TrimSpace(d.router.CardFailed(ctx, msg))
			if fallback == "" {
				return nil
			}
			return sender.SendText(ctx, msg.ChatID, fallback)
		}
		return nil
	}
	// 按钮刷新是尽力而为：更新失败不影响本次结果文本的投递。
	if reply.Refresh != nil && strings.TrimSpace(reply.Refresh.MessageID) != "" {
		if err := sender.ReplaceButtons(ctx, msg.ChatID, reply.Refresh.MessageID, reply.Refresh.Buttons); err != nil {
			logging.Error(logging.CategoryAgent, "渠道卡片按钮更新失败", "channel", msg.Channel, "error", err.Error())
		}
	}
	text := strings.TrimSpace(reply.Text)
	if text == "" {
		return nil
	}
	return sender.SendText(ctx, msg.ChatID, text)
}
