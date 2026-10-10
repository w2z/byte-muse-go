package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bytemuse/backend/internal/platform/clouddrive"
	"bytemuse/backend/internal/platform/pan115"

	"bytemuse/backend/internal/domain"
)

// TestStrmDownloadRateLimitClassification 验证业务限流和 HTTP 429 保留类型，普通 403 不冒充限流。
func TestStrmDownloadRateLimitClassification(t *testing.T) {
	for _, sample := range []struct {
		err  error
		want bool
	}{
		{&pan115.APIError{Code: 406, Message: "已达到当前访问上限"}, true},
		{&strmRateLimitedDownloadError{errors.New("媒体下载返回状态码 429")}, true},
		{&strmExpiredDownloadError{errors.New("request expired")}, false},
		{errors.New("媒体下载返回状态码 403"), false},
	} {
		if got := pan115.IsRateLimitError(sample.err); got != sample.want {
			t.Fatalf("error=%v rateLimit=%v", sample.err, got)
		}
	}
}

// TestStrmDownloadResponseError checks API diagnostics, bounded reads and non-JSON fallbacks.
func TestStrmDownloadResponseError(t *testing.T) {
	for _, sample := range []struct{ body, want string }{
		{`{"message":"请求过于频繁"}`, "媒体下载返回状态码 429：请求过于频繁"},
		{`{"error":"连接失效"}`, "媒体下载返回状态码 429：连接失效"},
		{`<html>secret</html>`, "媒体下载返回状态码 429"},
		{strings.Repeat("a", 8193), "媒体下载返回状态码 429"},
	} {
		response := &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(sample.body))}
		if got := strmDownloadResponseError(response).Error(); got != sample.want {
			t.Fatalf("got %q want %q", got, sample.want)
		}
	}
}

// downloadPan115Stub 将真实下载引向测试 HTTP 服务；并发解析不修改共享字段。
type downloadPan115Stub struct {
	strmPan115Stub
	address string
}

func (s *downloadPan115Stub) PlayURL(_ context.Context, code, ua string) (string, error) {
	return s.address + "/" + code, nil
}

// TestStrmProgressExcludesDownloads covers both drives, empty video sets, skips and failed downloads.
func TestStrmProgressExcludesDownloads(t *testing.T) {
	for _, kind := range []string{"115", "cd2"} {
		for _, videos := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/%d", kind, videos), func(t *testing.T) {
				files := []domain.Pan115File{{ID: "poster", Name: "poster.jpg", PickCode: "poster"}}
				entries := []clouddrive.Entry{{Name: "poster.jpg", FullPath: "/root/poster.jpg"}}
				for index := 0; index < videos; index++ {
					name := fmt.Sprintf("movie%d.mp4", index)
					files = append(files, domain.Pan115File{ID: name, Name: name, PickCode: name})
					entries = append(entries, clouddrive.Entry{Name: name, FullPath: "/root/" + name})
				}
				api := &downloadPan115Stub{address: "http://download.test", strmPan115Stub: strmPan115Stub{pages: map[string]domain.Pan115FilePage{"/root": {Files: files}}}}
				cloud := &strmCloudStub{configured: true, entries: map[string][]clouddrive.Entry{"/root": entries}, download: clouddrive.Download{URL: "http://download.test/poster"}}
				service := newStrmTestService(t, t.TempDir(), api, cloud, map[string]string{"STRM_DOWNLOAD_ENABLE": "true", "STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{{Kind: kind, ID: "/root", Path: "/root", LocalPath: "/out"}})})
				status := http.StatusOK
				service.http.Transport = embyRoundTripper(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("poster"))}, nil
				})
				for attempt, mode := range []domain.StrmGenerateMode{domain.StrmGenerateFull, domain.StrmGenerateIncremental, domain.StrmGenerateFull} {
					if attempt == 2 {
						status = http.StatusTooManyRequests
					}
					var progress ScanProgress
					ctx := WithScanProgress(context.Background(), func(update ScanProgress) {
						progress = update
						if update.Total > videos || update.Processed > update.Total {
							t.Errorf("invalid video progress: %+v", update)
						}
					})
					ctx = context.WithValue(ctx, strmRetryKey{}, &strmRetryPolicy{wait: func(context.Context, string, error) error { status = http.StatusOK; return nil }})
					result, err := service.Scan(ctx, "http://play.test", mode)
					if err != nil || progress.Total != videos || progress.Processed != videos || progress.Percent != 100 {
						t.Fatalf("progress=%+v err=%v", progress, err)
					}
					if (attempt == 0 && result.Downloaded != 1) || (attempt == 1 && result.DownloadSkipped != 1) || (attempt == 2 && (result.DownloadFailed != 0 || result.Downloaded != 1)) {
						t.Fatalf("attempt=%d result=%+v", attempt, result)
					}
				}
			})
		}
	}
}

