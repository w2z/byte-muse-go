// Package httpapi exposes application services through the public REST contract.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/covercache"
	"bytemuse/backend/internal/ports"
	"bytemuse/backend/internal/scheduler"
)

// Dependencies contains the services required by the HTTP transport.
type Dependencies struct {
	// ScanTasks 管理扫描入库与 STRM 生成的独立持久化任务。
	ScanTasks             *application.ScanTasks
	Tags                  *application.TagService
	Collection            *application.CollectionService
	Auth                  *auth.Service
	Catalog               *application.CatalogService
	CatalogQueries        *application.CatalogQueryService
	Actors                *application.ActorService
	Subscriptions         *application.SubscriptionService
	SubscriptionDownloads *application.SubscriptionDownloadService
	Downloads             *application.DownloadService
	Dashboard             *application.DashboardService
	Settings              *application.SettingsService
	// Version 提供当前运行版本与发布仓库版本的比较结果，供顶栏版本标签使用。
	Version *application.VersionService
	Pan115  *application.Pan115Service
	// Pan115Library 把设置页的 115 扫描目录递归扫描后登记到媒体库。
	Pan115Library *application.Pan115LibraryService
	Scheduler     *scheduler.Manager
	Logs          *logging.Logger
	Readiness     ports.ReadinessProbe
	// WeChatCallback 处理企业微信回调；未配置时为 nil，回调端点返回 503。
	WeChatCallback ports.CallbackVerifier
	// ChannelMessages 处理渠道入站消息；轮询与回调共用同一实现。
	ChannelMessages ports.ChannelMessageHandler
	// Strm 提供网盘目录的 strm 生成、本地 strm 目录浏览与播放地址解析。
	Strm      *application.StrmService
	EmbyMedia *application.EmbyMediaService
	// Covers 是影片封面的本地缓存，页面图片统一从这里取，源站不可用时回退原地址。
	Covers    *covercache.Cache
	StaticDir string
}

// publicAuthPaths 是不要求既有会话即可访问的认证入口：登录本身，以及用过期会话换取新会话的续签。
var publicAuthPaths = map[string]struct{}{
	"/api/v1/auth/login":   {},
	"/api/v1/auth/refresh": {},
	// 企业微信回调由平台服务器直接请求，没有会话；安全性由回调签名与 AES 解密保证。
	"/api/v1/message": {},
}

