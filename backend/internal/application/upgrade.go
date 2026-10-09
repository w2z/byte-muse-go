package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"bytemuse/backend/internal/ports"
)

// ErrUpgradeBusy 表示已有升级任务，客户端应继续查询原任务而非重复执行。
var ErrUpgradeBusy = errors.New("已有升级任务正在执行")

// ErrUpgradeUnavailable 表示当前服务没有通过容器内启动器运行。
var ErrUpgradeUnavailable = errors.New("当前镜像不支持容器内升级，请先更新一次镜像")

// UpgradeService 管理进程内单个后台升级任务，页面关闭不取消，服务退出会取消下载。
type UpgradeService struct {
	ctx       context.Context
	versions  *VersionService
	installer ports.UpgradeInstaller
	mu        sync.Mutex
	state     ports.UpgradeStatus
	running   bool
}

// NewUpgradeService 绑定发布版本与安装器，安装器为空时仅保留版本检查能力。
func NewUpgradeService(ctx context.Context, versions *VersionService, installer ports.UpgradeInstaller) *UpgradeService {
	return &UpgradeService{ctx: ctx, versions: versions, installer: installer}
}

// Status 返回当前进程任务状态；重启后的新进程读取启动器的持久化记录。
func (s *UpgradeService) Status() ports.UpgradeStatus {
	if s == nil || s.installer == nil {
		return ports.UpgradeStatus{Phase: "idle"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Phase != "" {
		return s.state
	}
	return s.installer.Status()
}

// Start 立即受理升级；后台重新确认目标为最新版本后下载，禁止客户端提交任意 URL 或降级版本。
func (s *UpgradeService) Start(target string) (ports.UpgradeStatus, error) {
	if s == nil || s.installer == nil {
		return ports.UpgradeStatus{}, ErrUpgradeUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	persisted := s.installer.Status()
	if s.running || persisted.Phase == "restarting" || persisted.Phase == "downloading" || persisted.Phase == "extracting" || persisted.Phase == "installing" {
		return s.state, ErrUpgradeBusy
	}
	if ports.CompareVersions(s.versions.Current(), target) >= 0 {
		return ports.UpgradeStatus{}, errors.New("升级目标必须高于当前版本")
	}
	s.state = ports.UpgradeStatus{Enabled: true, Phase: "downloading", Target: target, ProgressIndeterminate: true}
	s.running = true
	go s.run(target)
	return s.state, nil
}

// run 只在完整升级包验证成功后提交重启请求，错误保留旧进程继续运行。
func (s *UpgradeService) run(target string) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()
	status, err := s.versions.Refresh(ctx)
	if err == nil && (status.CheckError != "" || !status.HasUpdate || status.Latest != target) {
		err = errors.New("发布版本已变化或检查失败，请重新检查更新")
	}
	if err == nil {
		err = s.installer.Stage(ctx, target, func(state ports.UpgradeStatus) {
			s.mu.Lock()
			s.state = state
			s.mu.Unlock()
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.state.Phase = "failed"
		s.state.ProgressIndeterminate = false
		s.state.Error = err.Error()
		s.running = false
	} else {
		s.state.Phase = "restarting"
		s.state.CompletedSteps = 3
		s.state.ProgressPercent = 75
		s.state.PhaseProgressPercent = 0
		s.state.ProgressIndeterminate = true
	}
}