// TestStrmMediaDownload 验证开关、默认后缀、目录、增量跳过及视频体积过滤的独立性。
func TestStrmMediaDownload(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, "media-content") }))
	defer server.Close()
	api := &downloadPan115Stub{address: server.URL, strmPan115Stub: strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"1": {Files: []domain.Pan115File{{ID: "2", Name: "影片", IsDirectory: true}}},
		"2": {Files: []domain.Pan115File{{ID: "3", Name: "movie.mp4", PickCode: "video", Size: 200 * 1024 * 1024}, {ID: "4", Name: "poster.JPG", PickCode: "poster", Size: 3}, {ID: "5", Name: "movie.nfo", PickCode: "nfo", Size: 3}, {ID: "6", Name: "skip.txt", PickCode: "txt"}}},
	}}}
	page := api.pages["2"]
	for _, ext := range []string{"srt", "ssa", "ass", "png"} {
		page.Files = append(page.Files, domain.Pan115File{ID: ext, Name: "movie." + ext, PickCode: ext, Size: 5})
	}
	page.Files = append(page.Files, domain.Pan115File{ID: "jpeg", Name: "skip.jpeg", PickCode: "jpeg"})
	api.pages["2"] = page
	values := map[string]string{"STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "1", Path: "/源", LocalPath: "/movies", MinSizeMB: 100}})}
	root := t.TempDir()
	svc := newStrmTestService(t, root, api, nil, values)
	if _, err := svc.Scan(context.Background(), "http://example.test", domain.StrmGenerateIncremental); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("关闭时不应下载")
	}
	values["STRM_DOWNLOAD_ENABLE"] = "true"
	var progress ScanProgress
	ctx := WithScanProgress(context.Background(), func(update ScanProgress) { progress = update })
	result, err := svc.Scan(ctx, "http://example.test", domain.StrmGenerateIncremental)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"poster.JPG", "movie.nfo", "movie.srt", "movie.ssa", "movie.ass", "movie.png"} {
		data, err := os.ReadFile(filepath.Join(root, "movies", "影片", name))
		if err != nil || string(data) != "media-content" {
			t.Fatalf("下载文件 %s: %q %v, result=%+v", name, data, err, result)
		}
	}
	if requests.Load() != 6 || result.Downloaded != 6 || result.Files != 1 {
		t.Fatalf("下载请求=%d", requests.Load())
	}
	if progress.Processed != 1 || progress.Total != 1 || progress.Percent != 100 {
		t.Fatalf("附件混入 STRM 视频进度: %+v", progress)
	}
	if _, err := svc.Scan(context.Background(), "http://example.test", domain.StrmGenerateIncremental); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 6 {
		t.Fatal("增量不应重复下载")
	}
	result, err = svc.Scan(context.Background(), "http://example.test", domain.StrmGenerateFull)
	if err != nil || result.Downloaded != 6 || result.DownloadFailed != 0 || requests.Load() != 12 {
		t.Fatalf("全量下载替换失败: %+v %v", result, err)
	}
}

