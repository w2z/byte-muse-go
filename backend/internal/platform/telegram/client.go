// Package telegram 是 Telegram Bot API 的渠道适配层：只负责协议解析与消息投递，
// 业务分流（斜杠命令、番号订阅、Agent 对话）由 application/agent 的 Router 决定。
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

const (
	// defaultBaseURL 是 Telegram 官方 API 根地址，bot token 与 method 在请求时拼接。
	defaultBaseURL = "https://api.telegram.org"
	// defaultRequestTimeout 是除 getUpdates 外所有请求的超时；getUpdates 需覆盖长轮询时长。
	defaultRequestTimeout = 30 * time.Second
	// maxMessageRunes 是 Telegram 单条文本消息的 rune 上限，超出必须分片。
	maxMessageRunes = 4096
	// maxCaptionRunes 是 sendPhoto 的 caption rune 上限，超出必须截断，否则整条卡片发送失败。
	maxCaptionRunes = 1024
	// keyboardButtonsPerRow 是内联键盘每行按钮数，与 Telegram 官方客户端的两列布局一致。
	keyboardButtonsPerRow = 2
	// maxResponseBytes 限制单次响应读取量，避免异常响应拖垮内存。
	maxResponseBytes = 8 << 20
	// maxDescriptionRunes 限制回传的上游错误描述长度，避免把整段响应体暴露出去。
	maxDescriptionRunes = 200
	// photoKindNotification / photoKindCard 是图文消息日志里的来源标签，用于区分推送通知与番号卡片。
	photoKindNotification = "推送通知"
	photoKindCard         = "番号卡片"
)

// Client 是 Telegram Bot API 客户端，实现 ports.ChannelSender。
// baseURL 允许同包测试替换为 httptest 地址，生产固定为官方地址。
// spoiler 对应设置项 TELEGRAM_SPOILER：为真时本客户端发出的所有图文消息封面都按「防剧透」打码，
// 由客户端点击后才显示；推送通知与番号卡片共用这一条规则，不按发送场景分叉。
type Client struct {
	token   string
	baseURL string
	spoiler bool
	client  *http.Client
}

// NewClient 构造 Telegram 客户端。
// httpClient 为 nil 时基于 http.DefaultTransport 克隆自建，proxy 非空才启用 HTTP 代理；
// 传入自定义 httpClient 时由调用方决定代理，本函数只强制禁止跟随重定向。
func NewClient(token, proxy string, spoiler bool, httpClient *http.Client) *Client {
	if httpClient == nil {
		transport, ok := http.DefaultTransport.(*http.Transport)
		var clone *http.Transport
		if ok {
			clone = transport.Clone()
		} else {
			clone = &http.Transport{Proxy: http.ProxyFromEnvironment}
		}
		if trimmed := strings.TrimSpace(proxy); trimmed != "" {
			if parsed, err := url.Parse(trimmed); err == nil {
				clone.Proxy = http.ProxyURL(parsed)
			}
		}
		httpClient = &http.Client{Transport: clone}
	}
	// 复制一份客户端，避免修改调用方持有的实例；重定向一律拒绝。
	clone := *httpClient
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		token:   strings.TrimSpace(token),
		baseURL: strings.TrimRight(defaultBaseURL, "/"),
		spoiler: spoiler,
		client:  &clone,
	}
}

// Configured 表示是否已配置 bot token；未配置时所有请求直接失败，调用方据此决定是否启动轮询。
func (c *Client) Configured() bool {
	return c != nil && c.token != ""
}

// SendText 向 chatID 发送纯文本；超过单条上限时按 rune 分片逐条发送，任一片失败即返回错误。
func (c *Client) SendText(ctx context.Context, chatID, text string) error {
	for _, chunk := range splitMessage(text, maxMessageRunes) {
		payload := map[string]any{"chat_id": chatID, "text": chunk}
		if _, err := c.postJSON(ctx, "sendMessage", payload, defaultRequestTimeout); err != nil {
			return err
		}
	}
	return nil
}

