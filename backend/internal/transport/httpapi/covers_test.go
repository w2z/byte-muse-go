package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/platform/covercache"
)

// coverPNG 是最小 PNG 文件头，用于验证封面路由按图片真实格式落盘并原样返回。
var coverPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// newCoverHandler 返回登录后的封面路由、会话 Cookie 与缓存目录，测试不依赖数据库。
func newCoverHandler(t *testing.T) (http.Handler, []*http.Cookie, string) {
	t.Helper()
	authService, err := auth.New(auth.Config{Username: "test-admin", Password: "test-password", Secret: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	handler := New(Dependencies{Auth: authService, Covers: covercache.New(dir, nil)})
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"test-admin","password":"test-password"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", login.Code, login.Body.String())
	}
	return handler, login.Result().Cookies(), dir
}

// coverRequest 构造带会话的封面请求。
func coverRequest(target string, cookies []*http.Cookie) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return request
}

// TestCoverRouteCachesToDiskAndRequiresSession 验证封面路由要求登录会话，
// 首次请求把图片按番号落盘，再次请求直接读本地文件且不再访问源站。
func TestCoverRouteCachesToDiskAndRequiresSession(t *testing.T) {
	requests := 0
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(coverPNG)
	}))
	defer source.Close()

	handler, cookies, dir := newCoverHandler(t)
	target := "/api/v1/covers/SONS-1223?source=" + url.QueryEscape(source.URL+"/cover.jpg")

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, target, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("未登录状态码=%d", unauthorized.Code)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, coverRequest(target, cookies))
		if response.Code != http.StatusOK {
			t.Fatalf("第 %d 次状态码=%d body=%s", attempt, response.Code, response.Body.String())
		}
		if contentType := response.Header().Get("Content-Type"); contentType != "image/png" {
			t.Fatalf("第 %d 次 Content-Type=%q", attempt, contentType)
		}
		if !bytes.Equal(response.Body.Bytes(), coverPNG) {
			t.Fatalf("第 %d 次返回内容不是源图片", attempt)
		}
	}
	if requests != 1 {
		t.Fatalf("源站请求次数=%d，期望 1（第二次应命中本地缓存）", requests)
	}
	body, err := os.ReadFile(filepath.Join(dir, "sons-1223.png"))
	if err != nil || !bytes.Equal(body, coverPNG) {
		t.Fatalf("本地封面文件不符: %v", err)
	}
}

// TestCoverRouteFallsBackToSource 验证源地址非法时返回 400，源站不可用时 302 回退到源地址，
// 缓存故障不会让封面消失，也不会落盘任何文件。
func TestCoverRouteFallsBackToSource(t *testing.T) {
	handler, cookies, dir := newCoverHandler(t)
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, coverRequest("/api/v1/covers/SONS-1223?source="+url.QueryEscape("/relative/cover.jpg"), cookies))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("相对地址状态码=%d", invalid.Code)
	}
	// 未监听的本地端口：下载必然失败，路由必须回退到源地址。
	source := "http://127.0.0.1:1/cover.jpg"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, coverRequest("/api/v1/covers/SONS-1223?source="+url.QueryEscape(source), cookies))
	if response.Code != http.StatusFound || response.Header().Get("Location") != source {
		t.Fatalf("回退状态码=%d Location=%q", response.Code, response.Header().Get("Location"))
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("失败请求不应落盘: %v %v", entries, err)
	}
}
