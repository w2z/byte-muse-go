package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"bytemuse/backend/internal/ports"
)

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
