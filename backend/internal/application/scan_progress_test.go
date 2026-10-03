package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
)

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
				if p.Phase == "discovering" && p.Processed == 2 && p.Total == 4 && p.Percent == 50 {
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
