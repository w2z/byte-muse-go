package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"bytemuse/backend/internal/ports"
)

// stubReleaseSource 是发布源的测试替身：记录调用次数并按序返回预设结果。
type stubReleaseSource struct {
	info  ports.ReleaseInfo
	err   error
	calls int
}

func (s *stubReleaseSource) Latest(context.Context) (ports.ReleaseInfo, error) {
	s.calls++
	return s.info, s.err
}

// TestCompareVersions 覆盖版本比较的方向判定：数字段逐位比较，非数字版本不可比较。
func TestCompareVersions(t *testing.T) {
	for _, testCase := range []struct {
		current string
		latest  string
		want    int
	}{
		{"0.1.21", "0.1.21", 0},
		{"0.1.21", "0.1.22", -1},
		{"0.1.21", "0.1.20", 1},
		{"0.1.9", "0.1.10", -1},
		{"0.2.0", "0.1.99", 1},
		{"0.1", "0.1.1", -1},
		{"v0.1.21", "0.1.22", -1},
		{"dev", "0.1.22", 0},
		{"0.1.21", "unknown", 0},
		{"", "0.1.21", 0},
		{"0.1.21-beta", "0.1.22", 0},
	} {
		if got := ports.CompareVersions(testCase.current, testCase.latest); got != testCase.want {
			t.Fatalf("ports.CompareVersions(%q, %q)=%d, want %d", testCase.current, testCase.latest, got, testCase.want)
		}
	}
}

// TestVersionStatus 覆盖可更新、已最新、开发版本和远端失败四种结论。
func TestVersionStatus(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		current    string
		info       ports.ReleaseInfo
		sourceErr  error
		wantLatest string
		wantUpdate bool
		wantError  bool
	}{
		{name: "发现新版本", current: "0.1.21", info: ports.ReleaseInfo{Version: "0.1.22", Source: "https://github.com/w2z/byte-muse-go"}, wantLatest: "0.1.22", wantUpdate: true},
		{name: "已是最新", current: "0.1.21", info: ports.ReleaseInfo{Version: "0.1.21"}, wantLatest: "0.1.21"},
		{name: "本地版本更高", current: "0.1.30", info: ports.ReleaseInfo{Version: "0.1.21"}, wantLatest: "0.1.21"},
		{name: "开发版本不比较", current: "dev", info: ports.ReleaseInfo{Version: "0.1.22"}, wantLatest: "0.1.22"},
		{name: "远端失败仍返回当前版本", current: "0.1.21", sourceErr: errors.New("无法访问 GitHub 发布仓库"), wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := &stubReleaseSource{info: testCase.info, err: testCase.sourceErr}
			status, err := NewVersionService(testCase.current, source).Status(context.Background())
			if err != nil {
				t.Fatalf("Status 不应返回错误: %v", err)
			}
			if status.Current != testCase.current || status.Latest != testCase.wantLatest || status.HasUpdate != testCase.wantUpdate {
				t.Fatalf("status=%+v", status)
			}
			if (status.CheckError != "") != testCase.wantError {
				t.Fatalf("check_error=%q", status.CheckError)
			}
			if status.CheckedAt == "" {
				t.Fatal("缺少 checked_at")
			}
		})
	}
}

// TestVersionServiceCache 验证同一缓存窗口只访问一次上游，成功与失败使用不同缓存时长。
func TestVersionServiceCache(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	source := &stubReleaseSource{info: ports.ReleaseInfo{Version: "0.1.22"}}
	service := NewVersionService("0.1.21", source)
	service.now = func() time.Time { return now }
	for index := 0; index < 3; index++ {
		if _, err := service.Status(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if source.calls != 1 {
		t.Fatalf("缓存窗口内上游调用次数=%d", source.calls)
	}
	now = now.Add(releaseCacheTTL + time.Second)
	if _, err := service.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.calls != 2 {
		t.Fatalf("缓存过期后上游调用次数=%d", source.calls)
	}

	failing := &stubReleaseSource{err: errors.New("检查更新超时")}
	failingService := NewVersionService("0.1.21", failing)
	failingService.now = func() time.Time { return now }
	if _, err := failingService.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := failingService.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failing.calls != 1 {
		t.Fatalf("失败缓存窗口内上游调用次数=%d", failing.calls)
	}
	now = now.Add(releaseFailureTTL + time.Second)
	if _, err := failingService.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failing.calls != 2 {
		t.Fatalf("失败缓存过期后上游调用次数=%d", failing.calls)
	}
}

// TestVersionServiceWithoutSource 验证未装配发布源时返回 ErrVersionUnavailable。
func TestVersionServiceWithoutSource(t *testing.T) {
	if _, err := NewVersionService("0.1.21", nil).Status(context.Background()); !errors.Is(err, ErrVersionUnavailable) {
		t.Fatalf("err=%v", err)
	}
	var service *VersionService
	if _, err := service.Status(context.Background()); !errors.Is(err, ErrVersionUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if service.Current() != "dev" || NewVersionService("", &stubReleaseSource{}).Current() != "dev" {
		t.Fatal("未注入构建版本时应回退 dev")
	}
}
