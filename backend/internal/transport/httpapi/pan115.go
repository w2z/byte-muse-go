package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
)

// startPan115Login 申请一次扫码登录，返回可直接渲染的二维码与会话标识。
func startPan115Login(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		session, err := service.StartLogin(request.Context())
		if err != nil {
			writePan115Error(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, session)
	}
}

// pan115LoginStatus 查询扫码状态；授权后返回账号快照，前端据此结束轮询。
func pan115LoginStatus(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		result, err := service.LoginStatus(request.Context(), chi.URLParam(request, "sessionId"))
		if err != nil {
			writePan115Error(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

// cancelPan115Login 主动结束一次扫码会话，避免页面关闭后二维码仍可被扫描。
func cancelPan115Login(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service != nil {
			service.CancelLogin(chi.URLParam(request, "sessionId"))
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

// startPan115CookieLogin 按渠道申请一次 Cookie 扫码，返回二维码与会话标识。
// 请求体缺省（空体）表示使用默认渠道，因此空体不算调用方错误。
func startPan115CookieLogin(service *application.Pan115Service) http.HandlerFunc {
	type requestBody struct {
		ClientType string `json:"client_type"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		var body requestBody
		if err := decodeJSON(response, request, &body); err != nil && !errors.Is(err, io.EOF) {
			writeError(response, http.StatusBadRequest, "invalid_request", "请求体不是有效的扫码渠道")
			return
		}
		session, err := service.StartCookieLogin(request.Context(), body.ClientType)
		if err != nil {
			writePan115Error(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, session)
	}
}

// pan115CookieLoginStatus 查询 Cookie 扫码状态；授权当次返回 Cookie 明文，之后不再重复返回。
func pan115CookieLoginStatus(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		result, err := service.CookieLoginStatus(request.Context(), chi.URLParam(request, "sessionId"))
		if err != nil {
			writePan115Error(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

// cancelPan115CookieLogin 主动结束一次 Cookie 扫码会话，避免页面关闭后二维码仍可被扫描。
func cancelPan115CookieLogin(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service != nil {
			service.CancelCookieLogin(chi.URLParam(request, "sessionId"))
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

// pan115Account 返回当前 115 绑定状态；未绑定时 linked 为 false 且 account 为空。
func pan115Account(service *application.Pan115Service) http.HandlerFunc {
	type accountResponse struct {
		Linked  bool                  `json:"linked"`
		Account *domain.Pan115Account `json:"account"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		account, err := service.Account(request.Context())
		if errors.Is(err, application.ErrPan115NotLinked) {
			writeJSON(response, http.StatusOK, accountResponse{Linked: false})
			return
		}
		if err != nil {
			writePan115Error(response, err)
			return
		}
		writeJSON(response, http.StatusOK, accountResponse{Linked: true, Account: &account})
	}
}

// unlinkPan115 解除 115 绑定并删除本地令牌。
func unlinkPan115(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		if err := service.Unlink(request.Context()); err != nil {
			writeApplicationError(response, err)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

// listPan115Files 读取一个 115 目录，供设置页选择离线下载的保存目录。
func listPan115Files(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		query := request.URL.Query()
		offset, err := pan115Offset(query.Get("offset"))
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid_pagination", "offset 必须是非负整数")
			return
		}
		limit, err := parsePositive(query.Get("limit"), 0)
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid_pagination", "limit 必须是正整数")
			return
		}
		page, err := service.Files(request.Context(), query.Get("directory_id"), offset, limit)
		if err != nil {
			writePan115Error(response, err)
			return
		}
		// 文件列表接口正常会回传父目录树，这里只在 115 未回传时兜底补一次路径。
		if len(page.Path) == 0 {
			path, err := service.DirectoryPath(request.Context(), page.DirectoryID)
			if err != nil {
				writePan115Error(response, err)
				return
			}
			page.Path = path
		}
		writeJSON(response, http.StatusOK, page)
	}
}

// scanPan115Library 递归扫描设置页配置的 115 目录，把识别出番号的视频登记为「已在媒体库」。
// 目录、格式与失败隔离规则由应用层统一决定，这里只负责把结果原样返回。
func scanPan115Library(service *application.Pan115LibraryService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		streamScan(response, request, service.Scan, writePan115Error)
	}
}

// listPan115OfflineTasks 读取一页 115 离线任务。
func listPan115OfflineTasks(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		page, err := parsePositive(request.URL.Query().Get("page"), 1)
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid_pagination", "page 必须是正整数")
			return
		}
		result, err := service.OfflineTasks(request.Context(), page)
		if err != nil {
			writePan115Error(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

// addPan115OfflineTask 手动提交一个磁力或下载地址到 115 离线下载。
func addPan115OfflineTask(service *application.Pan115Service) http.HandlerFunc {
	type requestBody struct {
		URI         string `json:"uri"`
		DirectoryID string `json:"directory_id"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		var body requestBody
		if err := decodeJSON(response, request, &body); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "请求体不是有效的离线任务")
			return
		}
		hash, err := service.AddOffline(request.Context(), body.URI, body.DirectoryID)
		if err != nil {
			writePan115Error(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, map[string]string{"hash": hash})
	}
}

// removePan115OfflineTask 删除一条 115 离线任务记录，不删除已下载的源文件。
func removePan115OfflineTask(service *application.Pan115Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "115 网盘服务尚未就绪")
			return
		}
		if err := service.RemoveOffline(request.Context(), chi.URLParam(request, "hash")); err != nil {
			writePan115Error(response, err)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

// pan115Offset 解析目录分页偏移；空值按 0 处理，负值视为非法。
func pan115Offset(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, application.ErrInvalidPagination
	}
	return value, nil
}

// writePan115Error 把 115 错误映射为稳定的 HTTP 语义：
// 未绑定、重复提交是状态冲突，参数错误是调用方问题，上游故障按网关错误处理，
// 其余错误（数据库等）交给通用映射，避免把内部故障伪装成 115 故障。
func writePan115Error(response http.ResponseWriter, err error) {
	var apiErr *pan115.APIError
	switch {
	case errors.Is(err, application.ErrPan115ScanNotConfigured):
		writeError(response, http.StatusConflict, "pan115_scan_not_configured", "尚未配置 115 扫描目录，请先在设置中添加目录")
	case errors.Is(err, application.ErrInvalidSetting):
		writeError(response, http.StatusBadRequest, "invalid_request", trimErrorPrefix(err, application.ErrInvalidSetting))
	case errors.Is(err, application.ErrPan115NotLinked):
		writeError(response, http.StatusConflict, "pan115_not_linked", "115 网盘尚未绑定账号，请先在设置中扫码登录")
	case errors.Is(err, application.ErrPan115LoginUnknown):
		writeError(response, http.StatusNotFound, "pan115_login_unknown", "115 扫码会话不存在或已过期，请重新获取二维码")
	case errors.Is(err, application.ErrPan115OfflineExists):
		writeError(response, http.StatusConflict, "pan115_offline_exists", "115 已存在该离线任务")
	case errors.Is(err, application.ErrPan115InvalidInput):
		message := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(err.Error(), application.ErrPan115InvalidInput.Error()), ":"))
		writeError(response, http.StatusBadRequest, "invalid_request", message)
	case errors.As(err, &apiErr):
		writeError(response, http.StatusBadGateway, "pan115_api_error", "115 返回错误："+apiErr.Error())
	case pan115.Unavailable(err):
		writeError(response, http.StatusBadGateway, "pan115_unavailable", "115 网盘暂时不可用，请稍后重试")
	default:
		writeApplicationError(response, err)
	}
}
