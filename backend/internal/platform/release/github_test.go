package release

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestVersionFallbackOrder 验证 raw、CDN、代理顺序、成功短路、缺少代理和取消停止。
func TestVersionFallbackOrder(t *testing.T) {
	for _, success := range []string{"raw", "cdn", "proxy", "none", "unconfigured", "cancel"} {
		t.Run(success, func(t *testing.T) {
			var calls []string
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			response := func(name string) (*http.Response, error) {
				calls = append(calls, name)
				if success == "cancel" {
					cancel()
					return nil, context.Canceled
				}
				if name != success {
					return nil, errors.New("connection failed")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"version":"0.1.101"}`)), Header: make(http.Header)}, nil
			}
			direct := &http.Client{Transport: packageTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "raw.githubusercontent.com" {
					return response("raw")
				}
				if r.URL.String() != "https://cdn.jsdelivr.net/gh/w2z/byte-muse-go@HEAD/version.json" {
					t.Errorf("cdn=%s", r.URL)
				}
				return response("cdn")
			})}
			source := NewGitHubSource("w2z/byte-muse-go", direct)
			source.ProxyClient = func(context.Context) (*http.Client, error) {
				if success == "unconfigured" {
					return nil, nil
				}
				return &http.Client{Transport: packageTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Host != "raw.githubusercontent.com" {
						t.Errorf("proxy target=%s", r.URL.Host)
					}
					return response("proxy")
				})}, nil
			}
			_, err := source.Latest(ctx)
			want := map[string]string{"raw": "raw", "cdn": "raw,cdn", "proxy": "raw,cdn,proxy", "none": "raw,cdn,proxy", "unconfigured": "raw,cdn", "cancel": "raw"}[success]
			if strings.Join(calls, ",") != want {
				t.Fatalf("calls=%v want=%s", calls, want)
			}
			if (err != nil) != (success == "none" || success == "unconfigured" || success == "cancel") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

// TestDirectIgnoresEnvironmentProxy 防止首轮直连被 HTTP_PROXY/HTTPS_PROXY 隐式改变。
func TestDirectIgnoresEnvironmentProxy(t *testing.T) {
	client := directClient()
	defer client.CloseIdleConnections()
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("直连不应使用环境代理")
	}
	factory := NewProxyClient(func(context.Context) (map[string]string, error) { return map[string]string{}, nil })
	if client, err := factory(context.Background()); err != nil || client != nil {
		t.Fatalf("未配置代理时应跳过: client=%v err=%v", client, err)
	}
}

// newTestSource 把发布源指向本地测试服务器，避免真实访问 GitHub。
func newTestSource(t *testing.T, handler http.HandlerFunc) *GitHubSource {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	source := NewGitHubSource("w2z/byte-muse-go", server.Client())
	source.endpoint = server.URL
	source.cdnEndpoint = ""
	return source
}

// TestGitHubSourceLatest 验证内容读取、请求形态和响应校验。
func TestGitHubSourceLatest(t *testing.T) {
	const body = `{"version":"0.1.22","commit":"abc123","built_at":"2026-09-29T12:00:00Z","source":"https://github.com/w2z/byte-muse-go"}`
	source := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/w2z/byte-muse-go/HEAD/version.json" {
			t.Errorf("请求路径=%s", r.URL.Path)
		}
		if r.Header.Get("Accept") != "text/plain" {
			t.Errorf("Accept=%s", r.Header.Get("Accept"))
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("缺少 User-Agent")
		}
		_, _ = w.Write([]byte(body))
	})
	info, err := source.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.1.22" || info.Commit != "abc123" || info.Source != "https://github.com/w2z/byte-muse-go" {
		t.Fatalf("info=%+v", info)
	}
}

// TestGitHubSourceFailures 验证上游异常一律转换为脱敏的本地原因，不回显上游正文。
func TestGitHubSourceFailures(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     int
		body       string
		wantPhrase string
	}{
		{name: "未找到", status: http.StatusNotFound, body: `{"message":"Not Found"}`, wantPhrase: "未找到 version.json"},
		{name: "限流", status: http.StatusForbidden, body: `{"message":"rate limit exceeded for 1.2.3.4"}`, wantPhrase: "GitHub 访问受限"},
		{name: "限流429", status: http.StatusTooManyRequests, body: `{}`, wantPhrase: "GitHub 访问受限"},
		{name: "服务端错误", status: http.StatusBadGateway, body: `upstream-secret-body`, wantPhrase: "GitHub 请求失败"},
		{name: "重定向不跟随", status: http.StatusFound, body: ``, wantPhrase: "GitHub 请求失败"},
		{name: "非JSON", status: http.StatusOK, body: `<html>secret</html>`, wantPhrase: "格式无效"},
		{name: "缺少版本号", status: http.StatusOK, body: `{"commit":"abc"}`, wantPhrase: "缺少版本号"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			})
			_, err := source.Latest(context.Background())
			if err == nil || !strings.Contains(err.Error(), testCase.wantPhrase) {
				t.Fatalf("err=%v", err)
			}
			for _, leak := range []string{"1.2.3.4", "secret"} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("错误信息泄露上游内容: %v", err)
				}
			}
		})
	}
}

// TestGitHubSourceRejectsInvalidRepo 验证仓库名不合法时直接失败，不发出请求。
func TestGitHubSourceRejectsInvalidRepo(t *testing.T) {
	for _, repo := range []string{"", "byte-muse-go", "w2z/byte-muse-go/extra", "w2z/../etc", "https://github.com/w2z/byte-muse-go"} {
		calls := 0
		source := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			_, _ = w.Write([]byte(`{"version":"0.1.22"}`))
		})
		source.repo = repo
		if _, err := source.Latest(context.Background()); err == nil {
			t.Fatalf("repo=%q 应当被拒绝", repo)
		}
		if calls != 0 {
			t.Fatalf("repo=%q 不应发出请求", repo)
		}
	}
}

// TestGitHubSourceTimeout 验证上游无响应时按本地超时收敛，错误信息为中文说明。
func TestGitHubSourceTimeout(t *testing.T) {
	source := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Latest(ctx); err == nil {
		t.Fatal("取消后应当返回错误")
	}
}

// TestReleaseProxy 验证检查更新与升级下载读取同一动态代理，保存后下一次请求生效。
func TestReleaseProxy(t *testing.T) {
	calls := []int{0, 0}
	servers := make([]*httptest.Server, 2)
	for i := range servers {
		index := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls[index]++
			if r.URL.Host != "release.example.test" {
				t.Errorf("host=%s", r.URL.Host)
			}
			_, _ = w.Write([]byte(`{"version":"0.1.101"}`))
		}))
		defer servers[i].Close()
	}
	selected := servers[0].URL
	factory := NewProxyClient(func(context.Context) (map[string]string, error) { return map[string]string{"PROXY": selected}, nil })
	client, err := factory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	source := NewGitHubSource("w2z/byte-muse-go", client)
	source.endpoint = "http://release.example.test"
	source.cdnEndpoint = ""
	if _, err := source.Latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	selected = servers[1].URL
	client, err = factory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err := downloadBytes(context.Background(), client, "http://release.example.test/package.sha256", 1024); err != nil {
		t.Fatal(err)
	}
	if calls[0] != 1 || calls[1] != 1 {
		t.Fatalf("proxy calls=%v", calls)
	}
	selected = "file:///invalid-proxy"
	if _, err := factory(context.Background()); err == nil {
		t.Fatal("无效代理不应静默直连")
	}
}
