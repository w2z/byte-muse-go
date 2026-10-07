package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"bytemuse/backend/internal/application"
)

// systemVersion 返回当前运行版本与发布仓库记录；refresh=true 绕过缓存重新检查。
// 远端检查失败仍返回 200：当前版本始终可用，失败原因放在 check_error，前端只按 has_update 决定是否提示升级。
func systemVersion(service *application.VersionService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "版本检查未就绪")
			return
		}
		refresh := false
		if value := request.URL.Query().Get("refresh"); value != "" {
			var err error
			refresh, err = strconv.ParseBool(value)
			if err != nil {
				writeError(response, http.StatusBadRequest, "invalid_argument", "refresh 必须为布尔值")
				return
			}
		}
		check := service.Status
		if refresh {
			check = service.Refresh
		}
		status, err := check(request.Context())
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

// systemUpgradeStatus 提供升级进度，旧镜像或开发环境以 enabled=false 表示能力不可用。
func systemUpgradeStatus(service *application.UpgradeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, service.Status()) }
}

// systemUpgrade 仅接受目标版本；下载地址由服务端固定仓库生成，请求受理后后台执行。
func systemUpgrade(service *application.UpgradeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, 400, "invalid_argument", "升级请求必须使用 application/json")
			return
		}
		var body struct {
			Target string `json:"target"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.Target == "" || decoder.Decode(new(any)) != io.EOF {
			writeError(w, 400, "invalid_argument", "请提供有效的目标版本")
			return
		}
		state, err := service.Start(body.Target)
		if errors.Is(err, application.ErrUpgradeUnavailable) {
			writeError(w, 503, "upgrade_unavailable", err.Error())
			return
		}
		if errors.Is(err, application.ErrUpgradeBusy) {
			writeError(w, 409, "upgrade_busy", err.Error())
			return
		}
		if err != nil {
			writeError(w, 400, "invalid_argument", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, state)
	}
}
