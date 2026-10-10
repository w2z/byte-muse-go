package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/ports"
)

// TestUpgradeVersionValidation 验证近期检查结果可复用，过期和不匹配结果不能绕过校验。
func TestUpgradeVersionValidation(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		cached      string
		age         time.Duration
		latest      string
		sourceError error
		wantError   string
		wantStage   bool
	}{
		{name: "复用刚确认的版本", cached: "0.1.22", sourceError: errors.New("检查更新超时"), wantStage: true},
		{name: "过期重新检查并保留原因", cached: "0.1.22", age: 2 * time.Hour, sourceError: errors.New("检查更新超时"), wantError: "检查更新超时"},
		{name: "首次检查失败", sourceError: errors.New("无法访问 GitHub 发布仓库"), wantError: "无法访问 GitHub 发布仓库"},
		{name: "目标变化", latest: "0.1.23", wantError: "0.1.23"},
		{name: "缓存目标不匹配", cached: "0.1.23", latest: "0.1.22", wantError: "0.1.23"},
		{name: "过期后仍匹配", cached: "0.1.22", age: 2 * time.Hour, latest: "0.1.22", wantStage: true},
		{name: "没有可用更新", latest: "0.1.21", wantError: "没有高于当前运行版本"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			source := &stubReleaseSource{info: ports.ReleaseInfo{Version: scenario.cached}}
			versions := NewVersionService("0.1.21", source)
			if scenario.cached != "" {
				if _, err := versions.Refresh(context.Background()); err != nil {
					t.Fatal(err)
				}
				versions.cachedAt = versions.cachedAt.Add(-scenario.age)
			}
			source.info.Version, source.err = scenario.latest, scenario.sourceError
			installer := &upgradeInstallerFake{start: make(chan struct{}), finish: make(chan struct{})}
			close(installer.finish)
			service := NewUpgradeService(context.Background(), versions, installer)
			if _, err := service.Start("0.1.22"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for service.Status().Phase != "failed" && service.Status().Phase != "restarting" && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			state := service.Status()
			if scenario.wantStage {
				wantCalls := 1
				if scenario.age > 0 {
					wantCalls = 2
				}
				if state.Phase != "restarting" || source.calls != wantCalls {
					t.Fatalf("state=%+v calls=%d", state, source.calls)
				}
			} else {
				if state.Phase != "failed" || !strings.Contains(state.Error, scenario.wantError) {
					t.Fatalf("state=%+v want=%s", state, scenario.wantError)
				}
				select {
				case <-installer.start:
					t.Fatal("校验失败不得进入安装器")
				default:
				}
			}
		})
	}
}

type upgradeInstallerFake struct {
	start  chan struct{}
	finish chan struct{}
	err    error
}

func (f *upgradeInstallerFake) Stage(ctx context.Context, version string, report func(ports.UpgradeStatus)) error {
	report(ports.UpgradeStatus{Enabled: true, Phase: "extracting", Target: version, CompletedSteps: 1})
	report(ports.UpgradeStatus{Enabled: true, Phase: "installing", Target: version, CompletedSteps: 2})
	close(f.start)
	select {
	case <-f.finish:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f *upgradeInstallerFake) Status() ports.UpgradeStatus {
	return ports.UpgradeStatus{Phase: "idle", Enabled: true}
}

// TestUpgradeSingleFlight 验证升级受理不阻塞 HTTP，重复请求不创建第二个任务，下载失败可见。
func TestUpgradeSingleFlight(t *testing.T) {
	installer := &upgradeInstallerFake{start: make(chan struct{}), finish: make(chan struct{}), err: errors.New("升级包校验失败")}
	versions := NewVersionService("0.1.21", &stubReleaseSource{info: ports.ReleaseInfo{Version: "0.1.22"}})
	service := NewUpgradeService(context.Background(), versions, installer)
	if _, err := service.Start("0.1.22"); err != nil {
		t.Fatal(err)
	}
	<-installer.start
	if _, err := service.Start("0.1.22"); !errors.Is(err, ErrUpgradeBusy) {
		t.Fatalf("err=%v", err)
	}
	close(installer.finish)
	deadline := time.Now().Add(time.Second)
	for service.Status().Phase != "failed" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if state := service.Status(); state.Phase != "failed" || state.Error != "升级包校验失败" {
		t.Fatalf("state=%+v", state)
	}
}

// TestUpgradeProgress 验证失败保留已完成步骤。
func TestUpgradeProgress(t *testing.T) {
	installer := &upgradeInstallerFake{start: make(chan struct{}), finish: make(chan struct{}), err: errors.New("安装失败")}
	service := NewUpgradeService(context.Background(), NewVersionService("0.1.21", &stubReleaseSource{info: ports.ReleaseInfo{Version: "0.1.22"}}), installer)
	if _, err := service.Start("0.1.22"); err != nil {
		t.Fatal(err)
	}
	<-installer.start
	if state := service.Status(); state.Phase != "installing" || state.CompletedSteps != 2 {
		t.Fatalf("state=%+v", state)
	}
	close(installer.finish)
	deadline := time.Now().Add(time.Second)
	for service.Status().Phase != "failed" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if state := service.Status(); state.Phase != "failed" || state.CompletedSteps != 2 {
		t.Fatalf("state=%+v", state)
	}
}