// TestStrmDownloadConcurrency 确认实际文件传输能并发且最多五个，而不是串行获取完整文件。
func TestStrmDownloadConcurrency(t *testing.T) {
	var active, peak atomic.Int32
	release := make(chan struct{})
	reached := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if n == 5 {
			select {
			case reached <- struct{}{}:
			default:
			}
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "image")
	}))
	defer server.Close()
	var files []domain.Pan115File
	for i := 0; i < 12; i++ {
		files = append(files, domain.Pan115File{ID: fmt.Sprint(i), PickCode: fmt.Sprint(i), Name: fmt.Sprintf("%d.png", i)})
	}
	api := &downloadPan115Stub{address: server.URL, strmPan115Stub: strmPan115Stub{pages: map[string]domain.Pan115FilePage{"1": {Files: files}}}}
	svc := newStrmTestService(t, t.TempDir(), api, nil, map[string]string{"STRM_DOWNLOAD_ENABLE": "true", "STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "1", Path: "/源", LocalPath: "/out"}})})
	done := make(chan error, 1)
	go func() {
		_, err := svc.Scan(context.Background(), "http://example.test", domain.StrmGenerateIncremental)
		done <- err
	}()
	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		close(release)
		<-done
		t.Fatal("未达到五个并发下载")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 5 {
		t.Fatalf("并发峰值=%d", peak.Load())
	}
}

// TestStrmDownloadSettings 确认设置保存规范化并原子拒绝无效后缀。
func TestStrmDownloadSettings(t *testing.T) {
	repo := &settingsMemoryRepository{}
	svc, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := svc.Update(context.Background(), map[string]string{"STRM_DOWNLOAD_ENABLE": "true", "STRM_DOWNLOAD_EXTENSIONS": `[" .JPG ","jpg","nfo"]`})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Values["STRM_DOWNLOAD_EXTENSIONS"] != `["jpg","nfo"]` {
		t.Fatalf("未规范化: %v", saved.Values)
	}
	for _, value := range []string{`["../nfo"]`, `["*"]`, `null`, `[1]`, `["strm"]`} {
		if _, err := svc.Update(context.Background(), map[string]string{"STRM_DOWNLOAD_EXTENSIONS": value}); err == nil {
			t.Fatalf("接受无效后缀 %s", value)
		}
	}
}

// TestStrmDownloadFailureAndCancel 确认 HTTP 失败或取消不覆盖已有文件、不残留临时文件、不泄露签名地址。
func TestStrmDownloadFailureAndCancel(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRequest), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if cancelRequest {
					w.Header().Set("Content-Length", "1000")
					fmt.Fprint(w, "partial")
					w.(http.Flusher).Flush()
					cancel()
					return
				}
				http.Error(w, "secret-token", http.StatusForbidden)
			}))
			defer server.Close()
			root := t.TempDir()
			target := filepath.Join(root, "out")
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(target, "poster.png")
			if err := os.WriteFile(destination, []byte("original"), 0o644); err != nil {
				t.Fatal(err)
			}
			api := &downloadPan115Stub{address: server.URL + "?token=secret-token"}
			svc := newStrmTestService(t, root, api, nil, nil)
			_, err := svc.downloadStrmMedia(ctx, root, target, "115", strmSourceFile{Name: "poster.png", PickCode: "p"}, domain.StrmGenerateFull)
			if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("错误不正确: %v", err)
			}
			data, _ := os.ReadFile(destination)
			if string(data) != "original" {
				t.Fatal("失败覆盖了旧文件")
			}
			entries, _ := os.ReadDir(target)
			if len(entries) != 1 {
				t.Fatalf("残留临时文件: %v", entries)
			}
		})
	}
}

