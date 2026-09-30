// Package pan115 封装 115 开放平台的扫码登录、账号、目录与离线下载协议。
//
// 本包只做协议适配：限流、编码、错误映射和响应解码集中在这里；
// 令牌刷新、保存目录与下载受理等业务规则由 application 层统一实现，
// HTTP、定时任务与下载链路复用同一份规则。
package pan115

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 115 开放平台的固定入口；115 不接受经代理访问，这里始终直连。
const (
	passportBase = "https://passportapi.115.com"
	qrcodeBase   = "https://qrcodeapi.115.com"
	apiBase      = "https://proapi.115.com"
)

// 请求节流参数：相邻请求间隔 250ms（约 4 请求/秒）且并发不超过 2，
// 避免触发 115 风控；单个响应体上限 4MiB。
const (
	requestGap       = 250 * time.Millisecond
	maxInFlight      = 2
	requestTimeout   = 35 * time.Second
	maxResponseBytes = 4 << 20
)

// ErrUnauthorized 表示 115 拒绝了当前访问令牌，调用方刷新令牌后重试。
var ErrUnauthorized = errors.New("115 访问令牌已失效")

// TransportError 表示请求 115 时在网络层或 HTTP 状态层失败，而不是 115 返回的业务错误码。
// 调用方据此区分「115 暂时不可用」与「115 明确拒绝了本次业务操作」。
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return e.Err.Error() }

func (e *TransportError) Unwrap() error { return e.Err }

// Unavailable 判断错误是否来自 115 侧的网络或 HTTP 层失败。
func Unavailable(err error) bool {
	var transportErr *TransportError
	return errors.As(err, &transportErr)
}

// unauthorizedCodes 是 115 表示访问令牌失效的错误码。
var unauthorizedCodes = map[int]struct{}{
	99: {}, 40140114: {}, 40140115: {}, 40140116: {}, 40140119: {},
	40140120: {}, 40140123: {}, 40140124: {}, 40140125: {}, 40140126: {},
}

// APIError 是 115 返回的业务错误码与消息。
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("115 返回错误码 %d", e.Code)
	}
	return fmt.Sprintf("115 错误 %d：%s", e.Code, e.Message)
}

// Unauthorized 判断错误是否表示访问令牌失效；HTTP 401 与业务错误码都算。
func Unauthorized(err error) bool {
	if errors.Is(err, ErrUnauthorized) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		_, ok := unauthorizedCodes[apiErr.Code]
		return ok
	}
	return false
}

// Client 是直连 115 开放平台的协议客户端。
type Client struct {
	http  *http.Client
	gap   time.Duration
	slots chan struct{}

	// 三个入口默认指向 115 生产环境；仅包内测试会改写它们。
	passport string
	qrcode   string
	api      string

	mu       sync.Mutex
	nextCall time.Time
}

// New 创建协议客户端；client 为空时使用内置超时。
func New(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	clone := *client
	if clone.Timeout == 0 {
		clone.Timeout = requestTimeout
	}
	return &Client{
		http:     &clone,
		gap:      requestGap,
		slots:    make(chan struct{}, maxInFlight),
		passport: passportBase,
		qrcode:   qrcodeBase,
		api:      apiBase,
	}
}

