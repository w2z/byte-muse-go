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
	"strconv"
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

// 请求节流参数：相邻请求间隔 400ms（2.5 请求/秒）且严格串行，单个响应体上限 4MiB。
//
// 115 对开放接口的突发并发极其敏感：参考实现（MoviePilot、MeBox）实测 3 QPS 可长期稳定，
// 提到 8 QPS 会被阿里云 WAF 拦截并返回 405 阻断页，随后整体吞吐反而更低。
// 因此这里把速率压到 3 QPS 以下，并且不复用并发槽——宁可扫描慢一些，也不去试探风控边界。
const (
	requestGap       = 400 * time.Millisecond
	maxInFlight      = 1
	requestTimeout   = 35 * time.Second
	maxResponseBytes = 4 << 20
)

// playRequestGap 是换取下载直链（/open/ufile/downurl）的最小间隔。
// 播放端（Emby 等）会在媒体探测时批量换取直链，若不与目录扫描隔离，
// 会占满全局配额并反复触发 115 的访问上限，使目录扫描与 strm 生成长时间无法推进。
const playRequestGap = time.Second

// 限流退避参数。
//
// maxRequestAttempts 是普通错误（网络错误、5xx）的最大尝试次数（含首次）；
// maxRateLimitAttempts 是限流错误的最大尝试次数，明显更大：
// 115 的「访问上限」是整个账号共享的额度窗口，可能持续数十分钟，
// 任务必须能等到窗口恢复，而不是在几分钟内耗尽重试次数后直接失败。
//
// rateLimitBackoff 是限流冷却的基数，按尝试次数翻倍并封顶 rateLimitMaxBackoff：
// 短暂抖动后能快速恢复，额度真正用尽时也能退到足够长的等待。
// 115 显式返回 Retry-After 时以响应头为准。
//
// 网络错误与 5xx 按 serverRetryBaseWait 指数退避，且只重试可安全重放的 GET/HEAD。
const (
	maxRequestAttempts   = 5
	maxRateLimitAttempts = 8
	rateLimitBackoff     = 60 * time.Second
	rateLimitMaxBackoff  = 30 * time.Minute
	serverRetryBaseWait  = 1 * time.Second
	serverRetryMaxWait   = 30 * time.Second
)

// 115 的限流业务错误码：406 表示账号访问额度/访问上限已用尽，770004 表示访问频率过高。
// 115 并非所有限流场景都返回稳定错误码，因此错误码与 rateLimitMessages 的提示文本同时识别，
// 任一命中即按限流处理，避免把限流当成普通业务失败而直接终止扫描任务。
const (
	accessLimitCode     = 406
	requestFrequentCode = 770004
)

// rateLimitMessages 是 115 用文本表达的限流提示。
var rateLimitMessages = []string{
	"已达到当前访问上限",
	"达到访问上限",
	"访问频率过高",
	"请求过于频繁",
	"操作过于频繁",
}

// ErrUnauthorized 表示 115 拒绝了当前访问令牌，调用方刷新令牌后重试。
var ErrUnauthorized = errors.New("115 访问令牌已失效")

// TransportError 表示请求 115 时在网络层或 HTTP 状态层失败，而不是 115 返回的业务错误码。
// 调用方据此区分「115 暂时不可用」与「115 明确拒绝了本次业务操作」。
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return e.Err.Error() }

func (e *TransportError) Unwrap() error { return e.Err }

// HTTPError 表示 115 返回了非 2xx 的 HTTP 状态码，而不是业务错误码。
// 调用方据此区分「115 暂时不可用」与「115 明确拒绝了本次业务操作」；
// RetryAfter 保留响应头声明的冷却时间，供退避重试判断。
type HTTPError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string { return fmt.Sprintf("115 返回 HTTP %d", e.StatusCode) }

