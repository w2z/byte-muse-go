package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/ports"
)

// fakeReleaseSource 是发布源的测试替身，避免接口测试访问 GitHub。
type fakeReleaseSource struct {
	info ports.ReleaseInfo
	err  error
}

func (s fakeReleaseSource) Latest(context.Context) (ports.ReleaseInfo, error) { return s.info, s.err }

// TestSystemVersionRefresh 验证手动检查绕过缓存，失败可重试，非法参数不会触发检查。
func TestSystemVersionRefresh(t *testing.T) {
	source := &fakeReleaseSource{info: ports.ReleaseInfo{Version: "0.1.21"}}
	handler := systemVersion(application.NewVersionService("0.1.21", source))
	check := func(query string, code int, latest string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/system/version"+query, nil))
		if response.Code != code {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
		if code != 200 {
			return
		}
		var result application.VersionStatus
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Latest != latest {
			t.Fatalf("latest=%q want=%q", result.Latest, latest)
		}
	}
	check("", 200, "0.1.21")
	source.info.Version = "0.1.22"
	check("", 200, "0.1.21")
	check("?refresh=true", 200, "0.1.22")
	check("?refresh=invalid", 400, "")
	source.err = errors.New("检查更新超时")
	check("?refresh=true", 200, "")
	source.err = nil
	source.info.Version = "0.1.23"
	check("?refresh=true", 200, "0.1.23")
	check("", 200, "0.1.23")
}

// TestSystemUpgradeHTTP 验证升级接口鉴权、请求格式及无启动器环境不会误报受理成功。
func TestSystemUpgradeHTTP(t *testing.T) {
	authService, err := auth.New(auth.Config{Username: "test-admin", Password: "test-password", Secret: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{Auth: authService})
	for _, method := range []string{"GET", "POST"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/api/v1/system/upgrade", nil))
		if response.Code != 401 {
			t.Fatalf("method=%s status=%d", method, response.Code)
		}
	}
	for _, tc := range []struct {
		body, contentType string
		status            int
	}{
		{`{"target":"0.1.22"}`, "application/json", 503},
		{`{"target":"0.1.22"}`, "text/plain", 400},
		{`{"target":"0.1.22"} {}`, "application/json", 400},
		{`{"target":"0.1.22","url":"https://example.com/evil"}`, "application/json", 400},
	} {
		request := httptest.NewRequest("POST", "/system/upgrade", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", tc.contentType)
		response := httptest.NewRecorder()
		systemUpgrade(nil).ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("body=%s status=%d", tc.body, response.Code)
		}
	}
}

// TestSystemVersionHTTP 验证鉴权、可更新提示、失败降级和未装配发布源时的 503。
func TestSystemVersionHTTP(t *testing.T) {
	authService, err := auth.New(auth.Config{Username: "test-admin", Password: "test-password", Secret: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	newHandler := func(source ports.ReleaseSource) http.Handler {
		return New(Dependencies{Auth: authService, Version: application.NewVersionService("0.1.21", source)})
	}
	login := httptest.NewRecorder()
	newHandler(fakeReleaseSource{}).ServeHTTP(login, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"test-admin","password":"test-password"}`)))
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookies := login.Result().Cookies()

	for _, testCase := range []struct {
		name       string
		source     ports.ReleaseSource
		wantStatus int
		wantUpdate bool
		wantLatest string
		wantError  string
	}{
		{name: "发现新版本", source: fakeReleaseSource{info: ports.ReleaseInfo{Version: "0.1.22", Source: "https://github.com/w2z/byte-muse-go"}}, wantStatus: 200, wantUpdate: true, wantLatest: "0.1.22"},
		{name: "已是最新", source: fakeReleaseSource{info: ports.ReleaseInfo{Version: "0.1.21"}}, wantStatus: 200, wantLatest: "0.1.21"},
		{name: "远端失败仍返回当前版本", source: fakeReleaseSource{err: errors.New("无法访问 GitHub 发布仓库，请检查服务所在网络的连通性")}, wantStatus: 200, wantError: "无法访问 GitHub 发布仓库"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v1/system/version", nil)
			for _, cookie := range cookies {
				request.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			newHandler(testCase.source).ServeHTTP(response, request)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			var result application.VersionStatus
			if json.Unmarshal(response.Body.Bytes(), &result) != nil {
				t.Fatal(response.Body.String())
			}
			if result.Current != "0.1.21" || result.HasUpdate != testCase.wantUpdate || result.Latest != testCase.wantLatest {
				t.Fatalf("result=%+v", result)
			}
			if !strings.Contains(result.CheckError, testCase.wantError) {
				t.Fatalf("check_error=%q", result.CheckError)
			}
		})
	}

	request := httptest.NewRequest("GET", "/api/v1/system/version", nil)
	response := httptest.NewRecorder()
	newHandler(fakeReleaseSource{}).ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatalf("未登录状态码=%d", response.Code)
	}

	notReady := httptest.NewRecorder()
	request = httptest.NewRequest("GET", "/api/v1/system/version", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	New(Dependencies{Auth: authService}).ServeHTTP(notReady, request)
	if notReady.Code != 503 {
		t.Fatalf("未装配发布源状态码=%d", notReady.Code)
	}
}
