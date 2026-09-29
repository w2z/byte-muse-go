// Package wechat 实现企业微信渠道适配：应用消息发送、回调签名校验与消息解密。
// 业务分流（斜杠命令、番号订阅、Agent 对话）由 internal/application/agent 负责，本包只处理协议与投递。
package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"bytemuse/backend/internal/ports"
)

const (
	// defaultAPIHost 是企业微信官方接口基址，apiHost 为空时使用。
	defaultAPIHost = "https://qyapi.weixin.qq.com"
	// tokenLifetimeSeconds 是 access_token 的官方有效期。
	tokenLifetimeSeconds = 7200
	// tokenRefreshMarginSeconds 让缓存的 token 在官方过期前提前失效，避免临界期内调用失败。
	tokenRefreshMarginSeconds = 300
	// maxTextBytes 是企业微信文本消息的内容上限（字节），超长内容按 rune 边界分片。
	maxTextBytes = 2048
	// maxCardButtons 是模板卡片（按钮交互型）的按钮数量上限，超出平台会拒绝整张卡片。
	maxCardButtons = 6
	// cardAspectRatio 是模板卡片封面的宽高比。
	cardAspectRatio = 2.25
	// cardSourceDesc 是模板卡片左上角的来源文案。
	cardSourceDesc = "ByteMuse"
	// maxCardTitleRunes/maxCardDescRunes 按平台文案长度保守截断，避免整张卡片被拒绝。
	maxCardTitleRunes = 64
	maxCardDescRunes  = 120
	// maxResponseBytes 限制第三方响应体读取量，异常响应不会耗尽内存。
	maxResponseBytes = 1 << 20
	// requestTimeout 是单次企业微信请求的超时时间。
	requestTimeout = 30 * time.Second
	// maxTokenAttempts 允许在 token 失效时刷新一次凭证后重发，避免整段有效期内的持续失败。
	maxTokenAttempts = 2
)

// errNotConfigured 表示企业微信渠道未配置，调用方应跳过该渠道而不是重试。
var errNotConfigured = errors.New("企业微信渠道未配置")

// Client 是企业微信应用消息客户端，实现 ports.ChannelSender。
// photoURL 对应设置项 WECHAT_PHOTO（固定配图），banner 对应 WECHAT_BANNER（是否优先使用本次消息封面）。
type Client struct {
	corpID     string
	corpSecret string
	agentID    string
	apiHost    string
	photoURL   string
	banner     bool
	client     *http.Client

	// tokenMu 保护 access_token 缓存；并发首次获取只发起一次 gettoken 请求。
	tokenMu     sync.Mutex
	token       string
	tokenExpiry time.Time
}

var _ ports.ChannelSender = (*Client)(nil)

// NewClient 构造企业微信客户端，不发起网络请求。
// apiHost 为空时使用官方基址；传入的 httpClient 会被复制并强制不跟随重定向。
func NewClient(corpID, corpSecret, agentID, apiHost, photoURL string, banner bool, httpClient *http.Client) *Client {
	host := strings.TrimSpace(apiHost)
	if host == "" {
		host = defaultAPIHost
	}
	host = strings.TrimRight(host, "/")
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	clone := *httpClient
	if clone.Timeout <= 0 {
		clone.Timeout = requestTimeout
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		corpID:     strings.TrimSpace(corpID),
		corpSecret: strings.TrimSpace(corpSecret),
		agentID:    strings.TrimSpace(agentID),
		apiHost:    host,
		photoURL:   strings.TrimSpace(photoURL),
		banner:     banner,
		client:     &clone,
	}
}

// Configured 表示 corpid、secret、agentid 是否齐备；未配置时发送类方法直接报错且不发请求。
func (c *Client) Configured() bool {
	return c != nil && c.corpID != "" && c.corpSecret != "" && c.agentID != ""
}

