package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/clouddrive"
	"bytemuse/backend/internal/platform/pan115"
)

// strmProxyClient 转发需要携带固定请求头的网盘直链；不设置整体超时，避免长视频被中途中断。
var strmProxyClient = &http.Client{}

// hopByHopHeaders 是转发时不能透传的连接级响应头。
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// listStrmDirectories 列出 strm 根目录下某个目录的直接子目录。
// 服务端每次请求都实时读盘，因此外部新建的目录只要重新请求就能看到。
func listStrmDirectories(service *application.StrmService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "strm 服务尚未就绪")
			return
		}
		page, err := service.Directories(request.Context(), request.URL.Query().Get("path"))
		if err != nil {
			writeStrmError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, page)
	}
}

// createStrmDirectory 在 strm 根目录内新建一级目录，供设置页手动维护本地路径。
func createStrmDirectory(service *application.StrmService) http.HandlerFunc {
	type requestBody struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "strm 服务尚未就绪")
			return
		}
		var body requestBody
		if err := decodeJSON(response, request, &body); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "请求体不是有效的目录信息")
			return
		}
		created, err := service.CreateDirectory(request.Context(), body.Path, body.Name)
		if err != nil {
			writeStrmError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, created)
	}
}

// listStrmCloudDriveDirectories 列出 CloudDrive2 中某个目录的子目录，供设置页选择网盘路径。
// 与本地 strm 目录浏览相互独立：这里的数据来自网盘，因此不受本地根目录限制。
func listStrmCloudDriveDirectories(service *application.StrmService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "strm 服务尚未就绪")
			return
		}
		page, err := service.CloudDriveDirectories(request.Context(), request.URL.Query().Get("path"))
		if err != nil {
			writeStrmError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, page)
	}
}

// scanStrm 按当前设置生成 strm 文件；未配置 STRM_PLAY_BASE 时用本次请求的来源兜底。
func scanStrm(service *application.StrmService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "strm 服务尚未就绪")
			return
		}
		result, err := service.Scan(request.Context(), requestBaseURL(request))
		if err != nil {
			writeStrmError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

// playStrmFile 把 strm 里的播放地址解析为真实媒体地址：
// 115 直链绑定 User-Agent，直接 302；CloudDrive2 直链可能要求固定请求头，此时由服务端转发。
// 该端点不要求会话，Emby 等播放器需要直接请求；安全性由 strm 目录自身的访问控制决定。
func playStrmFile(service *application.StrmService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "strm 服务尚未就绪")
			return
		}
		kind, fileID, err := strmPlayTarget(request)
		if err != nil {
			writeStrmError(response, err)
			return
		}
		target, err := service.PlayURL(request.Context(), kind, fileID, request.Header.Get("User-Agent"))
		if err != nil {
			writeStrmError(response, err)
			return
		}
		if target.Proxy != nil {
			proxyStrmPlay(response, request, target.Proxy)
			return
		}
		http.Redirect(response, request, target.Redirect, http.StatusFound)
	}
}

// strmPlayTarget 从请求路径中取出网盘类型与文件标识。
// 直接基于转义后的原始路径解析，避免路由库按 / 拆段时丢掉 CloudDrive2 的目录层级。
func strmPlayTarget(request *http.Request) (string, string, error) {
	const prefix = "/files/play/"
	escaped := request.URL.EscapedPath()
	if !strings.HasPrefix(escaped, prefix) {
		return "", "", fmt.Errorf("%w: 播放地址不完整", application.ErrStrmInvalidInput)
	}
	return application.ParseStrmPlayPath(strings.TrimPrefix(escaped, prefix))
}

