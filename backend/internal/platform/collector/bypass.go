package collector

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// passiveCFScript 只排除已知后台检测脚本路径；同页真实挑战标记仍然生效。
var passiveCFScript = regexp.MustCompile(`/cdn-cgi/challenge-platform/(?:[a-z]/[a-z]/)?scripts/jsd/[a-z0-9._/-]+`)

// bypassClient 仅连接管理员配置的增强服务，不保存返回 Cookie，不透传源站凭据。
type bypassClient struct {
	http             *http.Client
	engine, endpoint string
	proxy            string
}

// NewClientWithBypass 供嵌入及测试显式装配；无效配置在 CF 重试时返回配置错误。
func NewClientWithBypass(h *http.Client, engine, endpoint string) *Client {
	c := NewClient(h)
	c.ConfigureBypass(engine, endpoint, false)
	return c
}

// ConfigureBypass 仅在客户端启动前调用。连接增强服务保持直连，显式代理用于远程浏览器访问源站。
func (c *Client) ConfigureBypass(engine, endpoint string, useProxy bool) {
	engine, endpoint = strings.TrimSpace(engine), strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if engine == "" {
		c.bypass = nil
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	proxy := ""
	if useProxy {
		proxy = c.proxy
	}
	c.bypass = &bypassClient{engine: engine, endpoint: endpoint, proxy: proxy, http: &http.Client{Transport: transport, Timeout: 55 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// classifyPage 区分限流、CF、登录及普通拒绝；导航栏登录链接不能当作登录墙。
func classifyPage(status int, headers http.Header, finalURL string, body []byte) error {
	if status == 429 {
		return ErrRateLimited
	}
	lower := passiveCFScript.ReplaceAllString(strings.ToLower(string(body)), "")
	if headers.Get("Cf-Mitigated") == "challenge" {
		return ErrChallenge
	}
	for _, marker := range []string{"<title>just a moment", "<title>请稍候", "/cdn-cgi/challenge-platform", "cf-chl-"} {
		if strings.Contains(lower, marker) {
			return ErrChallenge
		}
	}
	u, _ := url.Parse(finalURL)
	if status == 401 {
		return ErrCookieRequired
	}
	if u != nil {
		switch strings.TrimRight(u.Path, "/") {
		case "/users/sign_in", "/login", "/signin":
			return ErrCookieRequired
		}
		if strings.Contains(u.Path, "driver-verify") {
			return ErrInteractiveVerification
		}
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err == nil && doc.Find("form input[type='password']").Length() > 0 {
		return ErrCookieRequired
	}
	if err == nil && doc.Find("#challenge-form, #challenge-running, #cf-challenge-running").Length() > 0 {
		return ErrChallenge
	}
	if strings.Contains(lower, "/doc/driver-verify") {
		return ErrInteractiveVerification
	}
	if status == 403 {
		return ErrBlocked
	}
	if status < 200 || status >= 300 {
		return ErrUnavailable
	}
	return nil
}

// get 对已确认的 CF 验证最多调用一次增强；增强结果再次分类后才交给解析器。
func (b *bypassClient) get(ctx context.Context, target string) (body []byte, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	u, err := url.Parse(target)
	if err != nil || !allowedURL(u) {
		return nil, ErrInvalidRequest
	}
	endpoint, err := url.Parse(b.endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, ErrBypassConfig
	}
	var request *http.Request
	switch b.engine {
	case "flaresolverr":
		if !strings.HasSuffix(endpoint.Path, "/v1") {
			endpoint.Path += "/v1"
		}
		command := map[string]any{"cmd": "request.get", "url": target, "maxTimeout": 45000}
		if b.proxy != "" {
			proxyURL, parseErr := url.Parse(b.proxy)
			if parseErr != nil || proxyURL.Host == "" {
				return nil, ErrBypassConfig
			}
			// Chromium 的 SOCKS5 原生使用远程 DNS，协议名不接受 Go 的 socks5h 别名。
			if proxyURL.Scheme == "socks5h" {
				proxyURL.Scheme = "socks5"
			}
			proxy := map[string]string{}
			if proxyURL.User != nil {
				proxy["username"] = proxyURL.User.Username()
				proxy["password"], _ = proxyURL.User.Password()
				proxyURL.User = nil
				proxy["url"] = proxyURL.String()
				var token [16]byte
				if _, err := rand.Read(token[:]); err != nil {
					return nil, ErrBypassUnavailable
				}
				session := "bytemuse-" + hex.EncodeToString(token[:])
				// 创建响应可能丢失；始终只清理本次随机 ID，且不受抓取取消影响。
				defer func() {
					cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cleanupCancel()
					_, cleanupErr := b.flareCommand(cleanupCtx, endpoint.String(), map[string]any{"cmd": "sessions.destroy", "session": session})
					if resultErr == nil && cleanupErr != nil {
						body, resultErr = nil, cleanupErr
					}
				}()
				result, createErr := b.flareCommand(ctx, endpoint.String(), map[string]any{"cmd": "sessions.create", "session": session, "proxy": proxy})
				if createErr != nil {
					return nil, createErr
				}
				if result.Session != session {
					return nil, ErrBypassUnavailable
				}
				command["session"] = session
			} else {
				proxy["url"] = proxyURL.String()
				command["proxy"] = proxy
			}
		}
		payload, _ := json.Marshal(command)
		request, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	case "cloudflare_bypass_for_scraping":
		if !strings.HasSuffix(endpoint.Path, "/html") {
			endpoint.Path += "/html"
		}
		q := endpoint.Query()
		q.Set("url", target)
		if b.proxy != "" {
			proxy := b.proxy
			if strings.HasPrefix(proxy, "socks5h://") {
				proxy = "socks5://" + strings.TrimPrefix(proxy, "socks5h://")
			}
			q.Set("proxy", proxy)
		}
		endpoint.RawQuery = q.Encode()
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	default:
		return nil, ErrBypassConfig
	}
	if err != nil {
		return nil, ErrBypassConfig
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := b.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return nil, ErrBypassTimeout
		}
		return nil, ErrBypassUnavailable
	}
	defer response.Body.Close()
	const limit = 16 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(raw) > limit {
		return nil, ErrBypassUnavailable
	}
	if response.StatusCode != 200 {
		return nil, flareFailure(raw)
	}
	status, finalURL, headers := 200, target, http.Header{}
	if b.engine == "flaresolverr" {
		var result struct {
			Status   string `json:"status"`
			Solution struct {
				Status   int    `json:"status"`
				URL      string `json:"url"`
				Response string `json:"response"`
			} `json:"solution"`
		}
		if json.Unmarshal(raw, &result) != nil {
			return nil, ErrBypassUnavailable
		}
		if result.Status != "ok" {
			return nil, flareFailure(raw)
		}
		if result.Solution.URL == "" {
			return nil, ErrBypassUnavailable
		}
		raw, status, finalURL = []byte(result.Solution.Response), result.Solution.Status, result.Solution.URL
	} else if value := response.Header.Get("X-Cf-Bypasser-Final-Url"); value != "" {
		finalURL = value
	}
	final, err := url.Parse(finalURL)
	if err != nil || !allowedURL(final) || final.Host != u.Host {
		return nil, ErrInvalidRequest
	}
	if len(raw) == 0 || len(raw) > 8<<20 {
		return nil, ErrParse
	}
	if err = classifyPage(status, headers, finalURL, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// flareCommand 管理本次鉴权代理会话；拒绝远端错误正文，避免泄露代理凭据。
func (b *bypassClient) flareCommand(ctx context.Context, endpoint string, command map[string]any) (result struct {
	Status  string `json:"status"`
	Session string `json:"session"`
}, err error) {
	payload, _ := json.Marshal(command)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return result, ErrBypassConfig
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, ErrBypassUnavailable
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &result) != nil {
		return result, ErrBypassUnavailable
	}
	if resp.StatusCode != http.StatusOK || result.Status != "ok" {
		return result, flareFailure(raw)
	}
	return result, nil
}

// flareFailure 仅输出固定错误分类，不把远端正文、Cookie 或代理鉴权信息写入错误。
func flareFailure(raw []byte) error {
	var response struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return ErrBypassUnavailable
	}
	message := strings.ToLower(response.Message)
	for _, rule := range []struct {
		markers []string
		err     error
	}{
		{[]string{"captcha detected"}, ErrBypassCaptcha},
		{[]string{"err_proxy_connection_failed", "err_tunnel_connection_failed", "err_no_supported_proxies", "err_proxy_auth", "err_invalid_auth_credentials"}, ErrBypassProxy},
		{[]string{"session not created", "chrome failed to start", "chrome not reachable", "invalid session id", "tab crashed", "disconnected: not connected to devtools"}, ErrBypassBrowser},
		{[]string{"the session doesn't exist"}, ErrBypassSession},
		{[]string{"timeout", "timed out"}, ErrBypassTimeout},
		{[]string{"err_connection_reset", "err_name_not_resolved", "err_connection_refused", "err_connection_closed", "err_cert_", "err_ssl_"}, ErrBypassTarget},
	} {
		for _, marker := range rule.markers {
			if strings.Contains(message, marker) {
				return rule.err
			}
		}
	}
	return ErrBypassUnavailable
}