// SendText 发送纯文本消息；超过渠道上限的内容按 rune 边界分片逐条发送。
// 任一分片失败即返回错误，已发送的分片不会回滚。
func (c *Client) SendText(ctx context.Context, chatID, text string) error {
	if !c.Configured() {
		return errNotConfigured
	}
	target := strings.TrimSpace(chatID)
	if target == "" {
		return errors.New("企业微信接收者不能为空")
	}
	for _, chunk := range splitText(text, maxTextBytes) {
		payload := sendPayload{
			ToUser:  target,
			MsgType: "text",
			AgentID: c.agentID,
			Text:    &textBody{Content: chunk},
		}
		if err := c.sendMessage(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}

// SendPhoto 发送图文消息；picurl 取值与对标站一致，为空时降级为纯文本。
// banner 为真时优先使用本次传入的封面，否则使用构造时的固定配图。
func (c *Client) SendPhoto(ctx context.Context, chatID, photoURL, title, text string) error {
	if !c.Configured() {
		return errNotConfigured
	}
	picURL := c.photoURL
	if c.banner {
		picURL = strings.TrimSpace(photoURL)
	}
	if picURL == "" {
		return c.SendText(ctx, chatID, joinTitleText(title, text))
	}
	payload := sendPayload{
		ToUser:  strings.TrimSpace(chatID),
		MsgType: "news",
		AgentID: c.agentID,
		News: &newsBody{
			Articles: []newsArticle{{
				Title:       title,
				Description: text,
				URL:         "",
				PicURL:      picURL,
			}},
		},
	}
	return c.sendMessage(ctx, payload)
}

// SendCard 用模板卡片（按钮交互型）发送封面与操作按钮，按钮点击由企业微信回调事件送回。
// 企业微信要求卡片必须带 1-6 个按钮，因此按钮缺失或超限时返回错误，由调用方降级为文本流程。
// 未配置封面时省略 card_image，卡片仍可点击；平台拒绝卡片时同样由调用方降级。
func (c *Client) SendCard(ctx context.Context, chatID string, card ports.OutboundCard) error {
	if !c.Configured() {
		return errNotConfigured
	}
	target := strings.TrimSpace(chatID)
	if target == "" {
		return errors.New("企业微信接收者不能为空")
	}
	if len(card.Buttons) == 0 || len(card.Buttons) > maxCardButtons {
		return fmt.Errorf("企业微信模板卡片按钮数量必须为 1-%d 个", maxCardButtons)
	}
	body := templateCardBody{
		CardType:  "button_interaction",
		Source:    &cardSource{Desc: cardSourceDesc},
		MainTitle: &cardMainTitle{Title: truncateRunes(card.Title, maxCardTitleRunes), Desc: truncateRunes(card.Text, maxCardDescRunes)},
		Buttons:   make([]cardButton, 0, len(card.Buttons)),
	}
	if url := strings.TrimSpace(card.PhotoURL); url != "" {
		body.CardImage = &cardImage{URL: url, AspectRatio: cardAspectRatio}
	}
	for _, button := range card.Buttons {
		body.Buttons = append(body.Buttons, cardButton{Text: button.Label, Style: 1, Key: button.Data})
	}
	payload := sendPayload{
		ToUser:       target,
		MsgType:      "template_card",
		AgentID:      c.agentID,
		TemplateCard: &body,
	}
	return c.sendMessage(ctx, payload)
}

// ReplaceButtons 在企业微信下不执行更新：更新模板卡片需要发送时返回的 response_code，
// 而按钮点击回调不携带该值，本渠道不额外维护这张映射表，因此按接口约定直接返回 nil。
func (c *Client) ReplaceButtons(context.Context, string, string, []ports.ActionButton) error {
	return nil
}

// Health 校验 corpid/secret 能否换取 access_token；未配置时返回 nil，表示该渠道未启用而非异常。
// 健康检查不写入凭证缓存，避免探活影响正常发送所使用的 token。
func (c *Client) Health(ctx context.Context) error {
	if !c.Configured() {
		return nil
	}
	_, _, err := c.fetchToken(ctx)
	return err
}

// accessToken 返回可用的应用凭证，命中缓存时不发起网络请求。
// 缓存由 tokenMu 保护，并发首次获取只会产生一次 gettoken 请求。
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}
	token, expiresIn, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}
	lifetime := time.Duration(expiresIn) * time.Second
	if expiresIn <= 0 {
		lifetime = time.Duration(tokenLifetimeSeconds) * time.Second
	}
	effective := lifetime - time.Duration(tokenRefreshMarginSeconds)*time.Second
	if effective <= 0 {
		effective = lifetime
	}
	c.token = token
	c.tokenExpiry = time.Now().Add(effective)
	return token, nil
}

// invalidateToken 在平台判定凭证失效时清空缓存，使下一次发送重新获取。
func (c *Client) invalidateToken() {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = ""
	c.tokenExpiry = time.Time{}
}

