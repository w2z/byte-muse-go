package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestSource 把发布源指向本地测试服务器，避免真实访问 GitHub。
func newTestSource(t *testing.T, handler http.HandlerFunc) *GitHubSource {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	source := NewGitHubSource("w2z/byte-muse-go", server.Client())
	source.endpoint = server.URL
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
