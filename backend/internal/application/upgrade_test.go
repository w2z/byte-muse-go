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

func (f *upgradeInstallerFake) Stage(ctx context.Context, version string) error {
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