// Unavailable 判断错误是否来自 115 侧的网络或 HTTP 层失败。
func Unavailable(err error) bool {
	var transportErr *TransportError
	if errors.As(err, &transportErr) {
		return true
	}
	var httpErr *HTTPError
	return errors.As(err, &httpErr)
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
	slots chan struct{}

	// 节流参数：gap 是普通请求的最小间隔，playGap 是直链换取的最小间隔，
	// cooldown 是命中限流后的冷却时长，backoff 是网络错误与 5xx 的退避基数。
	// 默认取包内常量，仅包内测试会改写。
	gap      time.Duration
	playGap  time.Duration
	cooldown time.Duration
	backoff  time.Duration

	// 四个入口默认指向 115 生产环境；仅包内测试会改写它们。
	passport string
	qrcode   string
	api      string
	life     string

	// mu 保护下面三个截止时间：nextCall 与 nextPlay 是各链路的下次可发起时间，
	// retryAt 是命中限流后的全局冷却截止时间，所有链路共享。
	mu       sync.Mutex
	nextCall time.Time
	nextPlay time.Time
	retryAt  time.Time
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
		slots:    make(chan struct{}, maxInFlight),
		gap:      requestGap,
		playGap:  playRequestGap,
		cooldown: rateLimitBackoff,
		backoff:  serverRetryBaseWait,
		passport: passportBase,
		qrcode:   qrcodeBase,
		api:      apiBase,
		life:     "https://webapi.115.com",
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

// admit 取得一次普通请求配额：先占用并发槽，再等待全局冷却与最小间隔。
// 返回的释放函数必须调用，否则并发槽不会归还。
func (c *Client) admit(ctx context.Context) (func(), error) {
	return c.admitWith(ctx, c.gap, &c.nextCall)
}

// admitWith 是 admit 的通用实现：把最小间隔与「下次可发起时间」参数化，
// 使直链换取能用更小的配额复用同一套冷却与取消语义。
func (c *Client) admitWith(ctx context.Context, gap time.Duration, next *time.Time) (func(), error) {
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-c.slots }
	for {
		wait, cooling := c.reserve(gap, next)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				release()
				return nil, ctx.Err()
			}
		}
		if !cooling {
			return release, nil
		}
	}
}

// admitPlay 取得一次直链换取配额：只保证 playGap 的间隔，不占用并发槽，
// 避免与 admit 的两套槽位互相等待；并发上限仍由 admit 统一控制。
func (c *Client) admitPlay(ctx context.Context) error {
	for {
		wait, cooling := c.reserve(c.playGap, &c.nextPlay)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if !cooling {
			return nil
		}
	}
}

// reserve 计算本次请求需要等待的时间并推进「下次可发起时间」。
// cooling 为真表示此刻仍处于全局冷却窗口内：调用方等待后必须重新计算，
// 因为其他并发响应可能已经延长了冷却截止时间。
func (c *Client) reserve(gap time.Duration, next *time.Time) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	wait := gap - now.Sub(*next)
	if cooldown := c.retryAt.Sub(now); cooldown > wait {
		wait = cooldown
	}
	if wait < 0 {
		wait = 0
	}
	*next = now.Add(wait)
	return wait, now.Before(c.retryAt)
}

// extendCooldown 延长全局冷却窗口；并发请求同时命中限流时保留最晚的截止时间，
// 使 HTTP、定时任务与播放链路共享同一份退避，而不是各自重试。
func (c *Client) extendCooldown(wait time.Duration) {
	if wait <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if until := time.Now().Add(wait); until.After(c.retryAt) {
		c.retryAt = until
	}
}

// cooldownReporterKey 承载可选的限流冷却观察者。
type cooldownReporterKey struct{}

type externalRetryKey struct{}

// WithExternalRetry delegates retries to task-owned stage queues without extending the client's global cooldown.
// Normal request pacing and concurrency limits remain active; interactive clients keep their original retry policy.
func WithExternalRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, externalRetryKey{}, true)
}

// WithCooldownReporter 注册限流冷却观察者：每次进入或延长全局冷却时回调本次等待时长。
// 回调在请求 goroutine 内同步执行，只用于把「正在等待 115 恢复」反馈到任务进度，
// 不改变限流与重试语义；report 为 nil 时原样返回 ctx。
func WithCooldownReporter(ctx context.Context, report func(time.Duration)) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, cooldownReporterKey{}, report)
}