// New builds the API router and SPA static-file fallback.
func New(dependencies Dependencies) http.Handler {
	api := chi.NewRouter()
	api.Get("/health/live", live)
	api.Get("/health/ready", ready(dependencies.Readiness))
	// strm 播放地址：Emby 等播放器直接请求，没有会话；路径由服务端生成，不可枚举。
	api.HandleFunc("/files/play/*", playStrmFile(dependencies.Strm))
	api.Route("/api/v1", func(router chi.Router) {
		router.Get("/collection/sources", collectionSources(dependencies.Collection))
		router.Post("/collection/runs", runCollection(dependencies.Collection))
		router.Get("/collection/runs/{runId}", collectionRunStatus(dependencies.Collection))
		router.Post("/auth/login", login(dependencies.Auth))
		router.Post("/auth/refresh", refresh(dependencies.Auth))
		router.Post("/auth/logout", logout(dependencies.Auth))
		router.Get("/dashboard", dashboard(dependencies.Dashboard))
		router.Get("/media", listMedia(dependencies.Catalog))
		router.Get("/actors", listActors(dependencies.Actors))
		router.Get("/tags", listTags(dependencies.Tags))
		router.Put("/tags/{tagName}/subscription", saveTagSubscription(dependencies.Tags))
		router.Delete("/tags/{tagName}/subscription", cancelTagSubscription(dependencies.Tags))
		router.Put("/actors/{actorName}/subscription", saveActorSubscription(dependencies.Actors))
		router.Delete("/actors/{actorName}/subscription", cancelActorSubscription(dependencies.Actors))
		router.Get("/ranks", listRank(dependencies.CatalogQueries))
		router.Get("/codes/release_today", listReleaseToday(dependencies.CatalogQueries))
		router.Get("/codes/recommend", listRecommendations(dependencies.CatalogQueries))
		router.Get("/complex/search", searchCatalog(dependencies.CatalogQueries, dependencies.Tags))
		router.Get("/tasks", listScheduledTasks(dependencies.Scheduler))
		router.Post("/tasks/{taskName}/run", runScheduledTask(dependencies.Scheduler))
		router.Get("/logs", listLogs(dependencies.Logs))
		router.Delete("/logs", clearLogs(dependencies.Logs))
		router.Get("/media/{mediaId}", getMedia(dependencies.Catalog))
		// 封面缓存入口：命中 /data/cover 直接返回本地文件，未命中下载后按番号落盘。
		router.Get("/covers/{code}", serveCover(dependencies.Covers))
		router.Get("/subscriptions", listSubscriptions(dependencies.Subscriptions))
		router.Post("/subscriptions", createSubscription(dependencies.Subscriptions))
		router.Put("/subscriptions/{subscriptionId}", updateSubscription(dependencies.Subscriptions))
		router.Post("/subscriptions/{subscriptionId}/cancel", cancelSubscription(dependencies.Subscriptions))
		router.Post("/subscriptions/{subscriptionId}/download", enqueueSubscriptionDownload(dependencies.SubscriptionDownloads))
		router.Get("/downloads", listDownloads(dependencies.Downloads))
		router.Post("/downloads/{taskId}/{action}", controlDownload(dependencies.Downloads))
		router.Delete("/downloads/{taskId}", controlDownload(dependencies.Downloads))
		router.Get("/system/settings", getSystemSettings(dependencies.Settings))
		router.Put("/system/settings", updateSystemSettings(dependencies.Settings))
		router.Post("/system/settings/openai/test", testOpenAI)
		router.Get("/system/version", systemVersion(dependencies.Version))
		router.Post("/pan115/login/sessions", startPan115Login(dependencies.Pan115))
		router.Get("/pan115/login/sessions/{sessionId}", pan115LoginStatus(dependencies.Pan115))
		router.Delete("/pan115/login/sessions/{sessionId}", cancelPan115Login(dependencies.Pan115))
		router.Post("/pan115/cookie/login/sessions", startPan115CookieLogin(dependencies.Pan115))
		router.Get("/pan115/cookie/login/sessions/{sessionId}", pan115CookieLoginStatus(dependencies.Pan115))
		router.Delete("/pan115/cookie/login/sessions/{sessionId}", cancelPan115CookieLogin(dependencies.Pan115))
		router.Get("/pan115/account", pan115Account(dependencies.Pan115))
		router.Delete("/pan115/account", unlinkPan115(dependencies.Pan115))
		router.Get("/pan115/files", listPan115Files(dependencies.Pan115))
		router.Post("/pan115/library/scan", scanPan115Library(dependencies.Pan115Library, dependencies.ScanTasks))
		router.Get("/pan115/library/scan/task", scanTaskEndpoint(dependencies.ScanTasks, "library", false))
		router.Post("/pan115/library/scan/tasks/{id}/control", scanTaskEndpoint(dependencies.ScanTasks, "library", true))
		router.Get("/pan115/offline/tasks", listPan115OfflineTasks(dependencies.Pan115))
		router.Post("/pan115/offline/tasks", addPan115OfflineTask(dependencies.Pan115))
		router.Delete("/pan115/offline/tasks/{hash}", removePan115OfflineTask(dependencies.Pan115))
		router.Get("/strm/directories", listStrmDirectories(dependencies.Strm))
		router.Post("/strm/directories", createStrmDirectory(dependencies.Strm))
		router.Get("/strm/clouddrive/directories", listStrmCloudDriveDirectories(dependencies.Strm))
		router.Post("/strm/scan", scanStrm(dependencies.Strm, dependencies.ScanTasks))
		router.Get("/strm/scan/task", scanTaskEndpoint(dependencies.ScanTasks, "strm", false))
		router.Post("/strm/scan/tasks/{id}/control", scanTaskEndpoint(dependencies.ScanTasks, "strm", true))
		router.Post("/strm/emby/media-info/refresh", refreshStrmMediaInfo(dependencies.EmbyMedia))
		router.Get("/strm/emby/media-info/task", strmMediaInfoTask(dependencies.EmbyMedia))
		router.Post("/strm/emby/media-info/tasks/{id}/control", controlStrmMediaInfo(dependencies.EmbyMedia))
		router.Get("/message", wechatVerify(dependencies))
		router.Post("/message", wechatReceive(dependencies))
	})

	spa := spaHandler(dependencies.StaticDir)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		// strm 播放地址由 Emby 等播放器直接请求，没有会话；能否播放由地址本身是否可知决定。
		if strings.HasPrefix(request.URL.Path, "/files/play/") {
			api.ServeHTTP(response, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/") || strings.HasPrefix(request.URL.Path, "/health/") {
			if strings.HasPrefix(request.URL.Path, "/api/v1") {
				if _, public := publicAuthPaths[request.URL.Path]; !public && !authenticated(request, dependencies.Auth) {
					writeError(response, http.StatusUnauthorized, "unauthorized", "未登录或会话失效")
					return
				}
			}
			api.ServeHTTP(response, request)
			return
		}
		spa.ServeHTTP(response, request)
	})
}