// SendPhoto 发送图文消息；photoURL 为空或 sendPhoto 失败时降级为纯文本，保证用户至少看到内容。
// 封面是否打码由 applySpoiler 统一决定。
func (c *Client) SendPhoto(ctx context.Context, chatID, photoURL, title, text string) error {
	caption := composeCaption(title, text)
	if strings.TrimSpace(photoURL) == "" {
		return c.SendText(ctx, chatID, caption)
	}
	payload := map[string]any{"chat_id": chatID, "photo": photoURL, "caption": caption}
	c.applySpoiler(payload)
	if _, err := c.postJSON(ctx, "sendPhoto", payload, defaultRequestTimeout); err != nil {
		return c.SendText(ctx, chatID, caption)
	}
	c.logPhoto(photoKindNotification)
	return nil
}

// SendNotification 发送带可复制按钮的通知；按钮由 Telegram 原生 copy_text 控件处理。
// 图片说明超限或图文发送失败时保留全文分片发送，复制按钮只附在最后一片。
func (c *Client) SendNotification(ctx context.Context, chatID, title, text, photoURL string, buttons []ports.CopyTextButton) error {
	caption := composeCaption(title, text)
	markup, hasCopyButtons := copyTextKeyboard(buttons)
	if strings.TrimSpace(photoURL) != "" && utf8.RuneCountInString(caption) <= maxCaptionRunes {
		payload := map[string]any{
			"chat_id": chatID,
			"photo":   photoURL,
			"caption": caption,
		}
		if hasCopyButtons {
			payload["reply_markup"] = markup
		}
		c.applySpoiler(payload)
		if _, err := c.postJSON(ctx, "sendPhoto", payload, defaultRequestTimeout); err == nil {
			c.logPhoto(photoKindNotification)
			return nil
		}
	}
	chunks := splitMessage(caption, maxMessageRunes)
	for index, chunk := range chunks {
		payload := map[string]any{"chat_id": chatID, "text": chunk}
		if hasCopyButtons && index == len(chunks)-1 {
			payload["reply_markup"] = markup
		}
		if _, err := c.postJSON(ctx, "sendMessage", payload, defaultRequestTimeout); err != nil {
			return err
		}
	}
	return nil
}

// applySpoiler 按「防剧透」设置给图文消息打码：开启时附带 has_spoiler，
// 封面在客户端先显示为打码状态，用户点击后才展开。
// 纯文本没有可打码的对象，因此只在图片载荷上调用；推送通知与番号卡片共用本方法，
// 机器人发出的封面不会因为发送场景不同而漏打码。
func (c *Client) applySpoiler(payload map[string]any) {
	if c.spoiler {
		payload["has_spoiler"] = true
	}
}

// logPhoto 记录一条图文消息的实际发送形态。
// 发送成功本身不体现封面是否打码，「防剧透」是否生效只能从这条日志核对。
func (c *Client) logPhoto(kind string) {
	logging.Info(logging.CategoryNotification, "Telegram 图文消息已发送", "kind", kind, "spoiler", c.spoiler)
}

// SendCard 发送带内联按钮的图文卡片：上方封面、下方按钮。
// caption 按平台上限截断；封面为空或 sendPhoto 被拒时降级为纯文本消息，但按钮必须保留，
// 因此按钮不可用不会静默退化成普通文本，避免用户在无按钮的消息上等待点击。
func (c *Client) SendCard(ctx context.Context, chatID string, card ports.OutboundCard) error {
	if len(card.Buttons) == 0 {
		return fmt.Errorf("telegram 卡片缺少操作按钮")
	}
	caption := truncateRunes(composeCaption(card.Title, card.Text), maxCaptionRunes)
	markup := inlineKeyboard(card.Buttons)
	if strings.TrimSpace(card.PhotoURL) != "" {
		payload := map[string]any{
			"chat_id":      chatID,
			"photo":        card.PhotoURL,
			"caption":      caption,
			"reply_markup": markup,
		}
		c.applySpoiler(payload)
		if _, err := c.postJSON(ctx, "sendPhoto", payload, defaultRequestTimeout); err == nil {
			c.logPhoto(photoKindCard)
			return nil
		}
	}
	payload := map[string]any{"chat_id": chatID, "text": caption, "reply_markup": markup}
	_, err := c.postJSON(ctx, "sendMessage", payload, defaultRequestTimeout)
	return err
}

