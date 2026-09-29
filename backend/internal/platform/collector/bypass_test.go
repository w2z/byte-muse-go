package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/ports"
)

// TestBypassBoundaries 验证增强仅用于 CF、登录与普通导航分离、返回值和服务 URL 有界。
func TestBypassBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		html   string
		result string
		want   error
		calls  int
	}{
		{"普通403不尝试增强", 403, "Forbidden", "", ErrBlocked, 0},
		{"401不尝试增强", 401, "Unauthorized", "", ErrCookieRequired, 0},
		{"限流不尝试增强", 429, "limit", "", ErrRateLimited, 0},
		{"登录链接非登录墙", 200, `<a href="/users/sign_in">Sign in</a>`, "", nil, 0},
		{"正常页CF后台检测不增强", 200, `<div class="movie-list">content</div><script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>`, "", nil, 0},
		{"动态后台检测不增强", 200, `<script>a.src='/cdn-cgi/challenge-platform/h/g/scripts/jsd/main.js';</script>`, "", nil, 0},
		{"增强正常页保留后台检测", 403, "cf-chl-test", `{"status":"ok","solution":{"url":"https://javdb.com/","status":200,"response":"<div class='movie-list'>content</div><script src='/cdn-cgi/challenge-platform/scripts/jsd/main.js'></script>"}}`, nil, 1},
		{"登录表单", 200, `<form action="/users/sign_in"><input type="password"></form>`, "", ErrCookieRequired, 0},
		{"增强仍是CF", 403, "cf-chl-test", `{"status":"ok","solution":{"url":"https://javdb.com/","status":200,"response":"cf-chl-test"}}`, ErrChallenge, 1},
		{"增强到登录页", 403, "cf-chl-test", `{"status":"ok","solution":{"url":"https://javdb.com/users/sign_in","status":200,"response":"login"}}`, ErrCookieRequired, 1},
		{"增强跨域", 403, "cf-chl-test", `{"status":"ok","solution":{"url":"http://127.0.0.1/","status":200,"response":"private"}}`, ErrInvalidRequest, 1},
		{"增强返回年龄验证页", 403, "cf-chl-test", `{"status":"ok","solution":{"url":"https://javdb.com/doc/driver-verify","status":200,"response":"<title>Age Verification</title>"}}`, ErrInteractiveVerification, 1},
		{"增强服务失败", 403, "cf-chl-test", `{"status":"error","message":"secret-cookie"}`, ErrBypassUnavailable, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1" {
					t.Errorf("path=%s", r.URL.Path)
				}
				var payload struct {
					Cmd, URL   string
					MaxTimeout int
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Cmd != "request.get" || payload.URL != "https://javdb.com/" || payload.MaxTimeout != 45000 {
					t.Errorf("payload=%+v", payload)
				}
				w.Write([]byte(tc.result))
			}))
			defer server.Close()
			c := NewClientWithBypass(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.html)), Request: r}, nil
			})}, "flaresolverr", server.URL+"/v1")
			_, err := c.Get(context.Background(), "https://javdb.com/")
			if !errors.Is(err, tc.want) || calls != tc.calls {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

// TestChallengeSignals 不把后台检测当阻断，也不因存在后台检测而放过真实挑战。
func TestChallengeSignals(t *testing.T) {
	for _, body := range []string{
		`<script src="/cdn-cgi/challenge-platform/h/g/orchestrate/chl_page/v1"></script>`,
		`<title>Just a moment...</title><script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>`,
		`<form id="challenge-form"></form>`,
	} {
		if err := classifyPage(200, http.Header{}, "https://javdb.com/", []byte(body)); !errors.Is(err, ErrChallenge) {
			t.Fatalf("challenge accepted: %v", err)
		}
	}
	if err := classifyPage(200, http.Header{"Cf-Mitigated": {"challenge"}}, "https://javdb.com/", []byte(`<script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>`)); !errors.Is(err, ErrChallenge) {
		t.Fatal(err)
	}
}

// TestFlareFailureReasons 验证服务错误只映射白名单原因，HTTP 500 同样保留分类而不泄露正文。
func TestFlareFailureReasons(t *testing.T) {
	for _, tc := range []struct{ message, want string }{
		{"Error solving the challenge. Timeout after 45.0 seconds.", "bypass_timeout"},
		{"Captcha detected but no automatic solver is configured.", "bypass_captcha_required"},
		{"net::ERR_PROXY_CONNECTION_FAILED", "bypass_proxy_failed"},
		{"net::ERR_TUNNEL_CONNECTION_FAILED", "bypass_proxy_failed"},
		{"session not created: Chrome failed to start", "bypass_browser_failed"},
		{"net::ERR_CONNECTION_RESET", "bypass_target_unavailable"},
		{"The session doesn't exist.", "bypass_session_failed"},
		{"unknown failure", "bypass_unavailable"},
	} {
		for _, status := range []int{200, 500} {
			t.Run(fmt.Sprintf("%s/%d", tc.want, status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": tc.message + " http://secret-user:secret-password@proxy.invalid Cookie: secret-cookie"})
				}))
				defer server.Close()
				c := NewClientWithBypass(nil, "flaresolverr", server.URL)
				_, err := c.bypass.get(context.Background(), "https://javdb.com/")
				if !errors.Is(err, ErrBypassUnavailable) || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-") {
					t.Fatalf("classification=%v want=%s", err, tc.want)
				}
				_, err = c.bypass.flareCommand(context.Background(), server.URL, map[string]any{"cmd": "sessions.create"})
				if !errors.Is(err, ErrBypassUnavailable) || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-") {
					t.Fatalf("session classification=%v want=%s", err, tc.want)
				}
			})
		}
	}
}

