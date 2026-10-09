package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/ports"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
)

// listDownloadSourceSites exposes all persisted source categories behind the downloads auth boundary.
func listDownloadSourceSites(service *application.DownloadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "service_unavailable", "下载任务服务未就绪")
			return
		}
		items, err := service.SourceSites(r.Context())
		if err != nil {
			writeError(w, 500, "download_source_sites_failed", "读取资源站分类失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// controlDownload 提供鉴权任务控制入口；状态冲突和下载器结果不确定分别返回 409、502。
func controlDownload(service *application.DownloadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "service_unavailable", "下载任务服务未就绪")
			return
		}
		action := chi.URLParam(r, "action")
		if r.Method == http.MethodPost && action != "pause" && action != "stop" && action != "resume" && action != "retry" {
			writeError(w, 400, "invalid_download_action", "操作必须为 pause、stop、resume 或 retry")
			return
		}
		if r.Method == http.MethodDelete {
			action = "delete"
			switch r.URL.Query().Get("delete_files") {
			case "", "false":
			case "true":
				action = "delete_files"
			default:
				writeError(w, 400, "invalid_download_action", "delete_files 必须为 true 或 false")
				return
			}
		}
		err := service.Control(r.Context(), chi.URLParam(r, "taskId"), action)
		if err != nil {
			switch {
			case errors.Is(err, ports.ErrDownloadNotFound):
				writeError(w, 404, "download_not_found", err.Error())
			case errors.Is(err, ports.ErrDownloadConflict):
				writeError(w, 409, "download_conflict", err.Error())
			case errors.Is(err, ports.ErrDownloadAction):
				writeError(w, 409, "download_action_unsupported", err.Error())
			default:
				writeError(w, 502, "download_control_unconfirmed", "下载器操作未完成确认，请刷新后检查任务状态")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