// reportCooldown 通知当前上下文的冷却观察者；未注册时不做任何事。
func reportCooldown(ctx context.Context, wait time.Duration) {
	if report, ok := ctx.Value(cooldownReporterKey{}).(func(time.Duration)); ok {
		report(wait)
	}
}

// isRateLimitError 判断错误是否表示 115 限流：HTTP 429、风控拦截的 405 阻断页，
// 或限流业务错误码与提示文本。限流请求不会在 115 侧生效，因此可以安全重试且不受方法限制。
func isRateLimitError(err error) bool { return IsRateLimitError(err) }

// IsRateLimitError 供任务执行器复用客户端的限流分类，避免把普通下载错误当成账号限流。
func IsRateLimitError(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode == http.StatusMethodNotAllowed
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return isRateLimitCode(apiErr.Code) || matchesRateLimitMessage(apiErr.Message)
	}
	return false
}

// isRateLimitCode 判断 115 业务错误码是否属于限流。
func isRateLimitCode(code int) bool {
	return code == accessLimitCode || code == requestFrequentCode
}

// matchesRateLimitMessage 判断 115 的文本提示是否属于限流。
// 115 未给所有限流场景提供稳定错误码，文本识别是错误码之外的兜底。
func matchesRateLimitMessage(message string) bool {
	for _, text := range rateLimitMessages {
		if strings.Contains(message, text) {
			return true
		}
	}
	return false
}

// callValue 执行一次可能被 115 限流的调用，并在限流或临时故障时退避重试。
// attempt 每次都必须重新发起完整请求，重试不复用上一次的响应；
// 退避通过延长全局冷却窗口实现，因此等待对所有链路同时生效。
// 限流错误享有远大于普通错误的尝试预算，使扫描任务能等到账号额度窗口恢复；
// 尝试次数用尽后返回最后一次的错误，不把真实失败吞成空结果。
// 上下文取消由 attempt 内部的配额等待负责，取消后立即返回 context 错误而不是继续重试。
func callValue[T any](c *Client, ctx context.Context, method string, attempt func() (T, error)) (T, error) {
	var zero T
	for index := 1; ; index++ {
		value, err := attempt()
		if err == nil {
			return value, nil
		}
		if external, _ := ctx.Value(externalRetryKey{}).(bool); external {
			return zero, err
		}
		wait, retryable := c.retryDelay(method, err, index)
		if !retryable {
			return zero, err
		}
		limit := maxRequestAttempts
		if isRateLimitError(err) {
			limit = maxRateLimitAttempts
		}
		if index >= limit {
			return zero, err
		}
		c.extendCooldown(wait)
		reportCooldown(ctx, wait)
	}
}

// retryDelay 判断错误是否值得重试并给出冷却时长。
//
// 限流（HTTP 429、风控 405 阻断页、限流错误码与提示文本）对所有方法都重试，
// 因为被 115 限流的请求不会在上游生效，重复提交不会产生副作用；
// 冷却时长按尝试次数指数增长，与额度窗口的长度匹配。
// 网络错误与 5xx 只重试可安全重放的 GET/HEAD，避免离线任务提交等写操作被重复执行。
func (c *Client) retryDelay(method string, err error, attempt int) (time.Duration, bool) {
	// 调用方主动取消不是 115 的故障：立即返回，既不重试也不延长全局冷却。
	if errors.Is(err, context.Canceled) {
		return 0, false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.StatusCode == http.StatusTooManyRequests:
			if httpErr.RetryAfter > 0 {
				return httpErr.RetryAfter, true
			}
			return c.rateLimitWait(attempt), true
		case httpErr.StatusCode == http.StatusMethodNotAllowed && isReplayable(method):
			// 115 被风控拦截时返回 405 阻断页，它不是业务失败，等冷却后重试。
			return c.rateLimitWait(attempt), true
		case httpErr.StatusCode >= 500 && isReplayable(method):
			return c.serverBackoff(attempt), true
		}
		return 0, false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && (isRateLimitCode(apiErr.Code) || matchesRateLimitMessage(apiErr.Message)) {
		return c.rateLimitWait(attempt), true
	}
	var transportErr *TransportError
	if errors.As(err, &transportErr) && isReplayable(method) {
		return c.serverBackoff(attempt), true
	}
	return 0, false
}