// TestBypassHTMLProtocol 验证旧增强服务使用 GET /html 而不是猜测的 POST /cookies。
func TestBypassHTMLProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/html" || r.URL.Query().Get("url") != "https://javdb.com/" {
			t.Errorf("wrong request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("X-Cf-Bypasser-Final-Url", "https://javdb.com/")
		w.Write([]byte("<html>valid</html>"))
	}))
	defer server.Close()
	c := NewClientWithBypass(nil, "cloudflare_bypass_for_scraping", server.URL)
	body, err := c.bypass.get(context.Background(), "https://javdb.com/")
	if err != nil || string(body) != "<html>valid</html>" {
		t.Fatalf("%q %v", body, err)
	}
	c.ConfigureBypass("scrapling", server.URL, false)
	if _, err = c.bypass.get(context.Background(), "https://javdb.com/"); !errors.Is(err, ErrBypassConfig) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.ConfigureBypass("flaresolverr", server.URL, false)
	if _, err = c.bypass.get(ctx, "https://javdb.com/"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// TestConfiguredProxyReachesEnhancer 验证系统代理进入远程浏览器参数，而不用于连接增强服务。
func TestConfiguredProxyReachesEnhancer(t *testing.T) {
	for _, engine := range []string{"flaresolverr", "cloudflare_bypass_for_scraping", ""} {
		for _, proxy := range []string{"", "http://proxy.example:7890"} {
			for _, useProxy := range []bool{false, true} {
				t.Run(engine+proxy, func(t *testing.T) {
					wantProxy := ""
					if useProxy {
						wantProxy = proxy
					}
					calls := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if engine == "flaresolverr" {
							var p struct{ Proxy *struct{ URL string } }
							if json.NewDecoder(r.Body).Decode(&p) != nil {
								t.Error("invalid json")
							}
							if wantProxy == "" && p.Proxy != nil || wantProxy != "" && (p.Proxy == nil || p.Proxy.URL != wantProxy) {
								t.Error("wrong proxy parameter")
							}
							w.Write([]byte(`{"status":"ok","solution":{"url":"https://javdb.com/","status":200,"response":"<html>valid</html>"}}`))
						} else {
							if r.URL.Query().Get("proxy") != wantProxy {
								t.Error("wrong query proxy")
							}
							w.Write([]byte("<html>valid</html>"))
						}
					}))
					defer server.Close()
					c, err := NewClientWithProxy(proxy)
					if err != nil {
						t.Fatal(err)
					}
					c.ConfigureBypass(engine, server.URL, useProxy)
					c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("cf-chl-test")), Request: r}, nil
					})
					body, err := c.Get(context.Background(), "https://javdb.com/")
					if engine == "" {
						if !errors.Is(err, ErrChallenge) || calls != 0 {
							t.Fatalf("disabled: %v calls=%d", err, calls)
						}
					} else if err != nil || calls != 1 || string(body) != "<html>valid</html>" {
						t.Fatalf("result: %q %v calls=%d", body, err, calls)
					}
				})
			}
		}
	}
}

