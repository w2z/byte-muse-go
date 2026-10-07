package httpapi

import (
	"bytemuse/backend/internal/application"
	"net/http"
	"strconv"
)

// listUploadDirectories browses server-local directories behind the authenticated settings API.
func listUploadDirectories(w http.ResponseWriter, r *http.Request) {
	page, err := application.UploadDirectories(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_directory", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// uploadStatus exposes only operational progress and never provider credentials.
func uploadStatus(service *application.UploadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "网盘上传服务尚未就绪")
			return
		}
		writeJSON(w, http.StatusOK, service.Status())
	}
}

// uploadDirectoriesProgress returns one row per configured mapping.
func uploadDirectoriesProgress(service *application.UploadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "service_unavailable", "网盘上传服务尚未就绪")
			return
		}
		writeJSON(w, 200, map[string]any{"items": service.Directories()})
	}
}

// uploadFilesProgress returns bounded rows and rejects invalid paging inputs.
func uploadFilesProgress(service *application.UploadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, 503, "service_unavailable", "网盘上传服务尚未就绪")
			return
		}
		page, size := 1, 20
		var err error
		if raw := r.URL.Query().Get("page"); raw != "" {
			page, err = strconv.Atoi(raw)
			if err != nil || page < 1 || page > 1000000 {
				writeError(w, 400, "invalid_request", "page 必须为 1 到 1000000")
				return
			}
		}
		if raw := r.URL.Query().Get("page_size"); raw != "" {
			size, err = strconv.Atoi(raw)
			if err != nil || size < 1 || size > 100 {
				writeError(w, 400, "invalid_request", "page_size 必须为 1 到 100")
				return
			}
		}
		writeJSON(w, 200, service.Files(page, size))
	}
}
