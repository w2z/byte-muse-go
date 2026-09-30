package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/platform/downloadclient"
	"bytemuse/backend/internal/ports"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDownloadControlHTTP 使用真实 SQL、鉴权路由及 qB HTTP 适配器覆盖整个控制链路。
func TestDownloadControlHTTP(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := store.SQLDB().ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", "m1", "TEST-001", "test", "active", "absent", now, now)
	exec("INSERT INTO subscriptions(id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES(?,?,?,?,?,?,?,?,?,?)", "s1", "m1", "active", "strict", "{}", "key", "hash", now, now, 1)
	hash := strings.Repeat("a", 40)
	state := "downloading"
	exists := true
	commands := 0
	deleteFiles := ""
	reject := false
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/app/version":
			w.Write([]byte("v5.2.3"))
		case "/api/v2/torrents/info":
			if !exists {
				w.Write([]byte("[]"))
			} else {
				json.NewEncoder(w).Encode([]map[string]any{{"hash": hash, "state": state, "progress": 0.2}})
			}
		case "/api/v2/torrents/start", "/api/v2/torrents/stop", "/api/v2/torrents/delete":
			commands++
			r.ParseForm()
			if r.Form.Get("hashes") != hash {
				t.Error("unexpected hash")
			}
			if reject {
				w.WriteHeader(500)
				return
			}
			switch r.URL.Path {
			case "/api/v2/torrents/stop":
				state = "stoppedDL"
			case "/api/v2/torrents/start":
				state = "downloading"
			case "/api/v2/torrents/delete":
				exists = false
				deleteFiles = r.Form.Get("deleteFiles")
			}
		default:
			t.Errorf("unexpected qB request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer qb.Close()
	repo := database.NewSubscriptionDownloadRepository(store.SQLDB(), database.DialectSQLite)
	service := application.NewDownloadService(store.Downloads())
	service.SetControls(repo, func(context.Context) (map[string]ports.DownloadController, error) {
		return map[string]ports.DownloadController{"qbittorrent": downloadclient.NewQbittorrent(qb.URL, "test", "pass", "", "", qb.Client())}, nil
	})
	authService, err := auth.New(auth.Config{Username: "test", Password: "test-password", Secret: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{Auth: authService, Downloads: service})
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"test","password":"test-password"}`)))
	if login.Code != 200 {
		t.Fatalf("login=%d", login.Code)
	}
	request := func(method, path string, authorized bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, nil)
		if authorized {
			r.AddCookie(login.Result().Cookies()[0])
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	assertStatus := func(method, path string, want int) {
		t.Helper()
		w := request(method, path, true)
		if w.Code != want {
			t.Fatalf("%s %s = %d %s want %d", method, path, w.Code, w.Body.String(), want)
		}
	}
	insert := func(status, transfer string) {
		exec("INSERT INTO download_tasks(id,media_id,subscription_id,status,transfer_status,downloader,info_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", "d1", "m1", "s1", status, transfer, "qbittorrent", hash, now, now)
	}
	insert("submitted", "downloading")
	if w := request("POST", "/downloads/d1/stop", false); w.Code != 401 {
		t.Fatal("missing auth guard")
	}
	w := request("GET", "/downloads", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"TEST-001"`) || !strings.Contains(w.Body.String(), `"stop"`) || strings.Contains(w.Body.String(), `"pause"`) {
		t.Fatalf("list=%s", w.Body.String())
	}
	assertStatus("POST", "/downloads/d1/stop", 204)
	assertStatus("GET", "/downloads?transfer_status=stopped", 200)
	w = request("GET", "/downloads?transfer_status=stopped", true)
	if !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("filter=%s", w.Body.String())
	}
	stale := time.Now().Add(-time.Hour)
	if _, err = repo.SaveTransferStates(ctx, []ports.TransferState{{Hash: hash, Status: "downloading", ObservedAt: stale}}); err != nil {
		t.Fatal(err)
	}
	var transfer string
	store.SQLDB().QueryRow("SELECT transfer_status FROM download_tasks WHERE id='d1'").Scan(&transfer)
	if transfer != "stopped" {
		t.Fatal("stale sync overwrote control")
	}
	assertStatus("POST", "/downloads/d1/resume", 204)
	before := commands
	assertStatus("POST", "/downloads/d1/pause", 409)
	if commands != before {
		t.Fatal("unsupported action sent")
	}
	reject = true
	assertStatus("POST", "/downloads/d1/stop", 502)
	reject = false
	store.SQLDB().QueryRow("SELECT transfer_status FROM download_tasks WHERE id='d1'").Scan(&transfer)
	if transfer != "downloading" {
		t.Fatal("failed control changed state")
	}
	_, token, err := repo.LockControl(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	assertStatus("POST", "/downloads/d1/stop", 409)
	repo.ReleaseControl(ctx, "d1", token)
	state = "error"
	exec("UPDATE download_tasks SET transfer_status='failed' WHERE id='d1'")
	assertStatus("DELETE", "/downloads/d1?delete_files=true", 409)
	assertStatus("POST", "/downloads/d1/retry", 204)
	assertStatus("DELETE", "/downloads/d1?delete_files=false", 204)
	if deleteFiles != "false" {
		t.Fatal("deleted files unexpectedly")
	}
	assertStatus("POST", "/downloads/d1/resume", 404)
	exists = true
	state = "downloading"
	insert("submitted", "downloading")
	assertStatus("DELETE", "/downloads/d1?delete_files=true", 204)
	if deleteFiles != "true" {
		t.Fatal("file delete not requested")
	}
	exec("INSERT INTO download_tasks(id,media_id,subscription_id,status,created_at,updated_at,error_message) VALUES(?,?,?,?,?,?,?)", "d1", "m1", "s1", "failed", now, now, "search failed")
	assertStatus("DELETE", "/downloads/d1?delete_files=true", 409)
	before = commands
	assertStatus("POST", "/downloads/d1/retry", 204)
	if commands != before {
		t.Fatal("search retry called client")
	}
	var tasks int
	store.SQLDB().QueryRow("SELECT COUNT(*) FROM download_tasks WHERE id='d1'").Scan(&tasks)
	if tasks != 0 {
		t.Fatalf("搜索重试应删除失败任务，剩余任务行=%d", tasks)
	}
	var scanOrigin, scanStatus string
	if err := store.SQLDB().QueryRow("SELECT origin,status FROM subscription_scans WHERE subscription_id='s1'").Scan(&scanOrigin, &scanStatus); err != nil {
		t.Fatalf("搜索重试应登记一次资源搜索: %v", err)
	}
	if scanOrigin != "user" || scanStatus != "queued" {
		t.Fatalf("搜索队列项 = %s/%s", scanOrigin, scanStatus)
	}
	assertStatus("POST", "/downloads/d1/retry", 404)
	exec("INSERT INTO download_tasks(id,media_id,subscription_id,status,created_at,updated_at,error_message) VALUES(?,?,?,?,?,?,?)", "d2", "m1", "s1", "failed", now, now, "search failed")
	exec("UPDATE subscriptions SET status='canceled' WHERE id='s1'")
	assertStatus("POST", "/downloads/d2/retry", 409)
	assertStatus("DELETE", "/downloads/d2", 204)
	var count int
	store.SQLDB().QueryRow("SELECT COUNT(*) FROM media").Scan(&count)
	if count != 1 {
		t.Fatal("media changed")
	}
}
