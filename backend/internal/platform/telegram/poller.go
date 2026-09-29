package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"bytemuse/backend/internal/ports"
)

const (
	// defaultPollTimeout 是单次 getUpdates 的长轮询时长，与 Telegram 服务端保持连接等待新消息。
	defaultPollTimeout = 30 * time.Second
	// defaultBackoff 是 getUpdates 失败后的重试间隔，避免故障时高频打满接口。
	defaultBackoff = 3 * time.Second
	// whitelistRejection 是白名单外用户的固定回复，与旧版行为保持一致。
	whitelistRejection = "只允许白名单用户订阅"
)

// Poller 长轮询 Telegram，把消息与按钮点击交给渠道处理器。
// 回复投递由处理器统一完成（与 HTTP 回调共用同一实现），Poller 只负责协议、白名单与回执。
type Poller struct {
	client    *Client
	whitelist map[string]struct{}
	handler   ports.ChannelMessageHandler
	logger    *slog.Logger
	// backoff 是 getUpdates 失败后的退避时长，默认 3s，测试可注入更短的值。
	backoff time.Duration
}

// NewPoller 绑定客户端、白名单、渠道处理器与日志器。
// 白名单为空表示不限制；logger 为 nil 时回退默认 logger。
func NewPoller(client *Client, whitelist []string, handler ports.ChannelMessageHandler, logger *slog.Logger) *Poller {
	set := make(map[string]struct{}, len(whitelist))
	for _, id := range whitelist {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			set[trimmed] = struct{}{}
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Poller{
		client:    client,
		whitelist: set,
		handler:   handler,
		logger:    logger,
		backoff:   defaultBackoff,
	}
}

// Run 持续长轮询直到 ctx 结束。
// 单条消息的处理错误或 panic 只记录日志，不中断循环；ctx 结束统一返回 nil。
func (p *Poller) Run(ctx context.Context) error {
	if p.client == nil || !p.client.Configured() {
		return errors.New("telegram poller requires a configured client")
	}
	var offset int64
	for {
		if ctx.Err() != nil {
			return nil
		}
		updates, err := p.client.GetUpdates(ctx, offset, defaultPollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			p.logger.Warn("telegram 拉取更新失败，稍后重试", "error", err.Error())
			if !sleepContext(ctx, p.backoff) {
				return nil
			}
			continue
		}
		for _, update := range updates {
			// 先推进 offset 再处理：即使处理失败也不会重复消费同一条消息。
			offset = update.UpdateID + 1
			p.dispatch(ctx, update)
		}
	}
}

// dispatch 处理单条更新：按钮点击先回执以停止客户端加载态，再交给渠道处理器。
func (p *Poller) dispatch(ctx context.Context, update Update) {
	defer func() {
		if recovered := recover(); recovered != nil {
			p.logger.Error("telegram 消息处理发生 panic", "update_id", update.UpdateID, "panic", fmt.Sprint(recovered))
		}
	}()
	if len(p.whitelist) > 0 {
		if _, allowed := p.whitelist[update.UserID]; !allowed {
			if err := p.client.SendText(ctx, update.ChatID, whitelistRejection); err != nil {
				p.logger.Warn("telegram 白名单提示发送失败", "update_id", update.UpdateID, "error", err.Error())
			}
			return
		}
	}
	if p.handler == nil {
		return
	}
	msg := ports.InboundMessage{
		Channel: ports.ChannelTelegram,
		ChatID:  update.ChatID,
		UserID:  update.UserID,
		Text:    update.Text,
		Action:  update.Action,
	}
	if update.Action != nil {
		// 回执越早越好：未回执时 Telegram 客户端会持续显示加载态直到超时。
		if err := p.client.AnswerCallback(ctx, update.Action.CallbackID, ""); err != nil {
			p.logger.Warn("telegram 按钮回执失败", "update_id", update.UpdateID, "error", err.Error())
		}
	} else if err := p.client.SendChatAction(ctx, update.ChatID, "typing"); err != nil {
		p.logger.Warn("telegram 状态上报失败", "update_id", update.UpdateID, "error", err.Error())
	}
	if err := p.handler.Handle(ctx, msg); err != nil {
		p.logger.Error("telegram 消息处理失败", "update_id", update.UpdateID, "error", err.Error())
	}
}

// sleepContext 等待指定时长；ctx 提前结束时返回 false，调用方据此立即退出。
func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
