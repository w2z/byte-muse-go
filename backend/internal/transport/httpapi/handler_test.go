package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"bytemuse/backend/internal/transport/httpapi"
)

func TestHealthEndpointsAreNeverHandledBySPAFallback(t *testing.T) {
	handler := newHandler(t, true)

	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, response.Code, response.Body.String())
		}
		assertJSONField(t, response, "status", "ok")
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/missing", nil))
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "ByteMuse SPA") {
		t.Fatalf("unknown health response = %d %q, want JSON 404 without SPA", response.Code, response.Body.String())
	}
}

func TestReadinessReturnsServiceUnavailable(t *testing.T) {
	handler := newHandler(t, false)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", response.Code)
	}
	assertJSONField(t, response, "code", "service_unavailable")
}

func TestMediaListMatchesOpenAPIFields(t *testing.T) {
	handler := newHandler(t, true)
	cookie := loginCookie(t, handler)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/media?page=2&page_size=1", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("media list status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Page     int            `json:"page"`
		PageSize int            `json:"page_size"`
		Total    int            `json:"total"`
		Items    []domain.Media `json:"items"`
	}
	decodeJSON(t, response, &body)
	if body.Page != 2 || body.PageSize != 1 || body.Total != 2 || len(body.Items) != 1 {
		t.Fatalf("media page = %+v, want second page", body)
	}
	if body.Items[0].SubscriptionStatus != domain.SubscriptionStatusNone || body.Items[0].LibraryStatus != domain.LibraryStatusUnknown {
		t.Fatalf("media status fields = %+v", body.Items[0])
	}
}

func TestSubscriptionCreateReplayAndCancel(t *testing.T) {
	handler := newHandler(t, true)
	cookie := loginCookie(t, handler)
	body := `{"media_id":"media-1","mode":"strict","filter":{"minimum_seeders":2}}`
	first := performJSONWithCookie(handler, http.MethodPost, "/api/v1/subscriptions", body, map[string]string{"Idempotency-Key": "request-1000"}, cookie)
	if first.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", first.Code, first.Body.String())
	}
	var created domain.Subscription
	decodeJSON(t, first, &created)

	replay := performJSONWithCookie(handler, http.MethodPost, "/api/v1/subscriptions", body, map[string]string{"Idempotency-Key": "request-1000"}, cookie)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body=%s", replay.Code, replay.Body.String())
	}
	var replayed domain.Subscription
	decodeJSON(t, replay, &replayed)
	if replayed.ID != created.ID {
		t.Fatalf("replay id = %q, want %q", replayed.ID, created.ID)
	}

	cancel := performJSONWithCookie(handler, http.MethodPost, "/api/v1/subscriptions/"+created.ID+"/cancel", "", nil, cookie)
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200; body=%s", cancel.Code, cancel.Body.String())
	}
	assertJSONField(t, cancel, "status", string(domain.SubscriptionStatusCanceled))
}

func TestDownloadsAndSystemStatusMatchOpenAPIFields(t *testing.T) {
	handler := newHandler(t, true)
	cookie := loginCookie(t, handler)

	downloads := httptest.NewRecorder()
	downloadRequest := httptest.NewRequest(http.MethodGet, "/api/v1/downloads", nil)
	downloadRequest.AddCookie(cookie)
	handler.ServeHTTP(downloads, downloadRequest)
	if downloads.Code != http.StatusOK {
		t.Fatalf("downloads status = %d; body=%s", downloads.Code, downloads.Body.String())
	}
	var page map[string]any
	decodeJSON(t, downloads, &page)
	for _, field := range []string{"page", "page_size", "total", "items"} {
		if _, ok := page[field]; !ok {
			t.Fatalf("download page missing %q: %+v", field, page)
		}
	}

	status := httptest.NewRecorder()
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	statusRequest.AddCookie(cookie)
	handler.ServeHTTP(status, statusRequest)
	if status.Code != http.StatusOK {
		t.Fatalf("system status = %d; body=%s", status.Code, status.Body.String())
	}
	var system map[string]any
	decodeJSON(t, status, &system)
	for _, field := range []string{"version", "database_driver", "scheduler_running", "started_at"} {
		if _, ok := system[field]; !ok {
			t.Fatalf("system status missing %q: %+v", field, system)
		}
	}
}

func TestSPAFallbackServesIndexButNeverConsumesAPI(t *testing.T) {
	handler := newHandler(t, true)
	cookie := loginCookie(t, handler)

	spa := httptest.NewRecorder()
	handler.ServeHTTP(spa, httptest.NewRequest(http.MethodGet, "/catalog/media-1", nil))
	if spa.Code != http.StatusOK || !strings.Contains(spa.Body.String(), "ByteMuse SPA") {
		t.Fatalf("SPA fallback = %d %q", spa.Code, spa.Body.String())
	}

	api := httptest.NewRecorder()
	apiRequest := httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil)
	apiRequest.AddCookie(cookie)
	handler.ServeHTTP(api, apiRequest)
	if api.Code != http.StatusNotFound || strings.Contains(api.Body.String(), "ByteMuse SPA") {
		t.Fatalf("unknown API response = %d %q, want JSON 404", api.Code, api.Body.String())
	}
}

func newHandler(t *testing.T, ready bool) http.Handler {
	t.Helper()
	return newHandlerWithAuth(t, ready, testAuthService(t))
}

