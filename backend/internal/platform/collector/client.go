// Package collector 提供固定来源的元数据采集和统一网络错误分类。
package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/platform/javdbapp"
)

var (
	ErrBlocked                 = errors.New("source_blocked")                                      // ErrBlocked 表示验证页或拒绝访问。
	ErrParse                   = errors.New("source_format_changed")                               // ErrParse 表示返回结构不能可靠解析。
	ErrUnsupported             = errors.New("unsupported_collection")                              // ErrUnsupported 表示来源不支持请求能力。
	ErrInvalidRequest          = errors.New("invalid_collection_request")                          // ErrInvalidRequest 表示参数无效。
	ErrRateLimited             = errors.New("source_rate_limited")                                 // ErrRateLimited 表示站点限流，禁止立即重试。
	ErrUnavailable             = errors.New("source_unavailable")                                  // ErrUnavailable 表示网络或服务异常。
	ErrCookieRequired          = errors.New("source_cookie_required")                              // ErrCookieRequired 表示明确登录页或 HTTP 401。
	ErrInteractiveVerification = errors.New("source_interactive_verification")                     // ErrInteractiveVerification 表示源站需要人工年龄/驾驶验证。
	ErrChallenge               = fmt.Errorf("%w: cloudflare_challenge", ErrBlocked)                // ErrChallenge 仅表示可尝试增强的 CF 验证。
	ErrBypassUnavailable       = errors.New("bypass_unavailable")                                  // ErrBypassUnavailable 表示增强服务失败，不代表源站要求 Cookie。
	ErrBypassTimeout           = fmt.Errorf("%w: bypass_timeout", ErrBypassUnavailable)            // ErrBypassTimeout 表示增强等待超时。
	ErrBypassCaptcha           = fmt.Errorf("%w: bypass_captcha_required", ErrBypassUnavailable)   // ErrBypassCaptcha 表示增强明确要求人工验证码。
	ErrBypassProxy             = fmt.Errorf("%w: bypass_proxy_failed", ErrBypassUnavailable)       // ErrBypassProxy 表示浏览器代理连接或认证失败。
	ErrBypassBrowser           = fmt.Errorf("%w: bypass_browser_failed", ErrBypassUnavailable)     // ErrBypassBrowser 表示远程浏览器无法启动或崩溃。
	ErrBypassSession           = fmt.Errorf("%w: bypass_session_failed", ErrBypassUnavailable)     // ErrBypassSession 表示远程会话失效。
	ErrBypassTarget            = fmt.Errorf("%w: bypass_target_unavailable", ErrBypassUnavailable) // ErrBypassTarget 表示浏览器访问目标站点发生网络错误。
	ErrBypassConfig            = errors.New("bypass_config_invalid")                               // ErrBypassConfig 表示增强地址或协议尚不支持。
)

// Fetcher 是解析器唯一的网络入口，可在测试中注入脱敏页面。
type Fetcher interface {
	Get(context.Context, string) ([]byte, error)
}

// Client 限制来源、响应体、超时和访问间隔；只对已识别的 CF 验证增强一次，不重试限流。
type Client struct {
	http   *http.Client
	mu     sync.Mutex
	next   map[string]time.Time
	bypass *bypassClient
	proxy  string
	app    *javdbapp.Client
}

// NewClientWithProxy 使用管理员配置的 HTTP/SOCKS 代理；错误不泄露代理凭据。
func NewClientWithProxy(raw string) (*Client, error) {
	raw = strings.TrimSpace(raw)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(raw) != "" {
		proxy, err := url.Parse(raw)
		if err != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
			return nil, ErrInvalidRequest
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	c := NewClient(&http.Client{Transport: transport, Timeout: 25 * time.Second})
	c.proxy = raw
	return c, nil
}

// NewClient 克隆客户端并约束重定向，禁止把站点响应变成任意网络请求。
func NewClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	clone := *client
	if clone.Timeout == 0 {
		clone.Timeout = 25 * time.Second
	}
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return ErrUnavailable
		}
		if !allowedURL(req.URL) || (len(via) > 0 && req.URL.Host != via[0].URL.Host) {
			return ErrInvalidRequest
		}
		return nil
	}
	app, _ := javdbapp.NewAutomatic(&clone)
	return &Client{http: &clone, next: make(map[string]time.Time), app: app}
}

// MovieDetail 复用同一代理和 App 客户端读取影片与演员，不依赖 HTML 验证页。
func (c *Client) MovieDetail(ctx context.Context, movieID string) (javdbapp.Movie, error) {
	if c == nil || c.app == nil {
		return javdbapp.Movie{}, ErrUnavailable
	}
	return c.app.Detail(ctx, movieID)
}

// ActorMovies 按 JavDB 演员 ID 读取作品页，不将演员名作为来源 ID。
func (c *Client) ActorMovies(ctx context.Context, id string, page int) ([]javdbapp.Movie, error) {
	if c == nil || c.app == nil {
		return nil, ErrUnavailable
	}
	return c.app.ActorMovies(ctx, id, page)
}

func allowedURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Host {
	case "raw.githubusercontent.com":
		return u.Path == "/gfriends/gfriends/master/Filetree.json"
	case "www.avbase.net", "javdb.com", "www.javbus.com", "netflav.com", "www.javlibrary.com", "jable.tv", "supjav.com":
		return true
	}
	return false
}

// Get 发起一次有界只读请求；错误不带可能包含凭据的底层 URL 或代理信息。
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	body, err := c.getDirect(ctx, rawURL)
	if errors.Is(err, ErrChallenge) && c.bypass != nil {
		return c.bypass.get(ctx, rawURL)
	}
	return body, err
}

// getDirect 只发起直接请求，增强返回值不能再次触发增强，避免循环。
func (c *Client) getDirect(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !allowedURL(u) {
		return nil, ErrInvalidRequest
	}
	c.mu.Lock()
	now := time.Now()
	start := c.next[u.Host]
	if start.Before(now) {
		start = now
	}
	c.next[u.Host] = start.Add(time.Second)
	c.mu.Unlock()
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ByteMuse/1.0; metadata collector)")
	req.Header.Set("Accept", "text/html,application/json;q=0.9")
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 429 {
		return nil, ErrRateLimited
	}
	const limit = 8 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(body) > limit {
		return nil, ErrParse
	}
	finalURL := rawURL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	if err = classifyPage(response.StatusCode, response.Header, finalURL, body); err != nil {
		return nil, err
	}
	return body, nil
}