// TestAuthenticatedFlareProxySession 验证鉴权代理在创建会话时发送，失败也销毁本次会话。
func TestAuthenticatedFlareProxySession(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var commands []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var p map[string]any
				json.NewDecoder(r.Body).Decode(&p)
				cmd, _ := p["cmd"].(string)
				commands = append(commands, cmd)
				switch cmd {
				case "sessions.create":
					proxy, ok := p["proxy"].(map[string]any)
					if !ok || proxy["url"] != "http://proxy.example:7890" || proxy["username"] != "user" || proxy["password"] != "pass@word" {
						t.Error("credentials not split into session proxy")
					}
					if p["session"] == nil {
						t.Error("missing owned session")
					}
					json.NewEncoder(w).Encode(map[string]any{"status": "ok", "session": p["session"]})
				case "request.get":
					if p["session"] == nil || p["proxy"] != nil {
						t.Error("request must use owned session")
					}
					if fail {
						w.WriteHeader(500)
						return
					}
					w.Write([]byte(`{"status":"ok","solution":{"url":"https://javdb.com/","status":200,"response":"<html>valid</html>"}}`))
				case "sessions.destroy":
					w.Write([]byte(`{"status":"ok"}`))
				default:
					t.Error("unknown command")
				}
			}))
			defer server.Close()
			c, err := NewClientWithProxy("http://user:pass%40word@proxy.example:7890")
			if err != nil {
				t.Fatal(err)
			}
			c.ConfigureBypass("flaresolverr", server.URL, true)
			_, err = c.bypass.get(context.Background(), "https://javdb.com/")
			if fail && !errors.Is(err, ErrBypassUnavailable) || !fail && err != nil {
				t.Fatal(err)
			}
			if strings.Join(commands, ",") != "sessions.create,request.get,sessions.destroy" {
				t.Fatalf("commands=%v", commands)
			}
		})
	}
}

// TestLiveEnhancedCollection 仅显式启用时验证所有候选解析器；不注册来源、不写入业务数据库。
type liveEnhancerFetcher struct{ client *bypassClient }

// Get 允许显式诊断对照远程浏览器，不改变生产环境仅 CF 才增强的规则。
func (f liveEnhancerFetcher) Get(ctx context.Context, target string) ([]byte, error) {
	return f.client.get(ctx, target)
}

func TestLiveEnhancedCollection(t *testing.T) {
	if os.Getenv("BYTEMUSE_LIVE_COLLECTION") != "1" || os.Getenv("BYTEMUSE_BYPASS_URL") == "" {
		t.Skip("需要显式增强实网核验")
	}
	c, err := NewClientWithProxy(os.Getenv("BYTEMUSE_COLLECTION_PROXY"))
	if err != nil {
		t.Fatal(err)
	}
	c.ConfigureBypass(os.Getenv("BYTEMUSE_BYPASS_ENGINE"), os.Getenv("BYTEMUSE_BYPASS_URL"), os.Getenv("BYTEMUSE_BYPASS_USE_PROXY") == "true")
	var fetcher Fetcher = c
	if os.Getenv("BYTEMUSE_LIVE_FORCE_ENHANCER") == "1" {
		fetcher = liveEnhancerFetcher{c.bypass}
	}
	for _, req := range []ports.CollectionRequest{
		{Source: "javdb", Kind: "rank", Period: "daily", Page: 1},
		{Source: "javlibrary", Kind: "rank", Period: "wanted", Page: 1},
		{Source: "avbase", Kind: "search", Query: "TEST", Page: 1},
		{Source: "javbus", Kind: "search", Query: "TEST", Page: 1},
		{Source: "jable", Kind: "search", Query: "TEST", Page: 1},
		{Source: "supjav", Kind: "search", Query: "TEST", Page: 1},
	} {
		t.Run(req.Source, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 85*time.Second)
			defer cancel()
			var b ports.CollectionBatch
			var err error
			if req.Source == "javdb" {
				b, err = collectJavDB(ctx, fetcher, req)
			} else {
				b, err = collectUnreleasedFixture(ctx, fetcher, req)
			}
			if err != nil {
				t.Fatalf("source=%s error=%v", req.Source, err)
			}
			t.Logf("source=%s items=%d has_more=%v", req.Source, len(b.Items), b.HasMore)
		})
	}
}