// rateLimitWait 给出限流冷却时长：以 cooldown 为基数按尝试次数翻倍并封顶，
// 既能在短暂抖动后快速恢复，也能在额度真正用尽时退到足够长的等待。
func (c *Client) rateLimitWait(attempt int) time.Duration {
	if c.cooldown <= 0 {
		return 0
	}
	if attempt < 1 {
		attempt = 1
	}
	wait := c.cooldown << (attempt - 1)
	if wait <= 0 || wait > rateLimitMaxBackoff {
		return rateLimitMaxBackoff
	}
	return wait
}

// serverBackoff 给出网络错误与 5xx 的指数退避时长，按尝试次数翻倍并封顶。
func (c *Client) serverBackoff(attempt int) time.Duration {
	wait := c.backoff << (attempt - 1)
	if wait > serverRetryMaxWait {
		return serverRetryMaxWait
	}
	return wait
}

// isReplayable 判断请求方法是否可以安全重放；写操作不参与网络错误与 5xx 重试。
func isReplayable(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
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
// 错误保留状态码与 Retry-After，供调用方判断是否值得退避重试。
func readBody(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &HTTPError{
			StatusCode: response.StatusCode,
			RetryAfter: parseRetryAfter(response.Header.Get("Retry-After")),
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("读取 115 响应失败: %w", err)
	}
	return body, nil
}

// parseRetryAfter 解析 Retry-After 响应头，兼容秒数与 HTTP 日期两种格式；
// 缺失、非法或已过期时返回零，由调用方回落到默认冷却时长。
func parseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(header); err == nil {
		if wait := time.Until(deadline); wait > 0 {
			return wait
		}
	}
	return 0
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

// authRequest 发送一次授权请求并解析响应外壳；命中限流时统一退避重试。
func authRequest[T any](ctx context.Context, c *Client, method, endpoint string, form url.Values) (T, error) {
	return callValue(c, ctx, method, func() (T, error) {
		return authRequestOnce[T](ctx, c, method, endpoint, form)
	})
}

// authRequestOnce 是 authRequest 的单次实现；重试由 authRequest 统一驱动。
func authRequestOnce[T any](ctx context.Context, c *Client, method, endpoint string, form url.Values) (T, error) {
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
// 命中限流或临时故障时在这里统一退避重试。
func (c *Client) apiCallWithUserAgent(ctx context.Context, method, endpoint, token string, form url.Values, userAgent *string, action string) (json.RawMessage, error) {
	return callValue(c, ctx, method, func() (json.RawMessage, error) {
		return c.apiCallOnceWithUserAgent(ctx, method, endpoint, token, form, userAgent, action)
	})
}

// apiCallOnceWithUserAgent 是 apiCallWithUserAgent 的单次实现，
// 供需要自行控制重试节奏的调用方（例如直链换取）复用。
func (c *Client) apiCallOnceWithUserAgent(ctx context.Context, method, endpoint, token string, form url.Values, userAgent *string, action string) (json.RawMessage, error) {
	body, err := c.apiBodyOnce(ctx, method, endpoint, token, form, userAgent, action)
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

// apiBody 发送一次 proapi 请求，校验响应外壳后返回完整响应体；命中限流时统一退避重试。
// 响应外壳的校验只有这一处实现，避免各调用方重复解析 state/code。
func (c *Client) apiBody(ctx context.Context, method, endpoint, token string, form url.Values, userAgent *string, action string) ([]byte, error) {
	return callValue(c, ctx, method, func() ([]byte, error) {
		return c.apiBodyOnce(ctx, method, endpoint, token, form, userAgent, action)
	})
}

// apiBodyOnce 是 apiBody 的单次实现；重试由 apiBody 统一驱动。
func (c *Client) apiBodyOnce(ctx context.Context, method, endpoint, token string, form url.Values, userAgent *string, action string) ([]byte, error) {
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
