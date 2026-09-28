package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/ports"
)

// collectionSources 公开已实现能力；未装配服务时明确返回不可用。
func collectionSources(service *application.CollectionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "collection_unavailable", "采集服务未启用")
			return
		}
		writeJSON(w, 200, service.Sources())
	}
}

// runCollection 只接受显式 POST，避免查询页面意外触发外部抓取及数据库写入。
func runCollection(service *application.CollectionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "collection_unavailable", "采集服务未启用")
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		var req ports.CollectionRequest
		if err := decoder.Decode(&req); err != nil {
			writeError(w, 400, "invalid_collection_request", "采集参数无效")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeError(w, 400, "invalid_collection_request", "仅允许一个请求对象")
			return
		}
		result, err := service.Enqueue(r.Context(), req)
		if err != nil {
			status, code, message := 500, "collection_enqueue_failed", "采集任务入队失败"
			switch {
			case errors.Is(err, collector.ErrInvalidRequest), errors.Is(err, collector.ErrUnsupported):
				status, code, message = 400, "invalid_collection_request", "来源、能力或查询参数无效"
			}
			writeError(w, status, code, message)
			return
		}
		w.Header().Set("Location", "/api/v1/collection/runs/"+result.ID)
		writeJSON(w, http.StatusAccepted, result)
	}
}

// collectionRunStatus 查询持久化批次，不等待网络请求，也不触发任何处理。
func collectionRunStatus(service *application.CollectionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "collection_unavailable", "采集服务未启用")
			return
		}
		result, e := service.RunStatus(r.Context(), chi.URLParam(r, "runId"))
		if e != nil {
			if errors.Is(e, ports.ErrCollectionRunNotFound) {
				writeError(w, 404, "collection_run_not_found", "采集任务不存在")
			} else {
				writeError(w, 500, "collection_status_failed", "读取采集状态失败")
			}
			return
		}
		writeJSON(w, 200, result)
	}
}