func refreshStrmMediaInfo(service *application.EmbyMediaService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Emby 媒体信息服务未就绪")
			return
		}
		task, created, err := service.Enqueue(r.Context())
		if err != nil {
			writeError(w, http.StatusBadRequest, "emby_media_refresh_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"task": task, "queued": created})
	}
}
func strmMediaInfoTask(service *application.EmbyMediaService) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if service == nil {
			writeJSON(w, http.StatusOK, nil)
			return
		}
		writeJSON(w, http.StatusOK, service.Snapshot())
	}
}

func controlStrmMediaInfo(service *application.EmbyMediaService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Emby 媒体信息服务未就绪")
			return
		}
		var body struct {
			Action string `json:"action"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil || (body.Action != "pause" && body.Action != "resume" && body.Action != "cancel") {
			writeError(w, http.StatusBadRequest, "invalid_request", "action 必须为 pause、resume 或 cancel")
			return
		}
		task, err := service.Control(chi.URLParam(r, "id"), body.Action)
		if errors.Is(err, application.ErrScanTaskConflict) {
			writeError(w, http.StatusConflict, "task_conflict", err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "task_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

// enqueueSubscriptionDownload schedules a single active subscription without bypassing the durable worker.
func enqueueSubscriptionDownload(service *application.SubscriptionDownloadService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "订阅下载未就绪")
			return
		}
		// 后台手动触发属于用户显式发起的下载，失败通知必须推送。
		id, e := service.Enqueue(request.Context(), chi.URLParam(request, "subscriptionId"), ports.DownloadOriginUser)
		if e != nil {
			writeApplicationError(response, e)
			return
		}
		writeJSON(response, http.StatusAccepted, map[string]string{"task_id": id})
	}
}

// listLogs exposes persisted process logs used by the management page.
// Filters are read-only and share the logging package's stable vocabularies.
func listLogs(logger *logging.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		page, pageSize, ok := paginationWithLimit(response, request, 100, logging.MaxPageSize)
		if !ok {
			return
		}
		startTime, endTime, ok := logTimeRange(response, request)
		if !ok {
			return
		}
		query := logging.Query{
			Level:     logging.Level(request.URL.Query().Get("level")),
			Category:  logging.Category(request.URL.Query().Get("category")),
			Keyword:   request.URL.Query().Get("keyword"),
			StartTime: startTime, EndTime: endTime,
			Page: page, PageSize: pageSize,
		}
		if query.Level != "" && !query.Level.Valid() {
			writeError(response, http.StatusBadRequest, "invalid_log_filter", "日志级别无效")
			return
		}
		if query.Category != "" && !query.Category.Valid() {
			writeError(response, http.StatusBadRequest, "invalid_log_filter", "日志分类无效")
			return
		}
		if logger == nil {
			writeJSON(response, http.StatusOK, application.Page[logging.Record]{Page: page, PageSize: pageSize, Total: 0, Items: []logging.Record{}})
			return
		}
		items, total, err := logger.Search(request.Context(), query)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "log_query_failed", "日志查询失败")
			return
		}
		writeJSON(response, http.StatusOK, application.Page[logging.Record]{Page: page, PageSize: pageSize, Total: total, Items: items})
	}
}

// clearLogs 按当前筛选条件永久删除系统日志；无条件时删除全部日志。
func clearLogs(logger *logging.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		startTime, endTime, ok := logTimeRange(response, request)
		if !ok {
			return
		}
		query := logging.Query{
			Level:     logging.Level(request.URL.Query().Get("level")),
			Category:  logging.Category(request.URL.Query().Get("category")),
			Keyword:   request.URL.Query().Get("keyword"),
			StartTime: startTime, EndTime: endTime,
		}
		if query.Level != "" && !query.Level.Valid() {
			writeError(response, http.StatusBadRequest, "invalid_log_filter", "日志级别无效")
			return
		}
		if query.Category != "" && !query.Category.Valid() {
			writeError(response, http.StatusBadRequest, "invalid_log_filter", "日志分类无效")
			return
		}
		if logger != nil {
			if _, err := logger.Clear(request.Context(), query); err != nil {
				writeError(response, http.StatusInternalServerError, "log_clear_failed", "日志清空失败")
				return
			}
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

func live(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func ready(probe ports.ReadinessProbe) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if probe == nil || probe.Ready(request.Context()) != nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪")
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func login(service *auth.Service) http.HandlerFunc {
	type requestBody struct {
		Username string `json:"username"`
		Password string `json:"password"`
		// Remember 省略时按 false 处理，会话维持 1 天；为 true 时按“记住密码”签发 30 天。
		Remember bool `json:"remember"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		var body requestBody
		if service == nil || decodeJSON(response, request, &body) != nil {
			logging.Error(logging.CategorySystem, "管理员登录失败", "error", "请求参数无效", "ip", requestIP(request))
			writeError(response, http.StatusUnauthorized, "unauthorized", "用户名或密码错误")
			return
		}
		session, err := service.Login(body.Username, body.Password, body.Remember)
		if err != nil {
			logging.Error(logging.CategorySystem, "管理员登录失败", "error", "用户名或密码错误", "ip", requestIP(request))
			writeError(response, http.StatusUnauthorized, "unauthorized", "用户名或密码错误")
			return
		}
		writeSession(response, service, session)
		logging.Info(logging.CategorySystem, "管理员登录成功", "username", body.Username)
	}
}