// proxyStrmPlay 把播放请求转发到网盘直链，并带上直链要求的 User-Agent 与附加请求头。
// 只透传 Range 与响应状态，使播放器仍可拖动进度。
func proxyStrmPlay(response http.ResponseWriter, request *http.Request, target *domain.StrmProxyTarget) {
	proxyRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, target.URL, nil)
	if err != nil {
		writeError(response, http.StatusBadGateway, "strm_proxy_failed", "构造网盘直链请求失败")
		return
	}
	if target.UserAgent != "" {
		proxyRequest.Header.Set("User-Agent", target.UserAgent)
	}
	for name, value := range target.Headers {
		proxyRequest.Header.Set(name, value)
	}
	if ranges := request.Header.Get("Range"); ranges != "" {
		proxyRequest.Header.Set("Range", ranges)
	}
	proxyResponse, err := strmProxyClient.Do(proxyRequest)
	if err != nil {
		writeError(response, http.StatusBadGateway, "strm_proxy_failed", "请求网盘直链失败")
		return
	}
	defer proxyResponse.Body.Close()
	for name, values := range proxyResponse.Header {
		if hopByHopHeaders[name] {
			continue
		}
		for _, value := range values {
			response.Header().Add(name, value)
		}
	}
	response.WriteHeader(proxyResponse.StatusCode)
	if request.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(response, proxyResponse.Body)
}

// requestBaseURL 推导本次请求观测到的 ByteMuse 对外基址，用于未配置基址时的兜底。
// 反向代理场景下优先采用 X-Forwarded-Proto 与 X-Forwarded-Host。
func requestBaseURL(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	if forwarded := firstForwarded(request.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = forwarded
	}
	host := strings.TrimSpace(request.Host)
	if forwarded := firstForwarded(request.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// firstForwarded 取出逗号分隔的转发头里的第一个值。
func firstForwarded(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

// writeStrmError 把 strm 错误映射为稳定的 HTTP 语义：
// 参数问题与配置内容非法是调用方问题，未配置与未绑定是状态冲突，
// 网盘侧的文件缺失与直链缺失按 404/502 处理，其余交给通用映射。
func writeStrmError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrStrmInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_request", trimErrorPrefix(err, application.ErrStrmInvalidInput))
	case errors.Is(err, application.ErrInvalidSetting):
		writeError(response, http.StatusBadRequest, "invalid_request", trimErrorPrefix(err, application.ErrInvalidSetting))
	case errors.Is(err, application.ErrPan115InvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_request", trimErrorPrefix(err, application.ErrPan115InvalidInput))
	case errors.Is(err, pan115.ErrInvalidFileID):
		writeError(response, http.StatusBadRequest, "invalid_request", "网盘文件标识非法")
	case errors.Is(err, application.ErrStrmNotConfigured):
		writeError(response, http.StatusConflict, "strm_not_configured", trimErrorPrefix(err, application.ErrStrmNotConfigured))
	case errors.Is(err, application.ErrPan115NotLinked):
		writeError(response, http.StatusConflict, "pan115_not_linked", "115 网盘尚未绑定账号，请先在设置中扫码登录")
	case errors.Is(err, pan115.ErrFileNotFound):
		writeError(response, http.StatusNotFound, "strm_file_not_found", "网盘中不存在该文件")
	case errors.Is(err, pan115.ErrDownloadUnavailable):
		writeError(response, http.StatusBadGateway, "strm_download_unavailable", "网盘未返回可用的播放地址")
	case errors.Is(err, clouddrive.ErrNotConfigured):
		writeError(response, http.StatusConflict, "strm_not_configured", "CloudDrive2 尚未配置")
	case errors.Is(err, clouddrive.ErrUnauthorized):
		writeError(response, http.StatusBadGateway, "clouddrive_unauthorized", trimErrorPrefix(err, clouddrive.ErrUnauthorized))
	default:
		writeApplicationError(response, err)
	}
}

// trimErrorPrefix 去掉错误链上的哨兵前缀，只把可读原因返回给调用方。
func trimErrorPrefix(err error, sentinel error) string {
	message := strings.TrimPrefix(err.Error(), sentinel.Error())
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(message), ":"))
}
