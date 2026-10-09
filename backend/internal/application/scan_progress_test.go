package application

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
)

// TestScanFilesTrackActualGenerationAndDownload exercises the real walker and download writer.
func TestScanFilesTrackActualGenerationAndDownload(t *testing.T) {
	tree := newScanFileTree("integration")
	ctx := context.WithValue(context.Background(), scanFileTreeKey{}, tree)
	source := &downloadPan115Stub{address: "http://download.test", strmPan115Stub: strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"root":  {Files: []domain.Pan115File{{ID: "child", Name: "子目录", IsDirectory: true}, {ID: "ignored", Name: "排除目录", IsDirectory: true}}},
		"child": {Files: []domain.Pan115File{{ID: "video", Name: "movie.mp4", PickCode: "video"}, {ID: "poster", Name: "poster.jpg", PickCode: "poster"}}},
	}}}
	service := newStrmTestService(t, t.TempDir(), source, nil, map[string]string{"STRM_DOWNLOAD_ENABLE": "true", "STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "root", Path: "/电影", LocalPath: "/movies", Exclude: []domain.StrmExcludeKeyword{{Mode: domain.StrmExcludeModeEquals, Value: "排除目录"}}}})})
	service.http.Transport = embyRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: 6, Body: io.NopCloser(strings.NewReader("poster"))}, nil
	})
	result, err := service.Scan(ctx, "http://play.test", domain.StrmGenerateFull)
	if err != nil || result.Downloaded != 1 || result.Created != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	root := tree.page("", 1, 15).Items[0]
	children := tree.page(root.ID, 1, 15)
	if root.State != "completed" || root.Total != 2 || children.Total != 1 {
		t.Fatalf("%+v %+v", root, children)
	}
	files := tree.page(children.Items[0].ID, 1, 15)
	if files.Total != 2 || files.Items[1].Bytes != 6 || files.Items[1].Percent != 100 {
		t.Fatalf("%+v", files)
	}
}

// TestScanFileTreeProgress verifies nested aggregation, bytes, failures and directory discovery.
func TestScanFileTreeProgress(t *testing.T) {
	tree := newScanFileTree("task")
	ctx := context.WithValue(context.Background(), scanFileTreeKey{}, tree)
	ctx = withScanFileMapping(ctx, domain.StrmMapping{Kind: "115", ID: "1", Path: "/电影", LocalPath: "/movies"})
	finishDirectory := scanFileDirectory(ctx, "子目录")
	file := strmSourceFile{ID: "1", Name: "poster.jpg", Directory: "子目录"}
	finish := startScanFile(ctx, file, "download")
	reportScanFileBytes(ctx, file, 50, 100)
	root := tree.page("", 1, 15).Items[0]
	child := tree.page(root.ID, 1, 15).Items[0]
	row := tree.page(child.ID, 1, 15).Items[0]
	if root.Total != 1 || child.Percent != 0 || row.Percent != 50 || row.Bytes != 50 {
		t.Fatalf("%+v %+v %+v", root, child, row)
	}
	finish("completed")
	child = tree.page(root.ID, 1, 15).Items[0]
	if child.State == "completed" || child.Processed != 1 {
		t.Fatalf("premature completion: %+v", child)
	}
	finishDirectory(true)
	child = tree.page(root.ID, 1, 15).Items[0]
	if child.State != "completed" || child.Percent != 100 {
		t.Fatalf("%+v", child)
	}
	var workers sync.WaitGroup
	for index := 0; index < 5; index++ {
		workers.Add(1)
		go func() { defer workers.Done(); finish("completed") }()
	}
	workers.Wait()
	if got := tree.page(root.ID, 1, 15).Items[0]; got.Processed != 1 {
		t.Fatalf("duplicate completion: %+v", got)
	}
	if got := tree.page(child.ID, 2, 15); len(got.Items) != 0 || got.Total != 1 {
		t.Fatalf("pagination: %+v", got)
	}
	finishSecond := startScanFile(ctx, strmSourceFile{ID: "2", Name: "second.jpg", Directory: "子目录"}, "download")
	if got := tree.page(root.ID, 1, 15).Items[0]; got.State != "processing" || got.Percent != 50 {
		t.Fatalf("pending download: %+v", got)
	}
	finishSecond("failed")
	if got := tree.page(root.ID, 1, 15).Items[0]; got.State != "failed" || got.Failed != 1 {
		t.Fatalf("failed download: %+v", got)
	}
	finishDirectory(false)
	if got := tree.page(root.ID, 1, 15).Items[0]; got.State != "interrupted" {
		t.Fatalf("interrupted discovery: %+v", got)
	}
}

