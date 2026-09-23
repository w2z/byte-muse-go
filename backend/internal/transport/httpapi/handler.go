// Package httpapi exposes application services through the public REST contract.
package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// Dependencies contains the services required by the HTTP transport.
type Dependencies struct {
	Auth          *auth.Service
	Catalog       *application.CatalogService
	Actors        *application.ActorService
	Subscriptions *application.SubscriptionService
	Downloads     *application.DownloadService
	Dashboard     *application.DashboardService
	System        *application.SystemService
	Readiness     ports.ReadinessProbe
	StaticDir     string
}

// publicAuthPaths 是不要求既有会话即可访问的认证入口：登录本身，以及用过期会话换取新会话的续签。
var publicAuthPaths = map[string]struct{}{
	"/api/v1/auth/login":   {},
	"/api/v1/auth/refresh": {},
}

// New builds the API router and SPA static-file fallback.
func New(dependencies Dependencies) http.Handler {
	api := chi.NewRouter()
	api.Get("/health/live", live)
	api.Get("/health/ready", ready(dependencies.Readiness))
	api.Route("/api/v1", func(router chi.Router) {
		router.Post("/auth/login", login(dependencies.Auth))
		router.Post("/auth/refresh", refresh(dependencies.Auth))
		router.Get("/dashboard", dashboard(dependencies.Dashboard))
		router.Get("/media", listMedia(dependencies.Catalog))
		router.Get("/actors", listActors(dependencies.Actors))
		router.Get("/ranks", listMedia(dependencies.Catalog))
		router.Get("/tags", emptyCollection)
		router.Get("/brands", emptyCollection)
		router.Get("/codes/release_today", listMedia(dependencies.Catalog))
		router.Get("/codes/recommend", listMedia(dependencies.Catalog))
		router.Get("/complex/search", listMedia(dependencies.Catalog))
		router.Get("/tasks", listDownloads(dependencies.Downloads))
		router.Get("/logs", emptyCollection)
		router.Get("/notice", emptyCollection)
		router.Get("/profile", profile(dependencies.Auth))
		router.Get("/media/{mediaId}", getMedia(dependencies.Catalog))
		router.Get("/subscriptions", listSubscriptions(dependencies.Subscriptions))
		router.Post("/subscriptions", createSubscription(dependencies.Subscriptions))
		router.Post("/subscriptions/{subscriptionId}/cancel", cancelSubscription(dependencies.Subscriptions))
		router.Get("/downloads", listDownloads(dependencies.Downloads))
		router.Get("/system/status", systemStatus(dependencies.System))
		router.Get("/system/settings", systemSettings(dependencies.System))
		router.Get("/events", events)
	})

	spa := spaHandler(dependencies.StaticDir)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
			writeError(response, http.StatusUnauthorized, "unauthorized", "用户名或密码错误")
			return
		}
		session, err := service.Login(body.Username, body.Password, body.Remember)
		if err != nil {
			writeError(response, http.StatusUnauthorized, "unauthorized", "用户名或密码错误")
			return
		}
		writeSession(response, service, session)
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
		result, err := service.List(request.Context(), page, pageSize)
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
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
		result, err := service.List(request.Context(), page, pageSize)
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
		result, err := service.List(request.Context(), page, pageSize, domain.DownloadStatus(request.URL.Query().Get("status")))
		if err != nil {
			writeApplicationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func systemStatus(service *application.SystemService) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, service.Status())
	}
}

func systemSettings(service *application.SystemService) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, service.Settings())
	}
}

func events(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusInternalServerError, "stream_unsupported", "事件流不可用")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("event: ready\ndata: {}\n\n"))
	flusher.Flush()
	<-request.Context().Done()
}

func pagination(response http.ResponseWriter, request *http.Request) (int, int, bool) {
	page, err := parsePositive(request.URL.Query().Get("page"), 1)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_pagination", "page 必须是正整数")
		return 0, 0, false
	}
	pageSize, err := parsePositive(request.URL.Query().Get("page_size"), 20)
	if err != nil || pageSize > 100 {
		writeError(response, http.StatusBadRequest, "invalid_pagination", "page_size 必须在 1 到 100 之间")
		return 0, 0, false
	}
	return page, pageSize, true
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

func writeApplicationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidPagination), errors.Is(err, application.ErrInvalidSubscription):
		writeError(response, http.StatusBadRequest, "invalid_request", "请求参数无效")
	case errors.Is(err, ports.ErrMediaNotFound), errors.Is(err, ports.ErrSubscriptionNotFound):
		writeError(response, http.StatusNotFound, "not_found", "资源不存在")
	case errors.Is(err, ports.ErrIdempotencyConflict):
		writeError(response, http.StatusConflict, "conflict", "幂等键已用于不同请求")
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

func emptyCollection(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
}
func profile(service *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.CookieName())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "未登录或会话失效")
			return
		}
		session, err := service.Validate(cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "未登录或会话失效")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": session})
	}
}
