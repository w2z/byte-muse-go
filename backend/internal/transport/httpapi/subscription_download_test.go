package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/platform/database"
)

// TestSubscriptionDownloadHTTP verifies authentication, durable enqueue, and duplicate response semantics.
func TestSubscriptionDownloadHTTP(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "api.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "TEST-001", "film", "active", "absent", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO subscriptions (id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?)", "sub1", "m1", "active", "strict", "{}", "key", "hash", now, now, 1); err != nil {
		t.Fatal(err)
	}
	authService, err := auth.New(auth.Config{Username: "test-admin", Password: "test-only-password", Secret: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewSubscriptionDownloadService(database.NewSubscriptionDownloadRepository(store.SQLDB(), database.DialectSQLite), nil, nil, nil)
	handler := New(Dependencies{Auth: authService, SubscriptionDownloads: service})
	path := "/api/v1/subscriptions/sub1/download"
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"test-admin","password":"test-only-password"}`)))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
		t.Fatalf("login status=%d", login.Code)
	}
	var firstID string
	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.AddCookie(login.Result().Cookies()[0])
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("enqueue status=%d body=%s", response.Code, response.Body.String())
		}
		var body struct {
			TaskID string `json:"task_id"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.TaskID == "" {
			t.Fatalf("body=%s err=%v", response.Body.String(), err)
		}
		if i == 0 {
			firstID = body.TaskID
		} else if body.TaskID != firstID {
			t.Fatalf("duplicate task %s != %s", body.TaskID, firstID)
		}
	}
}
