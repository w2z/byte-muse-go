package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"bytemuse/backend/internal/auth"
)

func TestLoginSetsSecureSessionCookieAndProtectedRoutesRequireIt(t *testing.T) {
	handler := newHandler(t, true)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401; body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	assertJSONField(t, unauthorized, "code", "unauthorized")

	login := performJSON(handler, http.MethodPost, "/api/v1/auth/login", `{"username":"operator","password":"correct-password"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "bytemuse_session" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || !cookie.Secure {
		t.Fatalf("session cookie = %+v, want HttpOnly Secure SameSite=Lax", cookie)
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want 200; body=%s", authorized.Code, authorized.Body.String())
	}
}

// 不勾选“记住密码”的会话 Cookie 保留 1 天，勾选后保留 30 天。
func TestLoginRememberExtendsSessionCookie(t *testing.T) {
	handler := newHandler(t, true)

	plain := performJSON(handler, http.MethodPost, "/api/v1/auth/login", `{"username":"operator","password":"correct-password"}`, nil)
	if plain.Code != http.StatusOK || len(plain.Result().Cookies()) != 1 {
		t.Fatalf("login without remember: status=%d body=%s", plain.Code, plain.Body.String())
	}
	plainCookie := plain.Result().Cookies()[0]

	remembered := performJSON(handler, http.MethodPost, "/api/v1/auth/login", `{"username":"operator","password":"correct-password","remember":true}`, nil)
	if remembered.Code != http.StatusOK || len(remembered.Result().Cookies()) != 1 {
		t.Fatalf("login with remember: status=%d body=%s", remembered.Code, remembered.Body.String())
	}
	rememberedCookie := remembered.Result().Cookies()[0]

	// MaxAge 由过期时间减去签发时刻取整得到，留 10 秒余量避免取整边界抖动。
	if plainCookie.MaxAge < 3600-10 || plainCookie.MaxAge > 3600 {
		t.Fatalf("cookie without remember MaxAge = %d, want about 3600", plainCookie.MaxAge)
	}
	if rememberedCookie.MaxAge < 30*3600-10 || rememberedCookie.MaxAge > 30*3600 {
		t.Fatalf("cookie with remember MaxAge = %d, want about %d", rememberedCookie.MaxAge, 30*3600)
	}
}

// 会话过期但仍在宽限窗口内时，/auth/refresh 续签 Cookie；超出宽限返回 401。
func TestRefreshRenewsExpiredSessionWithinGrace(t *testing.T) {
	clock := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	service, err := auth.New(auth.Config{
		Username: "operator",
		Password: "correct-password",
		Secret:   "0123456789abcdef0123456789abcdef",
		TTL:      time.Hour,
		// 与产品默认值保持同一比例：不勾选 1 天，勾选 30 天，过期宽限 1 天。
		RememberTTL:  30 * time.Hour,
		RefreshGrace: 24 * time.Hour,
		Secure:       true,
		Now:          func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	handler := newHandlerWithAuth(t, true, service)

	login := performJSON(handler, http.MethodPost, "/api/v1/auth/login", `{"username":"operator","password":"correct-password","remember":true}`, nil)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login status = %d body=%s", login.Code, login.Body.String())
	}
	expiredCookie := login.Result().Cookies()[0]

	// 过期 1 小时后：受保护接口拒绝，但仍可续签。
	clock = clock.Add(31 * time.Hour)
	rejected := httptest.NewRecorder()
	rejectedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	rejectedRequest.AddCookie(expiredCookie)
	handler.ServeHTTP(rejected, rejectedRequest)
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status = %d, want 401; body=%s", rejected.Code, rejected.Body.String())
	}

	refreshed := performJSONWithCookie(handler, http.MethodPost, "/api/v1/auth/refresh", "", nil, expiredCookie)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body=%s", refreshed.Code, refreshed.Body.String())
	}
	cookies := refreshed.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("refresh cookies = %d, want 1", len(cookies))
	}
	renewed := cookies[0]
	if !renewed.HttpOnly || !renewed.Secure || renewed.SameSite != http.SameSiteLaxMode {
		t.Fatalf("renewed cookie = %+v, want HttpOnly Secure SameSite=Lax", renewed)
	}
	// 续签沿用“记住密码”档位，有效期回到满额 30 小时。
	if renewed.MaxAge < 30*3600-10 || renewed.MaxAge > 30*3600 {
		t.Fatalf("renewed cookie MaxAge = %d, want about %d", renewed.MaxAge, 30*3600)
	}
	var body struct {
		User auth.User `json:"user"`
	}
	decodeJSON(t, refreshed, &body)
	if body.User.Username != "operator" {
		t.Fatalf("refresh user = %+v, want configured operator", body.User)
	}

	// 新 Cookie 立即可用于受保护接口。
	authorized := httptest.NewRecorder()
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	authorizedRequest.AddCookie(renewed)
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("renewed session status = %d, want 200; body=%s", authorized.Code, authorized.Body.String())
	}

	// 超出宽限窗口后连续签也拒绝。
	clock = clock.Add(30*time.Hour + 24*time.Hour + time.Second)
	beyond := performJSONWithCookie(handler, http.MethodPost, "/api/v1/auth/refresh", "", nil, renewed)
	if beyond.Code != http.StatusUnauthorized {
		t.Fatalf("refresh beyond grace status = %d, want 401; body=%s", beyond.Code, beyond.Body.String())
	}
	if len(beyond.Result().Cookies()) != 0 {
		t.Fatal("refresh beyond grace set a session cookie")
	}

	// 没有 Cookie 的续签请求返回 401 而不是落进需要登录的中间件。
	anonymous := performJSON(handler, http.MethodPost, "/api/v1/auth/refresh", "", nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous refresh status = %d, want 401; body=%s", anonymous.Code, anonymous.Body.String())
	}
	assertJSONField(t, anonymous, "code", "unauthorized")
}

func TestLoginRejectsInvalidCredentialsWithoutCookie(t *testing.T) {
	handler := newHandler(t, true)
	response := performJSON(handler, http.MethodPost, "/api/v1/auth/login", `{"username":"operator","password":"wrong-password"}`, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401", response.Code)
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("invalid login set a session cookie")
	}
}

func TestDashboardAndSystemSettingsMatchOpenAPI(t *testing.T) {
	handler := newHandler(t, true)
	cookie := loginCookie(t, handler)

	for path, fields := range map[string][]string{
		"/api/v1/dashboard":       {"active_subscriptions", "completed_downloads", "media_count", "healthy_integrations"},
		"/api/v1/system/settings": {"database_driver", "demo_seed_enabled"},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(cookie)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d; body=%s", path, response.Code, response.Body.String())
		}
		var body map[string]any
		decodeJSON(t, response, &body)
		for _, field := range fields {
			if _, ok := body[field]; !ok {
				t.Fatalf("GET %s missing %q: %+v", path, field, body)
			}
		}
	}
}

func TestEventsWritesReadyEventAndExitsOnContextCancellation(t *testing.T) {
	handler := newHandler(t, true)
	cookie := loginCookie(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	request.AddCookie(cookie)
	response := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-response.flushed:
	case <-time.After(time.Second):
		t.Fatal("SSE did not flush ready event")
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("SSE content type = %q, want text/event-stream", got)
	}
	if body := response.String(); !strings.Contains(body, "event: ready") {
		t.Fatalf("SSE body = %q, want ready event", body)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not exit after context cancellation")
	}
}

func loginCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := performJSON(handler, http.MethodPost, "/api/v1/auth/login", `{"username":"operator","password":"correct-password"}`, nil)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("login failed: status=%d body=%s", response.Code, response.Body.String())
	}
	return response.Result().Cookies()[0]
}

func testAuthService(t *testing.T) *auth.Service {
	t.Helper()
	service, err := auth.New(auth.Config{
		Username: "operator",
		Password: "correct-password",
		Secret:   "0123456789abcdef0123456789abcdef",
		TTL:      time.Hour,
		// 与产品默认值保持同一比例：不勾选 1 天，勾选 30 天。
		RememberTTL: 30 * time.Hour,
		Secure:      true,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	return service
}

type flushRecorder struct {
	header  http.Header
	body    bytes.Buffer
	mu      sync.Mutex
	flushed chan struct{}
	once    sync.Once
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{header: make(http.Header), flushed: make(chan struct{})}
}
func (r *flushRecorder) Header() http.Header { return r.header }
func (r *flushRecorder) WriteHeader(int)     {}
func (r *flushRecorder) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(data)
}
func (r *flushRecorder) Flush() { r.once.Do(func() { close(r.flushed) }) }
func (r *flushRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}
