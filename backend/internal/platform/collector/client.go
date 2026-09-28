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
)

var (
	ErrBlocked        = errors.New("source_blocked")             // ErrBlocked 表示验证页或拒绝访问。
	ErrParse          = errors.New("source_format_changed")      // ErrParse 表示返回结构不能可靠解析。
	ErrUnsupported    = errors.New("unsupported_collection")     // ErrUnsupported 表示来源不支持请求能力。
	ErrInvalidRequest = errors.New("invalid_collection_request") // ErrInvalidRequest 表示参数无效。
	ErrRateLimited    = errors.New("source_rate_limited")        // ErrRateLimited 表示站点限流，禁止立即重试。
	ErrUnavailable    = errors.New("source_unavailable")         // ErrUnavailable 表示网络或服务异常。
)

// Fetcher 是解析器唯一的网络入口，可在测试中注入脱敏页面。
type Fetcher interface {
	Get(context.Context, string) ([]byte, error)
}

// Client 限制来源、响应体、超时和访问间隔；不会自动重试验证码或限流。
type Client struct {
	http *http.Client
	mu   sync.Mutex
	next map[string]time.Time
}

// NewClientWithProxy 使用管理员配置的 HTTP/SOCKS 代理；错误不泄露代理凭据。
func NewClientWithProxy(raw string) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(raw) != "" {
		proxy, err := url.Parse(raw)
		if err != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
			return nil, ErrInvalidRequest
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	return NewClient(&http.Client{Transport: transport, Timeout: 25 * time.Second}), nil
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
	return &Client{http: &clone, next: make(map[string]time.Time)}
}

func allowedURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Host {
	case "www.avbase.net", "javdb.com", "www.javbus.com", "netflav.com":
		return true
	}
	return false
}

// Get 发起一次有界只读请求；错误不带可能包含凭据的底层 URL 或代理信息。
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
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
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return nil, ErrBlocked
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}
	const limit = 8 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(body) > limit {
		return nil, ErrParse
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"<title>just a moment", "<title>请稍候", "/cdn-cgi/challenge-platform", "/doc/driver-verify", "cf-chl-"} {
		if strings.Contains(lower, marker) {
			return nil, ErrBlocked
		}
	}
	if response.Request != nil && strings.Contains(response.Request.URL.Path, "driver-verify") {
		return nil, ErrBlocked
	}
	return body, nil
}
