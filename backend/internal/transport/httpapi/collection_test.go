package httpapi

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
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/ports"
)

// TestLiveCollectionPersistence 核验真实 Netflav 数据可由现有仓储事务保存，仅使用临时数据库。
func TestLiveCollectionPersistence(t *testing.T) {
	if os.Getenv("BYTEMUSE_LIVE_COLLECTION") != "1" {
		t.Skip("需要显式启用真实站点核验")
	}
	ctx := context.Background()
	s, e := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "live.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	service := application.NewQueuedCollectionService(collector.NewRegistry(collector.NewClient(nil)), database.NewCollectionRepository(s.SQLDB(), database.DialectSQLite), nil)
	result, e := service.Enqueue(ctx, ports.CollectionRequest{Source: "netflav", Kind: "search", Query: "TEST", Page: 1})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = service.ProcessOne(ctx, "page", time.Now()); e != nil {
		t.Fatal(e)
	}
	for {
		ok, e := service.ProcessOne(ctx, "video", time.Now())
		if e != nil {
			t.Fatal(e)
		}
		if !ok {
			break
		}
	}
	result, e = service.RunStatus(ctx, result.ID)
	if e != nil || result.Status != "completed" {
		t.Fatalf("%+v %v", result, e)
	}
	if result.Counts.Fetched == 0 || result.Counts.Inserted != result.Counts.Fetched {
		t.Fatalf("%+v", result.Counts)
	}
	var n int
	if e = s.SQLDB().QueryRow(`SELECT count(*) FROM media`).Scan(&n); e != nil || n != result.Counts.MediaInserted {
		t.Fatalf("%d %v", n, e)
	}
	t.Logf("真实搜索事务统计: %+v", result.Counts)
}

type integrationPage struct{ body string }

func (p *integrationPage) Get(context.Context, string) ([]byte, error) { return []byte(p.body), nil }

