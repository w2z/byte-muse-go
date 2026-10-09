package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
)

// TestScanFileErrors preserves per-file failures without exposing error details on directories.
func TestScanFileErrors(t *testing.T) {
	for _, scenario := range []string{"generate", "download"} {
		t.Run(scenario, func(t *testing.T) {
			tree := newScanFileTree("errors")
			ctx := context.WithValue(context.Background(), scanFileTreeKey{}, tree)
			ctx = context.WithValue(ctx, strmRetryKey{}, &strmRetryPolicy{wait: func(context.Context, string, error) error { return context.Canceled }})
			files := []domain.Pan115File{{ID: "video", Name: "movie.mp4"}}
			want := "115 文件缺少 pick_code"
			if scenario == "download" {
				files = []domain.Pan115File{{ID: "poster", Name: "poster.jpg", PickCode: "poster"}}
				want = "115 错误 20018：请求过于频繁"
			}
			source := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{"root": {Files: files}}, playErr: &pan115.APIError{Code: 20018, Message: "请求过于频繁"}}
			service := newStrmTestService(t, t.TempDir(), source, nil, map[string]string{"STRM_DOWNLOAD_ENABLE": "true", "STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "root", Path: "/电影", LocalPath: "/movies"}})})
			_, _ = service.Scan(ctx, "http://play.test", domain.StrmGenerateFull)
			root := tree.page("", 1, 15, false).Items[0]
			child := tree.page(root.ID, 1, 15, false).Items[0]
			if root.Error != "" || !strings.Contains(child.Error, want) {
				t.Fatalf("root=%+v child=%+v", root, child)
			}
		})
	}
}

// TestScanFileErrorRedaction keeps readable causes without signed URLs or credentials.
func TestScanFileErrorRedaction(t *testing.T) {
	message := scanFileError(errors.New("请求失败 https://download.test/file?sign=secret Authorization: Bearer secret Cookie: UID=secret"))
	if strings.Contains(message, "secret") || strings.Contains(message, "https://") || !strings.Contains(message, "请求失败") {
		t.Fatal(message)
	}
	if message := scanFileError(errors.New(`{"access_token":"secret"}`)); strings.Contains(message, "secret") {
		t.Fatal(message)
	}
	if message := scanFileError(errors.New(strings.Repeat("错", 3000))); len([]rune(message)) != 2049 {
		t.Fatal("error message must be bounded")
	}
}

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
	root := tree.page("", 1, 15, false).Items[0]
	children := tree.page(root.ID, 1, 15, false)
	if root.State != "completed" || root.Total != 2 || children.Total != 1 {
		t.Fatalf("%+v %+v", root, children)
	}
	files := tree.page(children.Items[0].ID, 1, 15, false)
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
	root := tree.page("", 1, 15, false).Items[0]
	child := tree.page(root.ID, 1, 15, false).Items[0]
	row := tree.page(child.ID, 1, 15, false).Items[0]
	if root.Total != 1 || child.Percent != 0 || row.Percent != 50 || row.Bytes != 50 {
		t.Fatalf("%+v %+v %+v", root, child, row)
	}
	finish("completed", nil)
	child = tree.page(root.ID, 1, 15, false).Items[0]
	if child.State == "completed" || child.Processed != 1 {
		t.Fatalf("premature completion: %+v", child)
	}
	finishDirectory(true)
	child = tree.page(root.ID, 1, 15, false).Items[0]
	if child.State != "completed" || child.Percent != 100 {
		t.Fatalf("%+v", child)
	}
	var workers sync.WaitGroup
	for index := 0; index < 5; index++ {
		workers.Add(1)
		go func() { defer workers.Done(); finish("completed", nil) }()
	}
	workers.Wait()
	if got := tree.page(root.ID, 1, 15, false).Items[0]; got.Processed != 1 {
		t.Fatalf("duplicate completion: %+v", got)
	}
	if got := tree.page(child.ID, 2, 15, false); len(got.Items) != 0 || got.Total != 1 {
		t.Fatalf("pagination: %+v", got)
	}
	finishSecond := startScanFile(ctx, strmSourceFile{ID: "2", Name: "second.jpg", Directory: "子目录"}, "download")
	if got := tree.page(root.ID, 1, 15, false).Items[0]; got.State != "processing" || got.Percent != 50 {
		t.Fatalf("pending download: %+v", got)
	}
	finishSecond("failed", errors.New("下载失败"))
	finishSecond("failed", errors.New("不得覆盖首次错误"))
	failedFile := tree.page(child.ID, 1, 15, false).Items[0]
	if failedFile.Error != "下载失败" {
		t.Fatalf("lost original error: %+v", failedFile)
	}
	if got := tree.page(root.ID, 1, 15, false).Items[0]; got.State != "failed" || got.Failed != 1 {
		t.Fatalf("failed download: %+v", got)
	}
	finishDirectory(false)
	if got := tree.page(root.ID, 1, 15, false).Items[0]; got.State != "interrupted" {
		t.Fatalf("interrupted discovery: %+v", got)
	}
}