// ReplaceButtons 用新按钮替换原消息的内联键盘；buttons 为空表示移除键盘，防止重复点击过期操作。
// messageID 非十进制数字时返回错误，调用方只记录日志，不影响本次回复文本的投递。
func (c *Client) ReplaceButtons(ctx context.Context, chatID, messageID string, buttons []ports.ActionButton) error {
	id, err := strconv.ParseInt(strings.TrimSpace(messageID), 10, 64)
	if err != nil {
		return fmt.Errorf("telegram 消息 ID 无效: %s", messageID)
	}
	markup := map[string]any{"inline_keyboard": [][]map[string]string{}}
	if len(buttons) > 0 {
		markup = inlineKeyboard(buttons)
	}
	payload := map[string]any{"chat_id": chatID, "message_id": id, "reply_markup": markup}
	_, err = c.postJSON(ctx, "editMessageReplyMarkup", payload, defaultRequestTimeout)
	return err
}

// AnswerCallback 回执一次按钮点击，停止客户端加载态；text 为空表示不弹出提示。
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	if strings.TrimSpace(callbackID) == "" {
		return fmt.Errorf("telegram 按钮回执缺少 callback id")
	}
	payload := map[string]any{"callback_query_id": callbackID}
	if trimmed := strings.TrimSpace(text); trimmed != "" {
		payload["text"] = trimmed
	}
	_, err := c.postJSON(ctx, "answerCallbackQuery", payload, defaultRequestTimeout)
	return err
}

// inlineKeyboard 把按钮按每行两个排成内联键盘，最后一个奇数按钮独占一行。
func inlineKeyboard(buttons []ports.ActionButton) map[string]any {
	rows := make([][]map[string]string, 0, (len(buttons)+keyboardButtonsPerRow-1)/keyboardButtonsPerRow)
	for index := 0; index < len(buttons); index += keyboardButtonsPerRow {
		row := make([]map[string]string, 0, keyboardButtonsPerRow)
		for offset := 0; offset < keyboardButtonsPerRow && index+offset < len(buttons); offset++ {
			button := buttons[index+offset]
			row = append(row, map[string]string{"text": button.Label, "callback_data": button.Data})
		}
		rows = append(rows, row)
	}
	return map[string]any{"inline_keyboard": rows}
}

// copyTextKeyboard 使用原生复制按钮；超过平台 256 字符上限的内容不生成按钮，不截断复制地址。
func copyTextKeyboard(buttons []ports.CopyTextButton) (map[string]any, bool) {
	row := make([]map[string]any, 0, len(buttons))
	for _, button := range buttons {
		label := strings.TrimSpace(button.Label)
		text := strings.TrimSpace(button.Text)
		if label == "" || text == "" || utf8.RuneCountInString(text) > 256 {
			continue
		}
		row = append(row, map[string]any{
			"text":      label,
			"copy_text": map[string]string{"text": text},
		})
	}
	return map[string]any{"inline_keyboard": [][]map[string]any{row}}, len(row) > 0
}

// SendChatAction 上报聊天状态（如 typing），用于长任务前的即时反馈；action 为空时回退 typing。
func (c *Client) SendChatAction(ctx context.Context, chatID, action string) error {
	if strings.TrimSpace(action) == "" {
		action = "typing"
	}
	payload := map[string]any{"chat_id": chatID, "action": action}
	_, err := c.postJSON(ctx, "sendChatAction", payload, defaultRequestTimeout)
	return err
}

// SetMyCommands 注册机器人命令菜单；空命令列表不发起请求，避免误清空已有菜单。
func (c *Client) SetMyCommands(ctx context.Context, commands []ports.BotCommand) error {
	items := make([]map[string]string, 0, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(command.Command)
		if name == "" {
			continue
		}
		items = append(items, map[string]string{
			"command":     name,
			"description": strings.TrimSpace(command.Description),
		})
	}
	if len(items) == 0 {
		return nil
	}
	payload := map[string]any{"commands": items}
	_, err := c.postJSON(ctx, "setMyCommands", payload, defaultRequestTimeout)
	return err
}

// GetUpdates 拉取入站更新。
// 只保留 message 类型且 text 非空的更新；offset 由 Telegram 用于确认消费进度，
// 调用方必须在处理消息前推进 offset，否则重启后会重复投递同一条消息。
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	if timeout < 0 {
		timeout = 0
	}
	payload := map[string]any{
		"offset":          offset,
		"timeout":         int64(timeout / time.Second),
		"allowed_updates": []string{"message", "callback_query"},
	}
	raw, err := c.postJSON(ctx, "getUpdates", payload, timeout+10*time.Second)
	if err != nil {
		return nil, err
	}
	return decodeUpdates(raw)
}