// TestCollectionEndToEnd 仅替换外站响应，登录、路由、解析、迁移、事务及目录查询均为真实实现。
func TestCollectionEndToEnd(t *testing.T) {
	ctx := context.Background()
	store, e := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "api.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if e = store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	a, e := auth.New(auth.Config{Username: "test-admin", Password: "test-only-password", Secret: strings.Repeat("x", 32)})
	if e != nil {
		t.Fatal(e)
	}
	page := &integrationPage{body: `<script id="__NEXT_DATA__">{"page":"/search","props":{"initialState":{"search":{"docs":[{"videoId":"example","code":"TEST-001","title":"集成测试影片","actors":["jp:测试演员"]}],"page":1,"pages":1}}}}</script>`}
	service := application.NewQueuedCollectionService(collector.NewRegistry(page), database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite), nil)
	handler := New(Dependencies{Auth: a, Collection: service, Catalog: application.NewCatalogService(store.Media()), Actors: application.NewActorService(database.NewActorRepository(store.SQLDB(), database.DialectSQLite))})
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"test-admin","password":"test-only-password"}`)))
	if login.Code != 200 {
		t.Fatalf("login: %d", login.Code)
	}
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing session")
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(cookies[0])
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 2; i++ {
		if i == 0 {
			w := call("GET", "/api/v1/collection/sources", "")
			var sources []ports.CollectionSource
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sources) != nil || len(sources) != 2 {
				t.Fatalf("unexpected enabled sources: %d %s", w.Code, w.Body.String())
			}
			for _, source := range []string{"javlibrary", "avbase", "javbus", "jable", "supjav", "avgle", "thisav"} {
				w = call("POST", "/api/v1/collection/runs", `{"source":"`+source+`","kind":"search","query":"TEST"}`)
				if w.Code != 400 {
					t.Fatalf("excluded source %s accepted: %d %s", source, w.Code, w.Body.String())
				}
			}
			var runs int
			if err := store.SQLDB().QueryRow(`SELECT COUNT(*) FROM collection_runs`).Scan(&runs); err != nil || runs != 0 {
				t.Fatalf("excluded sources created runs: %d %v", runs, err)
			}
		}
		w := call("POST", "/api/v1/collection/runs", `{"source":"netflav","kind":"search","query":"TEST-001"}`)
		if w.Code != http.StatusAccepted {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var result ports.CollectionRun
		if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		if result.Status != "queued" || result.Saved != 0 {
			t.Fatalf("submission claimed completed: %+v", result)
		}
		for _, kind := range []string{"page", "video"} {
			if ok, e := service.ProcessOne(ctx, kind, time.Now()); e != nil || !ok {
				t.Fatalf("%s: %v %v", kind, ok, e)
			}
		}
		status := call("GET", w.Header().Get("Location"), "")
		if status.Code != 200 {
			t.Fatalf("status %d", status.Code)
		}
		if e = json.Unmarshal(status.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		if result.Counts.Inserted != 1-i || result.Counts.Existing != i || result.Request.Page != 1 || result.Status != "completed" {
			t.Fatalf("%+v", result)
		}
	}
	for _, entry := range []struct{ path, expected string }{{"/api/v1/media?search=TEST-001", "TEST-001"}, {"/api/v1/actors", "测试演员"}} {
		w := call("GET", entry.path, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), entry.expected) {
			t.Fatalf("%s: %d %s", entry.path, w.Code, w.Body.String())
		}
	}
	page.body = "unexpected HTML"
	w := call("POST", "/api/v1/collection/runs", `{"source":"netflav","kind":"search","query":"TEST-001"}`)
	if w.Code != 202 {
		t.Fatalf("bad upstream: %d", w.Code)
	}
	var failed ports.CollectionRun
	if e = json.Unmarshal(w.Body.Bytes(), &failed); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if _, e = service.ProcessOne(ctx, "page", time.Now().Add(time.Minute)); e != nil {
			t.Fatal(e)
		}
	}
	failed, e = service.RunStatus(ctx, failed.ID)
	if e != nil || failed.Status != "failed" || failed.Error != "source_format_changed" {
		t.Fatalf("%+v %v", failed, e)
	}
	var n int
	if e = store.SQLDB().QueryRow(`SELECT count(*) FROM collection_records`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("%d %v", n, e)
	}
}

type blockedCollection struct{ err error }

func (blockedCollection) Sources() []ports.CollectionSource { return nil }
func (b blockedCollection) Collect(context.Context, ports.CollectionRequest) (ports.CollectionBatch, error) {
	if b.err != nil {
		return ports.CollectionBatch{}, b.err
	}
	return ports.CollectionBatch{}, collector.ErrBlocked
}

func TestCollectionRoutesRequireAuthentication(t *testing.T) {
	handler := New(Dependencies{})
	for _, entry := range []struct{ method, path string }{{"GET", "/api/v1/collection/sources"}, {"POST", "/api/v1/collection/runs"}, {"GET", "/api/v1/collection/runs/missing"}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(entry.method, entry.path, nil))
		if response.Code != 401 {
			t.Fatalf("%s: %d", entry.path, response.Code)
		}
	}
}

func TestCollectionRejectsUnknownFieldsAndMultipleBodies(t *testing.T) {
	service := application.NewQueuedCollectionService(blockedCollection{}, nil, nil)
	for _, body := range []string{`{"url":"http://127.0.0.1"}`, `{} {}`, `{`} {
		response := httptest.NewRecorder()
		runCollection(service)(response, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if response.Code != 400 {
			t.Fatalf("%s: %d", body, response.Code)
		}
	}
}

func TestCollectionBlockedQueueIsNotEmptySuccess(t *testing.T) {
	for _, expected := range []struct {
		err  error
		code string
	}{
		{collector.ErrBlocked, "source_blocked"},
		{collector.ErrCookieRequired, "source_cookie_required"},
		{collector.ErrInteractiveVerification, "source_interactive_verification"},
		{collector.ErrBypassUnavailable, "bypass_unavailable"},
		{collector.ErrBypassTimeout, "bypass_timeout"},
		{collector.ErrBypassCaptcha, "bypass_captcha_required"},
		{collector.ErrBypassProxy, "bypass_proxy_failed"},
		{collector.ErrBypassBrowser, "bypass_browser_failed"},
		{collector.ErrBypassSession, "bypass_session_failed"},
		{collector.ErrBypassTarget, "bypass_target_unavailable"},
		{collector.ErrBypassConfig, "bypass_config_invalid"},
		{collector.ErrUnavailable, "source_unavailable"},
	} {
		t.Run(expected.code, func(t *testing.T) {
			ctx := context.Background()
			s, e := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "blocked.db")})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			if e = s.Migrate(ctx); e != nil {
				t.Fatal(e)
			}
			service := application.NewQueuedCollectionService(blockedCollection{err: expected.err}, database.NewCollectionRepository(s.SQLDB(), database.DialectSQLite), nil)
			run, e := service.Enqueue(ctx, ports.CollectionRequest{Source: "javdb", Kind: "rank", Period: "daily"})
			if e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 3; i++ {
				if _, e = service.ProcessOne(ctx, "page", time.Now().Add(time.Minute)); e != nil {
					t.Fatal(e)
				}
			}
			result, e := service.RunStatus(ctx, run.ID)
			if e != nil || result.Status != "failed" || result.Error != expected.code {
				t.Fatalf("%+v %v", result, e)
			}
		})
	}
}