// refresh 在会话仍有效、或过期未超过宽限窗口时续签 Cookie；超出宽限返回 401，由前端跳转登录页。
func refresh(service *auth.Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		cookie, cookieErr := request.Cookie(auth.CookieName())
		if service == nil || cookieErr != nil {
			writeError(response, http.StatusUnauthorized, "unauthorized", "会话已失效，请重新登录")
			return
		}
		session, err := service.Refresh(cookie.Value)
		if err != nil {
			writeError(response, http.StatusUnauthorized, "unauthorized", "会话已失效，请重新登录")
			return
		}
		writeSession(response, service, session)
	}
}

func requestIP(request *http.Request) string {
	if request == nil || request.RemoteAddr == "" {
		return "未知"
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}

// logout 立即使浏览器丢弃当前会话 Cookie；服务端会话本身无状态，无需额外存储撤销记录。
func logout(service *auth.Service) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		if service == nil {
			writeError(response, http.StatusInternalServerError, "internal_error", "服务内部错误")
			return
		}
		http.SetCookie(response, service.ClearCookie())
		response.WriteHeader(http.StatusNoContent)
	}
}

// writeSession 下发新的会话 Cookie，并回写契约要求的当前用户。
func writeSession(response http.ResponseWriter, service *auth.Service, session auth.Session) {
	http.SetCookie(response, service.Cookie(session))
	user, err := service.Validate(session.Token)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "服务内部错误")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"user": user})
}

func authenticated(request *http.Request, service *auth.Service) bool {
	if service == nil {
		return false
	}
	cookie, err := request.Cookie(auth.CookieName())
	if err != nil {
		return false
	}
	_, err = service.Validate(cookie.Value)
	return err == nil
}

