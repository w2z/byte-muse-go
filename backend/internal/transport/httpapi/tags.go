package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/ports"
	"github.com/go-chi/chi/v5"
	"net/http"
)

// listTags 查询已存标签，不采集外站，也不创建订阅或下载任务。
func listTags(service *application.TagService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "标签目录未就绪")
			return
		}
		page, size, ok := paginationWithLimit(w, r, 15, ports.MaxTagPageSize)
		if !ok {
			return
		}
		result, err := service.List(r.Context(), page, size, r.URL.Query().Get("search"), r.URL.Query().Get("subscription"), r.URL.Query().Get("category"))
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// saveTagSubscription 创建或编辑追新起始日期，由服务触发同一追新规则。
func saveTagSubscription(service *application.TagService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "service_unavailable", "标签服务未就绪")
			return
		}
		var body struct {
			LimitDate string `json:"limit_date"`
		}
		if decodeJSON(w, r, &body) != nil {
			writeError(w, 400, "invalid_request", "请求体不是有效 JSON")
			return
		}
		item, e := service.SaveSubscription(r.Context(), chi.URLParam(r, "tagName"), body.LimitDate)
		if e != nil {
			writeApplicationError(w, e)
			return
		}
		writeJSON(w, 200, item)
	}
}

// cancelTagSubscription 仅停止标签追新，重复取消不影响影片订阅。
func cancelTagSubscription(service *application.TagService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "service_unavailable", "标签服务未就绪")
			return
		}
		item, e := service.CancelSubscription(r.Context(), chi.URLParam(r, "tagName"))
		if e != nil {
			writeApplicationError(w, e)
			return
		}
		writeJSON(w, 200, item)
	}
}