// TestScanFileWaitingLifecycle verifies queue admission, worker promotion, deduplication and cancellation.
func TestScanFileWaitingLifecycle(t *testing.T) {
	tree := newScanFileTree("waiting")
	ctx := withScanFileMapping(context.WithValue(context.Background(), scanFileTreeKey{}, tree), domain.StrmMapping{Kind: "115", ID: "root", Path: "/movies"})
	root := tree.page("", 1, 15, false).Items[0]
	file := strmSourceFile{ID: "poster", Name: "poster.jpg"}
	trackScanFile(ctx, file, "download", "waiting")
	if got := tree.page(root.ID, 1, 15, false).Items[0]; got.State != "waiting" {
		t.Fatalf("queued: %+v", got)
	}
	finish := startScanFile(ctx, file, "download")
	trackScanFile(ctx, file, "download", "waiting")
	if got := tree.page(root.ID, 1, 15, false).Items[0]; got.State != "processing" {
		t.Fatalf("worker: %+v", got)
	}
	finish("completed", nil)
	startScanFile(ctx, file, "download")
	if got := tree.page("", 1, 15, false).Items[0]; got.Total != 1 || got.Processed != 1 {
		t.Fatalf("duplicate: %+v", got)
	}
}

// TestScanFileStatusOrdering verifies priority before pagination, stable ties and live transitions.
func TestScanFileStatusOrdering(t *testing.T) {
	tree := newScanFileTree("ordering")
	for _, parent := range []string{"", "nested"} {
		for _, state := range []string{"completed", "failed", "waiting", "processing", "interrupted", "skipped", "scanning", "processing"} {
			id := fmt.Sprintf("%s-%d", parent, len(tree.rows))
			tree.add(ScanFileRow{ID: id, parent: parent, Kind: "file", State: state})
		}
		var states []string
		for page := 1; page <= 4; page++ {
			for _, row := range tree.page(parent, page, 2, false).Items {
				states = append(states, row.State)
			}
		}
		want := []string{"processing", "scanning", "processing", "waiting", "failed", "interrupted", "completed", "skipped"}
		if !reflect.DeepEqual(states, want) {
			t.Fatalf("parent=%q got=%v want=%v", parent, states, want)
		}
		active := tree.page(parent, 1, 2, false).Items[0]
		tree.rows[active.ID].State = "completed"
		if got := tree.page(parent, 1, 2, true); got.Items[0].State != "scanning" || got.Total != 6 {
			t.Fatalf("live transition: %+v", got)
		}
	}
}

// TestScanFileCompletionOrdering verifies filtering and stable ordering before pagination at every level.
func TestScanFileCompletionOrdering(t *testing.T) {
	tree := newScanFileTree("task")
	tree.add(ScanFileRow{ID: "done", Kind: "directory", closed: true})
	tree.add(ScanFileRow{ID: "pending", Kind: "directory", State: "scanning"})
	tree.add(ScanFileRow{ID: "failed", Kind: "directory", closed: true, Failed: 1})
	tree.add(ScanFileRow{ID: "finished-file", parent: "pending", Kind: "file", State: "completed"})
	tree.add(ScanFileRow{ID: "active-file", parent: "pending", Kind: "file", State: "processing"})
	tree.add(ScanFileRow{ID: "skipped-file", parent: "pending", Kind: "file", State: "skipped"})
	first := tree.page("", 1, 2, false)
	if first.Total != 3 || first.Items[0].ID != "pending" || first.Items[1].ID != "failed" {
		t.Fatalf("first page: %+v", first)
	}
	last := tree.page("", 2, 2, false)
	if len(last.Items) != 1 || last.Items[0].ID != "done" {
		t.Fatalf("last page: %+v", last)
	}
	hidden := tree.page("", 1, 2, true)
	if hidden.Total != 2 || len(hidden.Items) != 2 {
		t.Fatalf("hidden: %+v", hidden)
	}
	files := tree.page("pending", 1, 10, true)
	if files.Total != 2 || files.Items[0].ID != "active-file" || files.Items[1].ID != "skipped-file" {
		t.Fatalf("nested: %+v", files)
	}
	if tree.rows["done"].State != "" {
		t.Fatal("query mutated live state")
	}
	tree.rows["pending"].closed = true
	if got := tree.page("", 1, 10, true); got.Total != 1 || got.Items[0].ID != "failed" {
		t.Fatalf("completion update: %+v", got)
	}
}

// TestScanProgressCountsFiles 验证进度使用全部目录的实际文件总数，并覆盖跳过文件与重复生成。
func TestScanProgressCountsFiles(t *testing.T) {
	for _, kind := range []string{"library", "strm"} {
		t.Run(kind, func(t *testing.T) {
			var updates []ScanProgress
			ctx := WithScanProgress(context.Background(), func(p ScanProgress) { updates = append(updates, p) })
			files := []domain.Pan115File{{ID: "a", Name: "ABC-001.mp4", PickCode: "a"}, {ID: "b", Name: "unknown.mp4", PickCode: "b"}}
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
				if p.Phase == "processing" && p.Processed > 0 && p.Processed < p.Total && p.Percent == p.Processed*100/p.Total {
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
