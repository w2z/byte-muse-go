package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
)

// strmPan115PlayStub 只实现播放解析，用于验证免会话的 strm 播放路由。
type strmPan115PlayStub struct {
	fileID string
}

func (s *strmPan115PlayStub) Files(context.Context, string, int, int) (domain.Pan115FilePage, error) {
	return domain.Pan115FilePage{}, nil
}

func (s *strmPan115PlayStub) PlayURL(_ context.Context, fileID, _ string) (string, error) {
	s.fileID = fileID
	return "https://115.example/direct", nil
}

// TestStrmPlayRouteIsPublicAndRedirects 验证 /files/play/ 免会话可访问，
// 且 115 的标识按路径段还原、CloudDrive2 的多级目录路径不被拆断。
func TestStrmPlayRouteIsPublicAndRedirects(t *testing.T) {
	stub := &strmPan115PlayStub{}
	service, err := application.NewStrmService(t.TempDir(), stub, nil, func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{Strm: service})

	for _, tc := range []struct{ target, wantID string }{
		{"/files/play/115/f1", "f1"},
		{"/files/play/115/1234567890", "1234567890"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.target, nil))
		if recorder.Code != http.StatusFound {
			t.Fatalf("%s 返回 %d，期望 302：%s", tc.target, recorder.Code, recorder.Body.String())
		}
		if location := recorder.Header().Get("Location"); location != "https://115.example/direct" {
			t.Fatalf("%s 跳转地址不符: %s", tc.target, location)
		}
		if stub.fileID != tc.wantID {
			t.Fatalf("%s 解析出的文件标识为 %q，期望 %q", tc.target, stub.fileID, tc.wantID)
		}
	}
}

// TestStrmRoutesRequireSession 验证设置页使用的 strm 管理接口仍然要求登录会话。
func TestStrmRoutesRequireSession(t *testing.T) {
	handler := New(Dependencies{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/strm/directories", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问 strm 管理接口返回 %d，期望 401", recorder.Code)
	}
}

// TestStrmDirectoryHandlerRejectsEscape 验证本地目录浏览无法越出 strm 根目录。
func TestStrmDirectoryHandlerRejectsEscape(t *testing.T) {
	service, err := application.NewStrmService(t.TempDir(), nil, nil, func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	// 直接调用 handler 绕过会话中间件，聚焦路径解析本身。
	listStrmDirectories(service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/strm/directories?path=/../../", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("越界路径应被收敛到根目录内，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	var page domain.StrmDirectoryPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Path != "/" {
		t.Fatalf("越界路径收敛结果不符: %+v", page)
	}
}

// strmPan115InvalidStub 让播放解析返回 115 的「文件标识无效」错误。
type strmPan115InvalidStub struct{}

func (s *strmPan115InvalidStub) Files(context.Context, string, int, int) (domain.Pan115FilePage, error) {
	return domain.Pan115FilePage{}, nil
}

func (s *strmPan115InvalidStub) PlayURL(context.Context, string, string) (string, error) {
	return "", pan115.ErrInvalidFileID
}

// TestStrmPlayRejectsInvalidFileID 验证 115 判定为非法的文件标识返回 400，
// 而不是把它当作未知故障返回 500。
func TestStrmPlayRejectsInvalidFileID(t *testing.T) {
	service, err := application.NewStrmService(t.TempDir(), &strmPan115InvalidStub{}, nil, func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{Strm: service})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/files/play/115/abc", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法文件标识返回 %d，期望 400：%s", recorder.Code, recorder.Body.String())
	}
}