// fetchToken 向企业微信换取 access_token；errcode 非 0 或凭证为空都视为失败。
func (c *Client) fetchToken(ctx context.Context) (string, int, error) {
	query := url.Values{}
	query.Set("corpid", c.corpID)
	query.Set("corpsecret", c.corpSecret)
	endpoint := c.apiHost + "/cgi-bin/gettoken?" + query.Encode()
	var response apiResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		return "", 0, err
	}
	if response.ErrCode != 0 {
		return "", 0, fmt.Errorf("获取企业微信 access_token 失败：errcode=%d errmsg=%s", response.ErrCode, response.ErrMsg)
	}
	if response.AccessToken == "" {
		return "", 0, errors.New("企业微信返回的 access_token 为空")
	}
	return response.AccessToken, response.ExpiresIn, nil
}

// sendMessage 调用应用消息接口；凭证失效时刷新一次并重发，其余错误直接返回。
func (c *Client) sendMessage(ctx context.Context, payload sendPayload) error {
	var lastErr error
	for attempt := 0; attempt < maxTokenAttempts; attempt++ {
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		endpoint := c.apiHost + "/cgi-bin/message/send?access_token=" + url.QueryEscape(token)
		var response apiResponse
		if err := c.doJSON(ctx, http.MethodPost, endpoint, payload, &response); err != nil {
			return err
		}
		if response.ErrCode == 0 {
			return nil
		}
		lastErr = fmt.Errorf("发送企业微信消息失败：errcode=%d errmsg=%s", response.ErrCode, response.ErrMsg)
		if attempt+1 < maxTokenAttempts && invalidTokenCode(response.ErrCode) {
			c.invalidateToken()
			continue
		}
		break
	}
	return lastErr
}

// doJSON 发送请求并解析 JSON 响应；响应体读取有上限，非 2xx 视为失败。
func (c *Client) doJSON(ctx context.Context, method, endpoint string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("企业微信接口返回状态码 %d", response.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析企业微信响应失败：%w", err)
	}
	return nil
}

// invalidTokenCode 判定平台是否在提示当前凭证已失效，需要刷新后重试。
func invalidTokenCode(code int) bool {
	switch code {
	case 40014, 42001:
		return true
	default:
		return false
	}
}

// splitText 按字节上限切分文本，切点只落在 rune 边界，保证每片都是合法 UTF-8。
func splitText(text string, limit int) []string {
	chunks := make([]string, 0, 1)
	var builder strings.Builder
	size := 0
	for _, value := range text {
		width := utf8.RuneLen(value)
		if size > 0 && size+width > limit {
			chunks = append(chunks, builder.String())
			builder.Reset()
			size = 0
		}
		builder.WriteRune(value)
		size += width
	}
	return append(chunks, builder.String())
}

// joinTitleText 在图文降级为纯文本时保留标题信息，避免用户丢失上下文。
func joinTitleText(title, text string) string {
	head := strings.TrimSpace(title)
	body := strings.TrimSpace(text)
	switch {
	case head == "":
		return body
	case body == "":
		return head
	default:
		return head + "\n" + body
	}
}

// sendPayload 是企业微信应用消息接口的请求体，text、news 与 template_card 按 msgtype 三选一。
type sendPayload struct {
	ToUser       string            `json:"touser"`
	MsgType      string            `json:"msgtype"`
	AgentID      string            `json:"agentid"`
	Text         *textBody         `json:"text,omitempty"`
	News         *newsBody         `json:"news,omitempty"`
	TemplateCard *templateCardBody `json:"template_card,omitempty"`
}

// templateCardBody 是模板卡片（按钮交互型）的请求体。
// 按钮点击由企业微信以 event 回调送回，EventKey 即 cardButton.Key。
type templateCardBody struct {
	CardType  string         `json:"card_type"`
	Source    *cardSource    `json:"source"`
	MainTitle *cardMainTitle `json:"main_title"`
	CardImage *cardImage     `json:"card_image,omitempty"`
	Buttons   []cardButton   `json:"button_list"`
}

type cardSource struct {
	Desc      string `json:"desc"`
	DescColor int    `json:"desc_color"`
}

type cardMainTitle struct {
	Title string `json:"title"`
	Desc  string `json:"desc,omitempty"`
}

type cardImage struct {
	URL         string  `json:"url"`
	AspectRatio float64 `json:"aspect_ratio"`
}

type cardButton struct {
	Text  string `json:"text"`
	Style int    `json:"style"`
	Key   string `json:"key"`
}

// truncateRunes 按 rune 上限截断文案，超出时以省略号结尾，保证不切断多字节字符。
func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit-1]) + "…"
}

type textBody struct {
	Content string `json:"content"`
}

type newsBody struct {
	Articles []newsArticle `json:"articles"`
}

type newsArticle struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	PicURL      string `json:"picurl"`
}

// apiResponse 覆盖 gettoken 与 message/send 的公共字段，errcode 为 0 表示业务成功。
type apiResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}
