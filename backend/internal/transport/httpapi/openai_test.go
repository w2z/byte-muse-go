package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/auth"
)

// TestOpenAIProbeHTTP 验证鉴权、草稿直测、上游失败与空响应，测试不依赖设置仓库。
func TestOpenAIProbeHTTP(t *testing.T) {
	a, err := auth.New(auth.Config{Username: "test-admin", Password: "test-password", Secret: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	h := New(Dependencies{Auth: a})
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"test-admin","password":"test-password"}`)))
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"成功", 200, `{"choices":[{"message":{"content":"OK"}}]}`, 200},
		{"密钥错误", 401, `secret-example-key`, 502},
		{"限流", 429, `{}`, 502},
		{"空选项", 200, `{"choices":[]}`, 502},
		{"空文本", 200, `{"choices":[{"message":{"content":"  "}}]}`, 502},
		{"非JSON", 200, `<html>secret-example-key</html>`, 502},
		{"重定向", 302, `{}`, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				time.Sleep(15 * time.Millisecond)
				if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret-example-key" {
					t.Error("错误的上游请求")
				}
				var body struct {
					Model    string `json:"model"`
					Messages []any  `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "draft-model" || len(body.Messages) == 0 {
					t.Error("未使用草稿模型或未发送对话")
				}
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			raw, _ := json.Marshal(map[string]string{"url": upstream.URL + "/v1/", "model": "draft-model", "api_key": "secret-example-key"})
			req := httptest.NewRequest("POST", "/api/v1/system/settings/openai/test", strings.NewReader(string(raw)))
			for _, cookie := range login.Result().Cookies() {
				req.AddCookie(cookie)
			}
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			if out.Code != tc.want || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", out.Code, calls, out.Body)
			}
			if strings.Contains(out.Body.String(), "secret-example-key") {
				t.Fatal("泄露密钥")
			}
			var result struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			state := "失败"
			if tc.want == 200 {
				state = "成功"
			}
			suffix := "$"
			if tc.want != 200 {
				suffix = "：.+$"
			}
			match := regexp.MustCompile(`^OpenAI 连接` + state + ` [(]([0-9]+)ms[)]` + suffix).FindStringSubmatch(result.Message)
			if len(match) != 2 {
				t.Fatal(result.Message)
			}
			elapsed, _ := strconv.Atoi(match[1])
			if tc.name == "密钥错误" && !strings.Contains(result.Message, "鉴权失败（HTTP 401）") {
				t.Fatal(result.Message)
			}
			if elapsed < 15 {
				t.Fatalf("耗时未覆盖上游请求: %d", elapsed)
			}
		})
	}
	for _, raw := range []string{`{}`, `{"url":"file:///tmp","model":"x","api_key":"x"}`, `{"url":"https://example.com/v1?key=secret","model":"x","api_key":"x"}`, `{"url":"https://example.com","model":"x","api_key":""}`} {
		req := httptest.NewRequest("POST", "/api/v1/system/settings/openai/test", strings.NewReader(raw))
		for _, cookie := range login.Result().Cookies() {
			req.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != 400 {
			t.Fatalf("invalid status=%d", out.Code)
		}
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, httptest.NewRequest("POST", "/api/v1/system/settings/openai/test", strings.NewReader(`{}`)))
	if out.Code != 401 {
		t.Fatalf("unauthenticated=%d", out.Code)
	}
}