func dashboard(service *application.DashboardService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		result, err := service.Get(request.Context())
		if err != nil {
			logging.Error(logging.CategorySystem, "仪表盘查询失败")
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func listMedia(service *application.CatalogService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		page, pageSize, ok := pagination(response, request)
		if !ok {
			return
		}
		search := request.URL.Query().Get("search")
		if search == "" {
			search = request.URL.Query().Get("query")
		}
		subscription := request.URL.Query().Get("subscription")
		if subscription == "" {
			subscription = request.URL.Query().Get("subscription_status")
		}
		result, err := service.List(request.Context(), page, pageSize, ports.MediaListQuery{
			Search:             search,
			SubscriptionStatus: subscription,
			DownloadStatus:     request.URL.Query().Get("download"),
			LibraryStatus:      request.URL.Query().Get("library"),
			VideoType:          request.URL.Query().Get("video_type"),
		})
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func saveActorSubscription(service *application.ActorService) http.HandlerFunc {
	type requestBody struct {
		LimitDate string `json:"limit_date"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		var body requestBody
		if err := decodeJSON(response, request, &body); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "请求体不是有效 JSON")
			return
		}
		item, err := service.SaveSubscription(request.Context(), chi.URLParam(request, "actorName"), body.LimitDate)
		if err != nil {
			logging.Error(logging.CategorySubscription, "演员订阅保存失败", "actor", chi.URLParam(request, "actorName"))
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, item)
		logging.Info(logging.CategorySubscription, "演员订阅已保存", "actor", chi.URLParam(request, "actorName"))
	}
}

func cancelActorSubscription(service *application.ActorService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		item, err := service.CancelSubscription(request.Context(), chi.URLParam(request, "actorName"))
		if err != nil {
			logging.Error(logging.CategorySubscription, "演员订阅取消失败", "actor", chi.URLParam(request, "actorName"))
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, item)
		logging.Info(logging.CategorySubscription, "演员订阅已取消", "actor", chi.URLParam(request, "actorName"))
	}
}

func listRank(service *application.CatalogQueryService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "榜单服务尚未就绪")
			return
		}
		page, pageSize, ok := pagination(response, request)
		if !ok {
			return
		}
		result, err := service.Rank(request.Context(), request.URL.Query().Get("type"), page, pageSize, ports.MediaListQuery{SubscriptionStatus: request.URL.Query().Get("subscription"), VideoType: request.URL.Query().Get("video_type")})
		if err != nil {
			logging.Error(logging.CategoryCollection, "榜单查询失败", "rank_type", request.URL.Query().Get("type"))
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
		logging.Info(logging.CategoryCollection, "榜单查询完成", "rank_type", request.URL.Query().Get("type"), "count", len(result.Items))
	}
}

func listReleaseToday(service *application.CatalogQueryService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "上新服务尚未就绪")
			return
		}
		page, pageSize, ok := pagination(response, request)
		if !ok {
			return
		}
		result, err := service.ReleaseToday(request.Context(), page, pageSize, ports.MediaListQuery{SubscriptionStatus: request.URL.Query().Get("subscription"), VideoType: request.URL.Query().Get("video_type")})
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func listRecommendations(service *application.CatalogQueryService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "推荐服务尚未就绪")
			return
		}
		page, pageSize, ok := pagination(response, request)
		if !ok {
			return
		}
		result, err := service.Recommend(request.Context(), page, pageSize, ports.MediaListQuery{SubscriptionStatus: request.URL.Query().Get("subscription"), VideoType: request.URL.Query().Get("video_type")})
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func searchCatalog(service *application.CatalogQueryService, tags ...*application.TagService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "搜索服务尚未就绪")
			return
		}
		page, pageSize, ok := pagination(response, request)
		if !ok {
			return
		}
		if tag := strings.TrimSpace(request.URL.Query().Get("tag")); tag != "" {
			if len(tags) == 0 || tags[0] == nil {
				writeError(response, 503, "service_unavailable", "标签服务未就绪")
				return
			}
			result, err := tags[0].SearchMedia(request.Context(), tag, page, pageSize)
			if err != nil {
				writeApplicationError(response, err)
				return
			}
			writeJSON(response, 200, result)
			return
		}
		result, err := service.Search(request.Context(), request.URL.Query().Get("q"), page, pageSize)
		if err != nil {
			logging.Error(logging.CategorySubscription, "资源搜索失败")
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
		logging.Info(logging.CategorySubscription, "资源搜索完成", "count", len(result.Items))
	}
}

func listActors(service *application.ActorService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "演员服务尚未就绪")
			return
		}
		page, pageSize, ok := pagination(response, request)
		if !ok {
			return
		}
		result, err := service.List(request.Context(), page, pageSize, request.URL.Query().Get("subscription"), request.URL.Query().Get("keywords"))
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getMedia(service *application.CatalogService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		item, err := service.Get(request.Context(), chi.URLParam(request, "mediaId"))
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, item)
	}
}

func listSubscriptions(service *application.SubscriptionService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		page, pageSize, ok := pagination(response, request)
		if !ok {
			return
		}
		result, err := service.List(request.Context(), page, pageSize, domain.SubscriptionStatus(request.URL.Query().Get("status")))
		if err != nil {
			logging.Error(logging.CategorySubscription, "订阅查询失败")
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func createSubscription(service *application.SubscriptionService) http.HandlerFunc {
	type requestBody struct {
		MediaID string                  `json:"media_id"`
		Mode    domain.SubscriptionMode `json:"mode"`
		Filter  map[string]any          `json:"filter"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		var body requestBody
		if err := decodeJSON(response, request, &body); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "请求体不是有效 JSON")
			return
		}
		item, created, err := service.Create(request.Context(), application.CreateSubscriptionCommand{
			IdempotencyKey: request.Header.Get("Idempotency-Key"), MediaID: body.MediaID, Mode: body.Mode, Filter: body.Filter,
		})
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(response, status, item)
	}
}

func updateSubscription(service *application.SubscriptionService) http.HandlerFunc {
	type requestBody struct {
		Mode    domain.SubscriptionMode `json:"mode"`
		Filter  map[string]any          `json:"filter"`
		Version int                     `json:"version"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		var body requestBody
		if err := decodeJSON(response, request, &body); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "请求体不是有效 JSON")
			return
		}
		item, err := service.Update(request.Context(), application.UpdateSubscriptionCommand{ID: chi.URLParam(request, "subscriptionId"), Mode: body.Mode, Filter: body.Filter, ExpectedVersion: body.Version})
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, item)
	}
}

func cancelSubscription(service *application.SubscriptionService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		item, err := service.Cancel(request.Context(), chi.URLParam(request, "subscriptionId"))
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, item)
	}
}

func listDownloads(service *application.DownloadService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		page, pageSize, ok := pagination(response, request)
		if !ok {
			return
		}
		parseTime := func(key string) (*time.Time, bool) {
			raw := request.URL.Query().Get(key)
			if raw == "" {
				return nil, true
			}
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, false
			}
			value = value.UTC()
			return &value, true
		}
		var filter ports.DownloadListQuery
		filter.Status = domain.DownloadStatus(request.URL.Query().Get("status"))
		filter.TransferStatus = request.URL.Query().Get("transfer_status")
		var valid bool
		if filter.AddedFrom, valid = parseTime("added_from"); !valid {
			writeError(response, 400, "invalid_download_filter", "加入时间格式无效")
			return
		}
		if filter.AddedTo, valid = parseTime("added_to"); !valid {
			writeError(response, 400, "invalid_download_filter", "加入时间格式无效")
			return
		}
		if filter.CompletedFrom, valid = parseTime("completed_from"); !valid {
			writeError(response, 400, "invalid_download_filter", "完成时间格式无效")
			return
		}
		if filter.CompletedTo, valid = parseTime("completed_to"); !valid {
			writeError(response, 400, "invalid_download_filter", "完成时间格式无效")
			return
		}
		result, err := service.ListFiltered(request.Context(), page, pageSize, filter)
		if err != nil {
			logging.Error(logging.CategoryDownload, "下载任务查询失败")
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func listScheduledTasks(manager *scheduler.Manager) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		if manager == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "调度器尚未就绪")
			return
		}
		items := manager.Tasks()
		result := make([]domain.ScheduledTask, 0, len(items))
		for _, item := range items {
			result = append(result, domain.ScheduledTask{Name: item.Name, Cron: item.Spec, LastRun: item.LastRun, Running: item.Running})
		}
		writeJSON(response, http.StatusOK, application.Page[domain.ScheduledTask]{Page: 1, PageSize: len(result), Total: len(result), Items: result})
	}
}

func runScheduledTask(manager *scheduler.Manager) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "调度器尚未就绪")
			return
		}
		name, err := url.PathUnescape(chi.URLParam(request, "taskName"))
		if err != nil || strings.TrimSpace(name) == "" {
			writeError(response, http.StatusBadRequest, "invalid_task", "任务名称无效")
			return
		}
		if err := manager.RunNow(name); err != nil {
			if errors.Is(err, scheduler.ErrTaskNotFound) {
				writeError(response, http.StatusNotFound, "task_not_found", "定时任务不存在")
				return
			}
			writeError(response, http.StatusConflict, "task_running", "任务正在执行中")
			return
		}
		writeJSON(response, http.StatusAccepted, map[string]string{"message": "任务已开始执行"})
	}
}

func getSystemSettings(service *application.SettingsService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "设置服务尚未就绪")
			return
		}
		settings, err := service.Get(request.Context())
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, settings)
	}
}

func updateSystemSettings(service *application.SettingsService) http.HandlerFunc {
	type requestBody struct{ Values map[string]string }
	return func(response http.ResponseWriter, request *http.Request) {
		var body requestBody
		if service == nil {
			writeError(response, http.StatusServiceUnavailable, "service_unavailable", "设置服务尚未就绪")
			return
		}
		if err := decodeJSON(response, request, &body); err != nil || body.Values == nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "请求体不是有效设置")
			return
		}
		settings, err := service.Update(request.Context(), body.Values)
		if errors.Is(err, application.ErrInvalidSetting) {
			writeError(response, http.StatusBadRequest, "invalid_setting", settingErrorMessage(err))
			return
		}
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, settings)
	}
}
func pagination(response http.ResponseWriter, request *http.Request) (int, int, bool) {
	return paginationWithLimit(response, request, 20, ports.MaxPageSize)
}

func paginationWithLimit(response http.ResponseWriter, request *http.Request, defaultPageSize, maxPageSize int) (int, int, bool) {
	page, err := parsePositive(request.URL.Query().Get("page"), 1)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_pagination", "page 必须是正整数")
		return 0, 0, false
	}
	pageSize, err := parsePositive(request.URL.Query().Get("page_size"), defaultPageSize)
	if err != nil || pageSize > maxPageSize {
		writeError(response, http.StatusBadRequest, "invalid_pagination", fmt.Sprintf("page_size 必须在 1 到 %d 之间", maxPageSize))
		return 0, 0, false
	}
	return page, pageSize, true
}

// logTimeRange 解析日志筛选的 RFC3339 时间边界；仅提供一侧时执行单边筛选。
func logTimeRange(response http.ResponseWriter, request *http.Request) (*time.Time, *time.Time, bool) {
	parse := func(name string) (*time.Time, error) {
		raw := strings.TrimSpace(request.URL.Query().Get(name))
		if raw == "" {
			return nil, nil
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, err
		}
		value = value.UTC()
		return &value, nil
	}
	start, err := parse("start_time")
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_log_filter", "开始时间必须使用 RFC3339 格式")
		return nil, nil, false
	}
	end, err := parse("end_time")
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_log_filter", "结束时间必须使用 RFC3339 格式")
		return nil, nil, false
	}
	if start != nil && end != nil && start.After(*end) {
		writeError(response, http.StatusBadRequest, "invalid_log_filter", "开始时间不能晚于结束时间")
		return nil, nil, false
	}
	return start, end, true
}

func parsePositive(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, application.ErrInvalidPagination
	}
	return value, nil
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) == nil {
		return errors.New("multiple JSON values")
	}
	return nil
}

// settingErrorMessage 把配置校验失败的原因透出给调用方，避免只回一句笼统的“不支持”。
// 校验消息只包含设置项名称与取值范围，不会包含用户填写的敏感值。
func settingErrorMessage(err error) string {
	message := strings.TrimSpace(strings.TrimPrefix(err.Error(), application.ErrInvalidSetting.Error()))
	message = strings.TrimPrefix(message, ":")
	if message = strings.TrimSpace(message); message == "" {
		return "包含不支持的设置项"
	}
	return "设置项无效：" + message
}

func writeApplicationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrTagFollowPending):
		writeError(response, 500, "tag_follow_pending", "标签规则已保存，追新暂未完成，请重试或等待下次标签追新任务")
	case errors.Is(err, application.ErrActorFollowPending):
		writeError(response, 500, "actor_follow_pending", "演员规则已保存，追新暂未完成，请重试或等待下次同步热门演员任务")
	case errors.Is(err, application.ErrInvalidTagRule):
		writeError(response, 400, "invalid_tag_rule", "标签类型、订阅状态或限制日期无效，日期须为 YYYY-MM-DD")
	case errors.Is(err, ports.ErrTagNotFound):
		writeError(response, 404, "not_found", "标签不存在")
	case errors.Is(err, application.ErrInvalidVideoType):
		writeError(response, http.StatusBadRequest, "invalid_video_type", "影片类型无效")
	case errors.Is(err, application.ErrInvalidDownloadFilter):
		writeError(response, http.StatusBadRequest, "invalid_download_filter", "下载筛选条件无效")
	case errors.Is(err, application.ErrInvalidPagination), errors.Is(err, application.ErrInvalidSubscription):
		writeError(response, http.StatusBadRequest, "invalid_request", "请求参数无效")
	case errors.Is(err, application.ErrInvalidActorLimitDate):
		writeError(response, http.StatusBadRequest, "invalid_request", "限制日期必须使用 YYYY-MM-DD 格式")
	case errors.Is(err, ports.ErrMediaNotFound), errors.Is(err, ports.ErrSubscriptionNotFound), errors.Is(err, ports.ErrActorNotFound):
		writeError(response, http.StatusNotFound, "not_found", "资源不存在")
	case errors.Is(err, ports.ErrIdempotencyConflict):
		writeError(response, http.StatusConflict, "conflict", "幂等键已用于不同请求")
	case errors.Is(err, ports.ErrActiveSubscriptionExists):
		writeError(response, http.StatusConflict, "already_subscribed", "该番号已订阅，未重复添加；卡片状态可查看本地文件是否存在")
	case errors.Is(err, ports.ErrVersionConflict):
		writeError(response, http.StatusConflict, "conflict", "订阅已被其他操作更新，请刷新后重试")
	case errors.Is(err, ports.ErrSubscriptionInactive):
		writeError(response, http.StatusConflict, "conflict", "当前订阅已失效，无法编辑")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "服务内部错误")
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "message": message})
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func spaHandler(staticDir string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(response, request)
			return
		}
		cleanPath := path.Clean("/" + request.URL.Path)
		requested := filepath.Join(staticDir, filepath.FromSlash(strings.TrimPrefix(cleanPath, "/")))
		if info, err := os.Stat(requested); err == nil && !info.IsDir() {
			serveFile(response, request, requested)
			return
		}
		index := filepath.Join(staticDir, "index.html")
		if _, err := os.Stat(index); err == nil {
			serveFile(response, request, index)
			return
		}
		writeError(response, http.StatusNotFound, "static_not_found", "前端静态资源尚未部署")
	})
}

func serveFile(response http.ResponseWriter, request *http.Request, filename string) {
	content, err := os.ReadFile(filename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(response, request)
			return
		}
		writeError(response, http.StatusInternalServerError, "static_read_failed", "无法读取前端静态资源")
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(filename)); contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	response.Header().Set("Cache-Control", "no-cache")
	response.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = response.Write(content)
	}
}