// newHandlerWithAuth 允许测试注入带自定义时钟、有效期和宽限窗口的认证服务。
func newHandlerWithAuth(t *testing.T, ready bool, authService *auth.Service) http.Handler {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>ByteMuse SPA</html>"), 0o600); err != nil {
		t.Fatalf("write SPA fixture: %v", err)
	}
	movies := []domain.Media{
		{ID: "media-1", Code: "BM-001", Title: "影片一", SubscriptionStatus: domain.SubscriptionStatusNone, LibraryStatus: domain.LibraryStatusAbsent, CreatedAt: time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC)},
		{ID: "media-2", Code: "BM-002", Title: "影片二", SubscriptionStatus: domain.SubscriptionStatusNone, LibraryStatus: domain.LibraryStatusUnknown, CreatedAt: time.Date(2026, 9, 23, 2, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 23, 2, 0, 0, 0, time.UTC)},
	}
	movieRepo := &mediaRepo{items: movies}
	subRepo := newSubscriptionRepo()
	downloadRepo := &downloadRepo{}
	system := application.NewSystemService("0.1.0", "sqlite", true, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC), func() bool { return true })
	return httpapi.New(httpapi.Dependencies{
		Actors:        application.NewActorService(&actorRepo{}),
		Auth:          authService,
		Catalog:       application.NewCatalogService(movieRepo),
		Subscriptions: application.NewSubscriptionService(subRepo),
		Downloads:     application.NewDownloadService(downloadRepo),
		Dashboard:     application.NewDashboardService(movieRepo, subRepo, downloadRepo, healthyIntegrationCounter(0)),
		System:        system,
		Readiness:     readinessStub(ready),
		StaticDir:     dir,
	})
}

type readinessStub bool

type healthyIntegrationCounter int

func (c healthyIntegrationCounter) CountHealthy(context.Context) (int, error) { return int(c), nil }

func (r readinessStub) Ready(context.Context) error {
	if !r {
		return context.Canceled
	}
	return nil
}

type mediaRepo struct{ items []domain.Media }

func (r *mediaRepo) List(_ context.Context, query ports.MediaListQuery) (domain.MediaPage, error) {
	start := min(query.Offset, len(r.items))
	end := min(start+query.Limit, len(r.items))
	return domain.MediaPage{Items: r.items[start:end], Total: len(r.items)}, nil
}
func (r *mediaRepo) Get(_ context.Context, id string) (domain.Media, error) {
	for _, item := range r.items {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.Media{}, ports.ErrMediaNotFound
}

type subscriptionRepo struct {
	items map[string]domain.Subscription
	keys  map[string]string
}

func newSubscriptionRepo() *subscriptionRepo {
	return &subscriptionRepo{items: make(map[string]domain.Subscription), keys: make(map[string]string)}
}
func (r *subscriptionRepo) Create(_ context.Context, request ports.CreateSubscription) (domain.Subscription, bool, error) {
	if id, ok := r.keys[request.IdempotencyKey]; ok {
		return r.items[id], false, nil
	}
	now := time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC)
	item := domain.Subscription{ID: "01K5Y8DB7W3YB6AJ6F8W9P8V4P", MediaID: request.MediaID, Status: domain.SubscriptionStatusActive, Mode: request.Mode, Filter: request.Filter, CreatedAt: now, UpdatedAt: now, Version: 1}
	r.items[item.ID], r.keys[request.IdempotencyKey] = item, item.ID
	return item, true, nil
}
func (r *subscriptionRepo) Cancel(_ context.Context, id string) (domain.Subscription, bool, error) {
	item, ok := r.items[id]
	if !ok {
		return domain.Subscription{}, false, ports.ErrSubscriptionNotFound
	}
	if item.Status == domain.SubscriptionStatusCanceled {
		return item, false, nil
	}
	item.Status, item.Version = domain.SubscriptionStatusCanceled, item.Version+1
	r.items[id] = item
	return item, true, nil
}
func (r *subscriptionRepo) List(_ context.Context, _ ports.SubscriptionListQuery) (domain.SubscriptionPage, error) {
	items := make([]domain.Subscription, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, item)
	}
	return domain.SubscriptionPage{Items: items, Total: len(items)}, nil
}

type downloadRepo struct{}

func (*downloadRepo) List(context.Context, ports.DownloadListQuery) (domain.DownloadPage, error) {
	return domain.DownloadPage{Items: []domain.DownloadTask{}, Total: 0}, nil
}

func performJSON(handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	return performJSONWithCookie(handler, method, path, body, headers, nil)
}

func performJSONWithCookie(handler http.Handler, method, path, body string, headers map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func assertJSONField(t *testing.T, response *httptest.ResponseRecorder, field string, want any) {
	t.Helper()
	var body map[string]any
	decodeJSON(t, response, &body)
	if body[field] != want {
		t.Fatalf("field %q = %#v, want %#v; body=%s", field, body[field], want, response.Body.String())
	}
}

type actorRepo struct{}

func (*actorRepo) ListSubscribed(context.Context, int, int) ([]domain.Actor, int, error) {
	return []domain.Actor{{Name: "演员一", LimitDate: actorDate("2026-01-01")}, {Name: "演员三", LimitDate: actorDate("2026-02-01")}}, 2, nil
}
func actorDate(v string) *string { return &v }

func TestActorListRequiresAuthAndReturnsSubscribedActors(t *testing.T) {
	handler := newHandler(t, true)
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/actors?page=1&page_size=20", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", unauthenticated.Code)
	}
	cookie := loginCookie(t, handler)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/actors?page=1&page_size=20", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	decodeJSON(t, response, &body)
	if body["page"] != float64(1) || body["page_size"] != float64(20) || body["total"] != float64(2) {
		t.Fatalf("body = %+v", body)
	}
}
