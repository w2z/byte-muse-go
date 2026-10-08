package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
	"context"
	"fmt"
	"slices"
	"time"
)

// SetControls 为列表和写操作注入同一下载器能力来源。
func (s *DownloadService) SetControls(repo ports.DownloadControlRepository, clients func(context.Context) (map[string]ports.DownloadController, error)) {
	s.controls, s.clients = repo, clients
}

// downloadActions intersects observed lifecycle actions with downloader capabilities; queued tasks remain stoppable.
func downloadActions(task domain.DownloadTask, capabilities []string) []string {
	actions := []string{}
	if task.Status == domain.DownloadStatusFailed && (task.InfoHash == nil || *task.InfoHash == "") {
		return []string{"retry", "delete"}
	}
	if task.InfoHash == nil || *task.InfoHash == "" || task.TransferStatus == nil {
		return actions
	}
	switch *task.TransferStatus {
	case "downloading", "queued", "stalled", "checking", "metadata", "moving":
		actions = []string{"pause", "stop", "delete", "delete_files"}
	case "paused", "stopped":
		actions = []string{"resume", "delete", "delete_files"}
	case "failed":
		actions = []string{"retry", "delete"}
	}
	return slices.DeleteFunc(actions, func(action string) bool { return !slices.Contains(capabilities, action) })
}

func (s *DownloadService) decorateActions(ctx context.Context, items []domain.DownloadTask) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	clients := map[string]ports.DownloadController{}
	if s.clients != nil {
		if current, err := s.clients(ctx); err == nil {
			clients = current
		}
	}
	capabilities := map[string][]string{}
	for i := range items {
		task := &items[i]
		if task.Downloader != nil {
			name := *task.Downloader
			if _, ok := capabilities[name]; !ok {
				capabilities[name] = nil
				if client := clients[name]; client != nil {
					capabilities[name], _ = client.Capabilities(ctx)
				}
			}
			task.AvailableActions = downloadActions(*task, capabilities[name])
		} else {
			task.AvailableActions = downloadActions(*task, nil)
		}
	}
	decorateMetrics(ctx, items, clients)
}

// Control 校验最新下载器状态，执行一次明确操作并回查；不把 HTTP 成功当作任务成功。
func (s *DownloadService) Control(ctx context.Context, id, action string) error {
	if !slices.Contains([]string{"pause", "stop", "resume", "retry", "delete", "delete_files"}, action) {
		return ports.ErrDownloadAction
	}
	if s.controls == nil {
		return ports.ErrDownloadAction
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	task, token, err := s.controls.LockControl(ctx, id)
	if err != nil {
		return err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = s.controls.ReleaseControl(releaseCtx, id, token)
	}()
	if task.Status == domain.DownloadStatusFailed && (task.InfoHash == nil || *task.InfoHash == "") {
		if action == "retry" {
			action = "retry_search"
		} else if action != "delete" {
			return ports.ErrDownloadAction
		}
		return s.controls.FinishControl(ctx, id, token, action, nil)
	}
	if s.clients == nil || task.Downloader == nil || task.InfoHash == nil {
		return ports.ErrDownloadAction
	}
	clients, err := s.clients(ctx)
	if err != nil {
		return err
	}
	client := clients[*task.Downloader]
	if client == nil {
		return ports.ErrDownloadAction
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("读取下载器能力失败: %w", err)
	}
	state, err := client.Observe(ctx, *task.InfoHash)
	if err != nil {
		return fmt.Errorf("回查下载器失败: %w", err)
	}
	if state == nil {
		// 删除响应丢失后再次删除可安全收敛；绝不重投不存在的外部资源。
		if action == "delete" {
			return s.controls.FinishControl(ctx, id, token, action, nil)
		}
		return fmt.Errorf("下载器中未找到任务，请刷新后检查下载器")
	}
	task.TransferStatus = &state.Status
	if !slices.Contains(downloadActions(task, caps), action) {
		return ports.ErrDownloadAction
	}
	commandErr := client.Control(ctx, *task.InfoHash, action)
	for attempt := 0; attempt < 8; attempt++ {
		observed, readErr := client.Observe(ctx, *task.InfoHash)
		if readErr == nil && controlConfirmed(action, observed) {
			err = s.controls.FinishControl(ctx, id, token, action, observed)
			if err == nil {
				logging.Info(logging.CategoryDownload, "下载任务操作已核实", "task_id", id, "action", action)
			}
			return err
		}
		if commandErr != nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("下载器操作结果待确认，请刷新后核实")
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("下载器操作结果未确认，请刷新后核实；未自动重复执行")
}

// controlConfirmed accepts an enabled queue or waiting state after resume; immediate traffic is not required.
func controlConfirmed(action string, state *ports.TransferState) bool {
	if action == "delete" || action == "delete_files" {
		return state == nil
	}
	if state == nil {
		return false
	}
	switch action {
	case "pause":
		return state.Status == "paused"
	case "stop":
		return state.Status == "stopped"
	case "resume", "retry":
		return slices.Contains([]string{"downloading", "queued", "stalled", "checking", "metadata", "moving", "completed"}, state.Status)
	}
	return false
}