// Update 是解析后的入站更新。ChatID/UserID 使用十进制字符串，避免与平台整型差异互相污染。
// Action 非空表示这是一次内联按钮点击，此时 Text 为空。
type Update struct {
	UpdateID int64
	ChatID   string
	UserID   string
	Text     string
	Action   *ports.InboundAction
}

// endpoint 拼接 Bot API 地址，形如 <base>/bot<token>/<method>。
func (c *Client) endpoint(method string) string {
	return c.baseURL + "/bot" + c.token + "/" + method
}

// postJSON 执行一次 Bot API 调用并返回 result 原始 JSON。
// 只接受 HTTP 2xx 且 ok=true；失败时只回传被截断的上游描述，不暴露完整响应体。
func (c *Client) postJSON(ctx context.Context, method string, payload any, timeout time.Duration) (json.RawMessage, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("telegram bot token is not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram %s HTTP %d", method, response.StatusCode)
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("telegram %s response is not valid JSON", method)
	}
	if !envelope.OK {
		if description := sanitizeDescription(envelope.Description); description != "" {
			return nil, fmt.Errorf("telegram %s rejected: %s", method, description)
		}
		return nil, fmt.Errorf("telegram %s rejected", method)
	}
	return envelope.Result, nil
}

// decodeUpdates 从 getUpdates 的 result 中提取可处理更新：
// 文本消息解析为 Text，内联按钮点击解析为 Action，其余类型（无文本消息、无消息来源的回调等）跳过。
func decodeUpdates(raw json.RawMessage) ([]Update, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var items []struct {
		UpdateID int64 `json:"update_id"`
		Message  *struct {
			Text string `json:"text"`
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			From *struct {
				ID int64 `json:"id"`
			} `json:"from"`
		} `json:"message"`
		CallbackQuery *struct {
			ID   string `json:"id"`
			Data string `json:"data"`
			From *struct {
				ID int64 `json:"id"`
			} `json:"from"`
			Message *struct {
				MessageID int64 `json:"message_id"`
				Chat      struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"callback_query"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("telegram getUpdates result is not a valid list")
	}
	updates := make([]Update, 0, len(items))
	for _, item := range items {
		if query := item.CallbackQuery; query != nil && strings.TrimSpace(query.Data) != "" && query.Message != nil {
			userID := ""
			if query.From != nil {
				userID = strconv.FormatInt(query.From.ID, 10)
			}
			updates = append(updates, Update{
				UpdateID: item.UpdateID,
				ChatID:   strconv.FormatInt(query.Message.Chat.ID, 10),
				UserID:   userID,
				Action: &ports.InboundAction{
					Data:       query.Data,
					MessageID:  strconv.FormatInt(query.Message.MessageID, 10),
					CallbackID: query.ID,
				},
			})
			continue
		}
		if item.Message == nil || strings.TrimSpace(item.Message.Text) == "" {
			continue
		}
		userID := ""
		if item.Message.From != nil {
			userID = strconv.FormatInt(item.Message.From.ID, 10)
		}
		updates = append(updates, Update{
			UpdateID: item.UpdateID,
			ChatID:   strconv.FormatInt(item.Message.Chat.ID, 10),
			UserID:   userID,
			Text:     item.Message.Text,
		})
	}
	return updates, nil
}

// truncateRunes 按 rune 上限截断文本，超出时以省略号结尾，保证不切断多字节字符。
func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

// splitMessage 按 rune 上限切分文本，保证不切断多字节字符；空串返回 nil 表示无需发送。
func splitMessage(text string, limit int) []string {
	if text == "" || limit <= 0 {
		return nil
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	chunks := make([]string, 0, (len(runes)+limit-1)/limit)
	for start := 0; start < len(runes); start += limit {
		end := start + limit
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

// composeCaption 组合图文标题与正文；两者都存在时用换行分隔，便于阅读。
func composeCaption(title, text string) string {
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

// sanitizeDescription 截断上游错误描述，只保留可读原因，不携带完整响应体。
func sanitizeDescription(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return ""
	}
	if runes := []rune(description); len(runes) > maxDescriptionRunes {
		return string(runes[:maxDescriptionRunes])
	}
	return description
}