// Close 释放空闲连接；替换客户端或退出时调用。
func (c *Client) Close() {
	transport := c.http.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// admit 取得一次请求配额：先占用并发槽，再等待全局最小间隔。
func (c *Client) admit(ctx context.Context) (func(), error) {
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.Lock()
	now := time.Now()
	wait := c.gap - now.Sub(c.nextCall)
	if wait < 0 {
		wait = 0
	}
	c.nextCall = now.Add(wait)
	c.mu.Unlock()
	if wait <= 0 {
		return func() { <-c.slots }, nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return func() { <-c.slots }, nil
	case <-ctx.Done():
		<-c.slots
		return nil, ctx.Err()
	}
}

// send 发送一次已编码的请求；响应体由调用方关闭。
func (c *Client) send(ctx context.Context, method, endpoint, token string, body io.Reader, contentType string) (*http.Response, error) {
	return c.sendWithUserAgent(ctx, method, endpoint, token, body, contentType, nil)
}

// sendWithUserAgent 在 send 的基础上显式设置 User-Agent。
// 115 会把下载直链绑定到换取直链时使用的 User-Agent，播放链路必须能透传真实播放端的 UA；
// userAgent 为 nil 表示不干预，由 net/http 使用默认值。
func (c *Client) sendWithUserAgent(ctx context.Context, method, endpoint, token string, body io.Reader, contentType string, userAgent *string) (*http.Response, error) {
	release, err := c.admit(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if userAgent != nil {
		// 显式写入（含空值）：空值同样要落到请求头，用于抑制 net/http 的默认 UA。
		request.Header.Set("User-Agent", *userAgent)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, &TransportError{Err: fmt.Errorf("请求 115 失败: %w", err)}
	}
	if response.StatusCode == http.StatusUnauthorized {
		_ = response.Body.Close()
		return nil, ErrUnauthorized
	}
	return response, nil
}

// do 以表单编码发送请求；GET 的表单并入查询串，POST 保持表单体。
// 115 的扫码状态接口按查询串校验签名，把 uid/time/sign 放进请求体会被判为 key invalid，
// 因此这里统一决定参数位置，调用方不再各自拼接。
func (c *Client) do(ctx context.Context, method, endpoint, token string, form url.Values) (*http.Response, error) {
	return c.doWithUserAgent(ctx, method, endpoint, token, form, nil)
}

// doWithUserAgent 与 do 相同，但可显式设置 User-Agent。
func (c *Client) doWithUserAgent(ctx context.Context, method, endpoint, token string, form url.Values, userAgent *string) (*http.Response, error) {
	var body io.Reader
	contentType := ""
	if len(form) > 0 {
		if method == http.MethodGet {
			endpoint = appendQuery(endpoint, form)
		} else {
			body = strings.NewReader(form.Encode())
			contentType = "application/x-www-form-urlencoded"
		}
	}
	return c.sendWithUserAgent(ctx, method, endpoint, token, body, contentType, userAgent)
}

// appendQuery 把表单并入端点已有的查询串，保留端点自带的参数。
func appendQuery(endpoint string, form url.Values) string {
	separator := "?"
	if strings.Contains(endpoint, "?") {
		separator = "&"
	}
	return endpoint + separator + form.Encode()
}

// doMultipart 以 multipart/form-data 提交；115 的离线任务接口要求该编码。
func (c *Client) doMultipart(ctx context.Context, endpoint, token string, form url.Values) (*http.Response, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for key, values := range form {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				return nil, fmt.Errorf("构造 115 请求体失败: %w", err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("构造 115 请求体失败: %w", err)
	}
	return c.send(ctx, http.MethodPost, endpoint, token, &buffer, writer.FormDataContentType())
}

// readBody 读取并校验响应；非 2xx 直接报错，不把错误页当作业务数据解码。
func readBody(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &TransportError{Err: fmt.Errorf("115 返回 HTTP %d", response.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("读取 115 响应失败: %w", err)
	}
	return body, nil
}

// decodeData 把响应里的 data 段解码为目标类型；空 data 视为零值。
func decodeData[T any](raw json.RawMessage, action string) (T, error) {
	var value T
	if len(raw) == 0 || string(raw) == "null" {
		return value, nil
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("解码 115 %s失败: %w", action, err)
	}
	return value, nil
}

// authEnvelope 是 passport 与扫码接口的响应外壳：state 为数字 1 表示成功。
type authEnvelope struct {
	State   int             `json:"state"`
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Errno   int             `json:"errno"`
	Data    json.RawMessage `json:"data"`
}

func authRequest[T any](ctx context.Context, c *Client, method, endpoint string, form url.Values) (T, error) {
	var zero T
	response, err := c.do(ctx, method, endpoint, "", form)
	if err != nil {
		return zero, err
	}
	body, err := readBody(response)
	if err != nil {
		return zero, err
	}
	var envelope authEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return zero, fmt.Errorf("解码 115 授权响应失败: %w", err)
	}
	if envelope.Error != "" {
		return zero, &APIError{Code: envelope.Errno, Message: envelope.Error}
	}
	if envelope.State != 1 || envelope.Code != 0 {
		return zero, &APIError{Code: envelope.Code, Message: envelope.Message}
	}
	return decodeData[T](envelope.Data, "授权数据")
}

// apiEnvelope 是 proapi 文件与离线接口的响应外壳：state 为布尔 true 表示成功。
type apiEnvelope struct {
	State   bool            `json:"state"`
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// apiCall 发送一次 proapi 请求并返回 data 段。
func (c *Client) apiCall(ctx context.Context, method, endpoint, token string, form url.Values, action string) (json.RawMessage, error) {
	return c.apiCallWithUserAgent(ctx, method, endpoint, token, form, nil, action)
}

// apiCallWithUserAgent 与 apiCall 相同，但可显式设置 User-Agent；userAgent 为 nil 表示不干预。
func (c *Client) apiCallWithUserAgent(ctx context.Context, method, endpoint, token string, form url.Values, userAgent *string, action string) (json.RawMessage, error) {
	body, err := c.apiBody(ctx, method, endpoint, token, form, userAgent, action)
	if err != nil {
		return nil, err
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解码 115 %s响应失败: %w", action, err)
	}
	return envelope.Data, nil
}

// apiCallInto 发送一次 proapi 请求并把完整响应体解码到 out。
// 115 会把分页总数、父目录树等字段放在与 data 同级的顶层，只读 data 段会丢掉这些字段，
// 需要这些顶层字段的调用方使用本入口。
func (c *Client) apiCallInto(ctx context.Context, method, endpoint, token string, form url.Values, action string, out any) error {
	body, err := c.apiBody(ctx, method, endpoint, token, form, nil, action)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解码 115 %s响应失败: %w", action, err)
	}
	return nil
}

// apiBody 发送一次 proapi 请求，校验响应外壳后返回完整响应体。
// 响应外壳的校验只有这一处实现，避免各调用方重复解析 state/code。
func (c *Client) apiBody(ctx context.Context, method, endpoint, token string, form url.Values, userAgent *string, action string) ([]byte, error) {
	response, err := c.doWithUserAgent(ctx, method, endpoint, token, form, userAgent)
	if err != nil {
		return nil, err
	}
	body, err := readBody(response)
	if err != nil {
		return nil, err
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解码 115 %s响应失败: %w", action, err)
	}
	if !envelope.State || envelope.Code != 0 {
		return nil, &APIError{Code: envelope.Code, Message: envelope.Message}
	}
	return body, nil
}

func apiGet[T any](ctx context.Context, c *Client, endpoint, token string, query url.Values, action string) (T, error) {
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	raw, err := c.apiCall(ctx, http.MethodGet, endpoint, token, nil, action)
	if err != nil {
		var zero T
		return zero, err
	}
	return decodeData[T](raw, action)
}

func apiPost[T any](ctx context.Context, c *Client, endpoint, token string, form url.Values, action string) (T, error) {
	raw, err := c.apiCall(ctx, http.MethodPost, endpoint, token, form, action)
	if err != nil {
		var zero T
		return zero, err
	}
	return decodeData[T](raw, action)
}
