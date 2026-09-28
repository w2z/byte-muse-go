package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/ports"
	"net/http"
)

// listTags 查询已存标签，不采集外站，也不创建订阅或下载任务。
func listTags(service *application.TagService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "标签目录未就绪")
			return
		}
		page, size, ok := paginationWithLimit(w, r, 15, ports.MaxPageSize)
		if !ok {
			return
		}
		result, err := service.List(r.Context(), page, size, r.URL.Query().Get("search"))
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
