package httpapi

import (
	"bytemuse/backend/internal/application"
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"time"
)

// managedScan 复用持久化任务执行；Prefer: respond-async 立即返回 202，旧客户端仍可等待结果或进度流。
// 无论客户端是否断连，后台任务均独立运行；任务控制只通过显式控制接口执行。
func managedScan[T any](w http.ResponseWriter, r *http.Request, tasks *application.ScanTasks, kind, mode string, run func(context.Context) (T, error), failure func(http.ResponseWriter, error)) {
	if tasks == nil {
		streamScan(w, r, run, failure)
		return
	}
	runErrors := make(chan error, 1)
	task, err := tasks.Start(r.Context(), kind, mode, func(ctx context.Context) (any, error) { result, err := run(ctx); runErrors <- err; return result, err })
	if err != nil {
		writeScanTaskError(w, err)
		return
	}
	if r.Header.Get("Prefer") == "respond-async" {
		w.Header().Set("Preference-Applied", "respond-async")
		writeJSON(w, http.StatusAccepted, task)
		return
	}
	streamScan(w, r, func(ctx context.Context) (T, error) {
		var result T
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			current, e := tasks.Get(ctx, task.ID)
			if e != nil {
				return result, e
			}
			if current == nil || current.ID != task.ID {
				return result, application.ErrScanTaskConflict
			}
			application.NotifyScanProgress(ctx, current.Progress)
			if !current.Active() {
				if current.State != "completed" {
					if runErr := <-runErrors; runErr != nil {
						return result, runErr
					}
					return result, errors.New("任务已停止或失败")
				}
				e = json.Unmarshal(current.Result, &result)
				return result, e
			}
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-ticker.C:
			}
		}
	}, failure)
}

// scanTaskEndpoint 返回独立任务的持久化状态，或按任务 ID 执行暂停、继续和取消。
func scanTaskEndpoint(tasks *application.ScanTasks, kind string, control bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tasks == nil {
			writeError(w, 503, "service_unavailable", "后台任务服务尚未就绪")
			return
		}
		if !control {
			task, err := tasks.Latest(r.Context(), kind)
			if err != nil {
				writeScanTaskError(w, err)
				return
			}
			writeJSON(w, 200, task)
			return
		}
		var body struct {
			Action string `json:"action"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil || (body.Action != "pause" && body.Action != "resume" && body.Action != "cancel") {
			writeError(w, 400, "invalid_request", "action 必须为 pause、resume 或 cancel")
			return
		}
		task, err := tasks.Control(r.Context(), kind, chi.URLParam(r, "id"), body.Action)
		if err != nil {
			writeScanTaskError(w, err)
			return
		}
		writeJSON(w, 200, task)
	}
}

func writeScanTaskError(w http.ResponseWriter, err error) {
	if errors.Is(err, application.ErrScanTaskConflict) {
		writeError(w, 409, "task_conflict", "已有未结束任务或任务状态已变化，请刷新后重试")
		return
	}
	writeError(w, 500, "task_error", "任务状态保存或读取失败")
}