// TestStrmDownloadCloudHeadersAndPaths 验证 CD2 必需请求头、嵌套字幕文件及路径越界防护。
func TestStrmDownloadCloudHeadersAndPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "CloudAgent" || r.Header.Get("X-Media") != "required" {
			http.Error(w, "headers", 403)
			return
		}
		fmt.Fprint(w, "subtitle")
	}))
	defer server.Close()
	root := t.TempDir()
	target := filepath.Join(root, "out")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	cloud := &strmCloudStub{configured: true, download: clouddrive.Download{URL: server.URL, UserAgent: "CloudAgent", Headers: map[string]string{"X-Media": "required"}}}
	svc := newStrmTestService(t, root, nil, cloud, nil)
	_, err := svc.downloadStrmMedia(context.Background(), root, target, "cd2", strmSourceFile{ID: "/cloud/movie.ass", Directory: "字幕/中文", Name: "movie.ass"}, domain.StrmGenerateFull)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "字幕", "中文", "movie.ass"))
	if err != nil || string(data) != "subtitle" {
		t.Fatalf("字幕内容: %q %v", data, err)
	}
	for _, dir := range []string{"../outside", "../../escape"} {
		if _, err := svc.downloadStrmMedia(context.Background(), root, target, "cd2", strmSourceFile{ID: "/cloud/x", Directory: dir, Name: "x.nfo"}, domain.StrmGenerateFull); err == nil {
			t.Fatalf("允许越界 %s", dir)
		}
	}
}

// TestStrmDownloadDoesNotUseNotificationDeadline 防止下载沿用通知请求的整次超时，持续传输应能完成。
func TestStrmDownloadDoesNotUseNotificationDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 8; i++ {
			fmt.Fprint(w, "chunk")
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	svc := newStrmTestService(t, root, &downloadPan115Stub{address: server.URL}, nil, nil)
	svc.http.Timeout = 20 * time.Millisecond
	_, err := svc.downloadStrmMedia(context.Background(), root, root, "115", strmSourceFile{Name: "movie.srt", PickCode: "p"}, domain.StrmGenerateFull)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "movie.srt"))
	if err != nil || string(data) != strings.Repeat("chunk", 8) {
		t.Fatalf("传输被截断: %q %v", data, err)
	}
}

// TestStrmDuplicateSidecarsPreserveExistingIncremental 验证网盘同名附件不会阻断已有本地附件的增量跳过，仍保护实际写入冲突。
func TestStrmDuplicateSidecarsPreserveExistingIncremental(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      domain.StrmGenerateMode
		existing  bool
		collision bool
	}{
		{"incremental-existing", domain.StrmGenerateIncremental, true, false},
		{"incremental-missing", domain.StrmGenerateIncremental, false, true},
		{"full-existing", domain.StrmGenerateFull, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "out"), 0755); err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(root, "out", "movie.nfo")
			if tc.existing {
				if err := os.WriteFile(local, []byte("merged-local-metadata"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			api := &downloadPan115Stub{address: "http://download.test", strmPan115Stub: strmPan115Stub{pages: map[string]domain.Pan115FilePage{"root": {Files: []domain.Pan115File{
				{ID: "nfo-a", PickCode: "nfo-a", Name: "movie.nfo"},
				{ID: "nfo-b", PickCode: "nfo-b", Name: "movie.nfo"},
				{ID: "video", PickCode: "video", Name: "movie.mp4"},
			}}}}}
			service := newStrmTestService(t, root, api, nil, map[string]string{strmDownloadEnableSettingKey: "true", strmPathsSettingKey: strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "root", Path: "/root", LocalPath: "/out"}})})
			var requests atomic.Int32
			service.http.Transport = embyRoundTripper(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("remote-nfo"))}, nil
			})
			result, err := service.Scan(context.Background(), "http://play.test", tc.mode)
			if tc.collision {
				if err == nil || !strings.Contains(err.Error(), "文件名冲突") {
					t.Fatalf("expected collision: result=%+v err=%v", result, err)
				}
				return
			}
			if err != nil || result.Created != 1 || result.DownloadSkipped != 2 || requests.Load() != 0 {
				t.Fatalf("result=%+v requests=%d err=%v", result, requests.Load(), err)
			}
			body, err := os.ReadFile(local)
			if err != nil || string(body) != "merged-local-metadata" {
				t.Fatalf("local=%q err=%v", body, err)
			}
			body, err = os.ReadFile(filepath.Join(root, "out", "movie.strm"))
			if err != nil || strings.TrimSpace(string(body)) != "http://play.test/files/play/115/video" {
				t.Fatalf("video=%q err=%v", body, err)
			}
		})
	}
}
