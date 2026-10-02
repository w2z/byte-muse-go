package application

import (
	"context"
	"testing"

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
