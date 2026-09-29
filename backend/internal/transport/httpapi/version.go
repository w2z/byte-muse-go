package httpapi

import (
	"errors"
	"net/http"

	"bytemuse/backend/internal/application"
)

// systemVersion 返回当前运行版本与发布仓库记录的版本。
// 远端检查失败仍返回 200：当前版本始终可用，失败原因放在 check_error，前端只按 has_update 决定是否提示升级。
func systemVersion(service *application.VersionService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "版本检查未就绪")
			return
		}
		status, err := service.Status(request.Context())
		if errors.Is(err, application.ErrVersionUnavailable) {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "版本检查未就绪")
			return
		}
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, status)
	}
}
