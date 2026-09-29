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