// TestScanProgressCountsFiles 验证进度使用全部目录的实际文件总数，并覆盖跳过文件与重复生成。
func TestScanProgressCountsFiles(t *testing.T) {
	for _, kind := range []string{"library", "strm"} {
		t.Run(kind, func(t *testing.T) {
			var updates []ScanProgress
			ctx := WithScanProgress(context.Background(), func(p ScanProgress) { updates = append(updates, p) })
			files := []domain.Pan115File{{ID: "a", Name: "ABC-001.mp4"}, {ID: "b", Name: "unknown.mp4"}}
			if kind == "library" {
				service := newLibraryScanService(t, &libraryScanPan115Stub{files: map[string][]domain.Pan115File{"1": files, "2": files}}, &libraryScanWriterStub{}, map[string]string{pan115ScanPathsSettingKey: `[{"id":"1","path":"/a"},{"id":"2","path":"/b"}]`})
				if _, err := service.Scan(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				service := newStrmTestService(t, t.TempDir(), &strmPan115Stub{pages: map[string]domain.Pan115FilePage{"1": {Files: files}, "2": {Files: files}}}, nil, map[string]string{strmPathsSettingKey: `[{"kind":"115","id":"1","path":"/a","local_path":"/a"},{"kind":"115","id":"2","path":"/b","local_path":"/b"}]`})
				for i := 0; i < 2; i++ {
					if _, err := service.Scan(ctx, "https://example.com", domain.StrmGenerateFull); err != nil {
						t.Fatal(err)
					}
				}
			}
			halfway := false
			dynamic := false
			for _, p := range updates {
				// 扫描入库与 strm 生成都是边扫描边处理：处理中总数随发现增长到 4、已处理 3 时进度 75%。
				if p.Phase == "processing" && p.Processed == 3 && p.Total == 4 && p.Percent == 75 {
					dynamic = true
				}
				if p.Phase == "processing" && p.Processed == 1 && p.Total == 2 && p.Percent == 50 {
					halfway = true
				}
			}
			if !halfway {
				t.Fatalf("缺少实际文件进度 1/2 (50%%): %+v", updates)
			}
			if !dynamic {
				t.Fatalf("未随发现文件更新总数与百分比: %+v", updates)
			}
			last := updates[len(updates)-1]
			if last.Phase != "completed" || last.Processed != 4 || last.Total != 4 || last.Percent != 100 {
				t.Fatalf("结束进度错误: %+v", last)
			}
		})
	}
}

// TestScanProgressEmptyAndCancelled 覆盖空目录与取消，避免空任务除零或取消后假报完成。
func TestScanProgressEmptyAndCancelled(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		var updates []ScanProgress
		ctx, cancel := context.WithCancel(context.Background())
		ctx = WithScanProgress(ctx, func(p ScanProgress) { updates = append(updates, p) })
		if cancelled {
			cancel()
		}
		service := newLibraryScanService(t, &libraryScanPan115Stub{files: map[string][]domain.Pan115File{"1": {}}}, &libraryScanWriterStub{}, map[string]string{pan115ScanPathsSettingKey: `[{"id":"1","path":"/empty"}]`})
		_, err := service.Scan(ctx)
		cancel()
		if cancelled {
			if err != context.Canceled || len(updates) != 0 {
				t.Fatalf("取消后仍执行: %v %+v", err, updates)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		last := updates[len(updates)-1]
		if last.Processed != 0 || last.Total != 0 || last.Percent != 100 || last.Phase != "completed" {
			t.Fatalf("空目录进度: %+v", last)
		}
	}
}

// TestFormatCooldownRendersReadableChinese 验证冷却时长会转成用户可读的中文时长，
// 避免任务进度里出现「等待 3600s」这类难以理解的原始数值。
func TestFormatCooldownRendersReadableChinese(t *testing.T) {
	cases := []struct {
		wait time.Duration
		want string
	}{
		{0, "片刻"},
		{time.Millisecond, "1 秒"},
		{45 * time.Second, "45 秒"},
		{time.Minute, "1 分钟"},
		{90 * time.Second, "2 分钟"},
		{30 * time.Minute, "30 分钟"},
		{time.Hour, "1 小时"},
		{2*time.Hour + 30*time.Minute, "2 小时 30 分钟"},
	}
	for _, testCase := range cases {
		if got := formatCooldown(testCase.wait); got != testCase.want {
			t.Errorf("formatCooldown(%v) = %q，期望 %q", testCase.wait, got, testCase.want)
		}
	}
}

// TestPan115CooldownNoticeNamesTarget 验证冷却提示带上触发限流的映射路径，
// 使用户能直接定位是哪条配置消耗了 115 配额；路径为空时回落到网盘名称。
func TestPan115CooldownNoticeNamesTarget(t *testing.T) {
	notice := pan115CooldownNotice("/影片/合集", 5*time.Minute)
	if !strings.Contains(notice, "/影片/合集") || !strings.Contains(notice, "5 分钟") {
		t.Fatalf("冷却提示 = %q，期望包含映射路径与等待时长", notice)
	}
	if fallback := pan115CooldownNotice("  ", time.Minute); !strings.HasPrefix(fallback, "115 网盘") {
		t.Fatalf("空路径提示 = %q，期望以「115 网盘」开头", fallback)
	}
}
