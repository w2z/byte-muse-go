package application

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
)

type refreshingDownloadStub struct {
	strmPan115Stub
	calls int
	links []string
}

func (stub *refreshingDownloadStub) PlayURL(_ context.Context, _, userAgent string) (string, error) {
	stub.calls++
	if len(stub.links) > 0 {
		return stub.links[min(stub.calls-1, len(stub.links)-1)], nil
	}
	return fmt.Sprintf("http://download.test/%d?ua=%s", stub.calls, userAgent), nil
}

// TestStrmDownloadChecksLinkExpiry 验证过期直链不发起下载、刷新次数有界，缺失或非法时间戳不误拦截。
func TestStrmDownloadChecksLinkExpiry(t *testing.T) {
	expired := "http://download.test/stale?t=1"
	fresh := fmt.Sprintf("http://download.test/fresh?t=%d", time.Now().Add(time.Hour).Unix())
	for _, sample := range []struct {
		name         string
		links        []string
		wantCalls    int
		wantRequests int
		wantSuccess  bool
	}{
		{"refresh-before-request", []string{expired, fresh}, 2, 1, true},
		{"fresh", []string{fresh}, 1, 1, true},
		{"still-expired", []string{expired}, 2, 0, false},
		{"missing-timestamp", []string{"http://download.test/fresh"}, 1, 1, true},
		{"invalid-timestamp", []string{"http://download.test/fresh?t=unknown"}, 1, 1, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			root := t.TempDir()
			api := &refreshingDownloadStub{links: sample.links}
			service := newStrmTestService(t, root, api, nil, nil)
			requests := 0
			service.http.Transport = embyRoundTripper(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.URL.Path == "/stale" {
					t.Error("expired URL must never reach download server")
				}
				return &http.Response{StatusCode: 200, ContentLength: 7, Body: io.NopCloser(strings.NewReader("renewed"))}, nil
			})
			_, err := service.downloadStrmMedia(context.Background(), root, root, "115", strmSourceFile{Name: "poster.jpg", PickCode: "poster"}, domain.StrmGenerateFull)
			if (err == nil) != sample.wantSuccess || api.calls != sample.wantCalls || requests != sample.wantRequests {
				t.Fatalf("err=%v resolve=%d requests=%d", err, api.calls, requests)
			}
			entries, readErr := os.ReadDir(root)
			wantFiles := 0
			if sample.wantSuccess {
				wantFiles = 1
			}
			if readErr != nil || len(entries) != wantFiles {
				t.Fatalf("files=%v err=%v", entries, readErr)
			}
		})
	}
}

// TestStrmDownloadRefreshExpiredLink 验证仅明确过期时重新取链一次，取消和普通拒绝不会反复取链，失败保留旧文件。
func TestStrmDownloadRefreshExpiredLink(t *testing.T) {
	for _, sample := range []struct {
		name          string
		body          string
		alwaysExpired bool
		cancel        bool
		wantCalls     int
		wantSuccess   bool
	}{
		{name: "expired-then-success", body: `{"message":"request expired"}`, wantCalls: 2, wantSuccess: true},
		{name: "still-expired", body: `{"message":"request expired"}`, alwaysExpired: true, wantCalls: 2},
		{name: "permission-denied", body: `{"message":"access denied"}`, wantCalls: 1},
		{name: "unclassified-forbidden", body: `<html>request expired</html>`, wantCalls: 1},
		{name: "cancelled", body: `{"message":"request expired"}`, cancel: true, wantCalls: 1},
	} {
		t.Run(sample.name, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "poster.jpg")
			if err := os.WriteFile(destination, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			api := &refreshingDownloadStub{}
			service := newStrmTestService(t, root, api, nil, nil)
			requests := 0
			service.http.Transport = embyRoundTripper(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.URL.Path != fmt.Sprintf("/%d", requests) || request.UserAgent() != strmDownloadUserAgent {
					t.Errorf("must use newly resolved URL and consistent UA")
				}
				if sample.cancel {
					cancel()
				}
				if requests == 1 || sample.alwaysExpired {
					return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(sample.body))}, nil
				}
				return &http.Response{StatusCode: 200, ContentLength: 7, Body: io.NopCloser(strings.NewReader("renewed"))}, nil
			})
			skipped, err := service.downloadStrmMedia(ctx, root, root, "115", strmSourceFile{Name: "poster.jpg", PickCode: "poster"}, domain.StrmGenerateFull)
			if skipped || (err == nil) != sample.wantSuccess || api.calls != sample.wantCalls || requests != sample.wantCalls {
				t.Fatalf("skipped=%v err=%v resolve=%d requests=%d", skipped, err, api.calls, requests)
			}
			want := "original"
			if sample.wantSuccess {
				want = "renewed"
			}
			body, readErr := os.ReadFile(destination)
			if readErr != nil || string(body) != want {
				t.Fatalf("file=%q err=%v", body, readErr)
			}
			entries, readErr := os.ReadDir(root)
			if readErr != nil || len(entries) != 1 {
				t.Fatalf("temporary files leaked: %v %v", entries, readErr)
			}
		})
	}
}
