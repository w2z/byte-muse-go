package pan115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Cookie 扫码渠道标识；取值与 115 客户端的 app 参数一致，
// 扫码结果接口按该值决定下发哪个端的 Cookie。
const (
	CookieClientAlipayMini = "alipaymini"
	CookieClientWeChatMini = "wechatmini"
	CookieClientAndroid    = "115android"
	CookieClientIOS        = "115ios"
	CookieClientWeb        = "web"
	CookieClientIPad       = "115ipad"
	CookieClientTV         = "tv"
)

// defaultCookieClientType 是未指定或无法识别渠道时的兜底值，与 115 扫码默认渠道一致。
const defaultCookieClientType = CookieClientAlipayMini

// cookieClientTypes 是允许的扫码渠道，也是渠道取值域的唯一权威清单。
var cookieClientTypes = []string{
	CookieClientAlipayMini,
	CookieClientWeChatMini,
	CookieClientAndroid,
	CookieClientIOS,
	CookieClientWeb,
	CookieClientIPad,
	CookieClientTV,
}

// NormalizeCookieClientType 归一化扫码渠道：空值与未知值回落到默认渠道。
// 调用方不需要各自实现兜底，也不会把非法值透传给 115。
func NormalizeCookieClientType(raw string) string {
	value := strings.TrimSpace(raw)
	for _, item := range cookieClientTypes {
		if item == value {
			return item
		}
	}
	return defaultCookieClientType
}

// CookieLogin 保存一次 Cookie 扫码会话；uid/time/sign 只在本包内使用，不返回给调用方，
// 因此二维码会话不会成为可重放的凭据。
type CookieLogin struct {
	QRCode []byte

	uid        string
	issuedAt   string
	sign       string
	clientType string
}

// BeginCookieLogin 申请扫码二维码并取回可直接渲染的 PNG。
// 二维码与状态查询始终走 web 入口，渠道只影响最终换取哪个端的 Cookie。
func (c *Client) BeginCookieLogin(ctx context.Context, clientType string) (*CookieLogin, error) {
	clientType = NormalizeCookieClientType(clientType)
	data, err := cookieRequest[struct {
		UID  string          `json:"uid"`
		Time json.RawMessage `json:"time"`
		Sign string          `json:"sign"`
	}](ctx, c, http.MethodGet, c.qrcode+"/api/1.0/"+CookieClientWeb+"/1.0/token/", nil, "扫码令牌")
	if err != nil {
		return nil, err
	}
	issuedAt := cookieNumberString(data.Time)
	if data.UID == "" || issuedAt == "" || data.Sign == "" {
		return nil, fmt.Errorf("115 扫码响应缺少登录参数")
	}
	image, err := c.qrCodeImage(ctx, CookieClientWeb, data.UID)
	if err != nil {
		return nil, err
	}
	return &CookieLogin{QRCode: image, uid: data.UID, issuedAt: issuedAt, sign: data.Sign, clientType: clientType}, nil
}

// CookieLoginStatus 查询一次 Cookie 扫码状态；超时由调用方控制。
func (c *Client) CookieLoginStatus(ctx context.Context, login *CookieLogin) (LoginState, error) {
	data, err := cookieRequest[struct {
		Status *int `json:"status"`
	}](ctx, c, http.MethodGet, c.qrcode+"/get/status/", url.Values{
		"uid":  {login.uid},
		"time": {login.issuedAt},
		"sign": {login.sign},
	}, "扫码状态")
	if err != nil {
		// 二维码失效后 115 返回 state=0 且 message 为 key invalid，属于正常过期而不是上游故障。
		var apiErr *APIError
		if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Message), "key invalid") {
			return LoginExpired, nil
		}
		return "", err
	}
	return loginStateFromCode(data.Status), nil
}

// ExchangeCookie 用已授权的 uid 换取 Cookie，返回「名=值; 名=值」形式。
// Cookie 只在内存中传递，既不落库也不写入日志。
func (c *Client) ExchangeCookie(ctx context.Context, login *CookieLogin) (string, error) {
	data, err := cookieRequest[struct {
		Cookie map[string]string `json:"cookie"`
	}](ctx, c, http.MethodPost, c.qrcode+"/app/1.0/"+login.clientType+"/1.0/login/qrcode/", url.Values{
		"account": {login.uid},
	}, "登录凭据")
	if err != nil {
		return "", err
	}
	cookie := formatCookie(data.Cookie)
	if cookie == "" {
		return "", fmt.Errorf("115 未返回登录 Cookie")
	}
	return cookie, nil
}

// formatCookie 把 Cookie 字典拼成请求头形式；按名排序保证同一账号每次结果一致，空名或空值项直接丢弃。
func formatCookie(values map[string]string) string {
	names := make([]string, 0, len(values))
	for name, value := range values {
		if name != "" && value != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+values[name])
	}
	return strings.Join(parts, "; ")
}

// loginStateFromCode 把 115 的业务状态码映射成对外状态；未识别状态按「等待中」处理，
// 避免把仍在进行的登录误判为失败。令牌扫码与 Cookie 扫码共用这一份映射。
func loginStateFromCode(code *int) LoginState {
	if code == nil {
		return LoginWaiting
	}
	switch *code {
	case 0:
		return LoginWaiting
	case 1:
		return LoginScanned
	case 2:
		return LoginAuthorized
	case -1:
		return LoginExpired
	case -2:
		return LoginCanceled
	default:
		return LoginWaiting
	}
}

// cookieEnvelope 是 115 扫码接口的响应外壳；state 在不同接口上可能是数字 1 或布尔 true。
type cookieEnvelope struct {
	State   json.RawMessage `json:"state"`
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Errno   int             `json:"errno"`
	Data    json.RawMessage `json:"data"`
}

// cookieStateOK 判断扫码响应是否成功。
func cookieStateOK(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "1", "true", `"1"`, `"true"`:
		return true
	default:
		return false
	}
}

// cookieRequest 发送一次扫码接口请求并返回 data 段；命中限流时统一退避重试。
// 扫码接口的响应外壳与 passport/proapi 不同（state 可能是布尔值），这里单独实现，
// 避免把类型差异带进已有的令牌解析逻辑。
func cookieRequest[T any](ctx context.Context, c *Client, method, endpoint string, form url.Values, action string) (T, error) {
	return callValue(c, ctx, method, func() (T, error) {
		return cookieRequestOnce[T](ctx, c, method, endpoint, form, action)
	})
}

// cookieRequestOnce 是 cookieRequest 的单次实现；重试由 cookieRequest 统一驱动。
func cookieRequestOnce[T any](ctx context.Context, c *Client, method, endpoint string, form url.Values, action string) (T, error) {
	var zero T
	response, err := c.do(ctx, method, endpoint, "", form)
	if err != nil {
		return zero, err
	}
	body, err := readBody(response)
	if err != nil {
		return zero, err
	}
	var envelope cookieEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return zero, fmt.Errorf("解码 115 %s失败: %w", action, err)
	}
	if envelope.Error != "" {
		return zero, &APIError{Code: envelope.Errno, Message: envelope.Error}
	}
	if !cookieStateOK(envelope.State) || envelope.Code != 0 {
		return zero, &APIError{Code: envelope.Code, Message: envelope.Message}
	}
	return decodeData[T](envelope.Data, action)
}

// cookieNumberString 把 115 返回的时间戳（数字或字符串）统一成字符串；无效值返回空串。
func cookieNumberString(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return ""
	}
	if strings.HasPrefix(value, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return ""
		}
		return strings.TrimSpace(text)
	}
	return value
}
