package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/database"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUploadRoutesRequireSession prevents anonymous directory and upload-task disclosure.
func TestUploadRoutesRequireSession(t *testing.T) {
	handler := New(Dependencies{})
	for _, route := range []string{"directories", "status", "directories/progress", "files"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/cloud-upload/"+route, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s: %d", route, response.Code)
		}
	}
}

// TestUploadQueueRestartAndControls uses the real migrated repository and worker without provider writes.
func TestUploadQueueRestartAndControls(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(root, "state.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "file.txt"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	mappings, _ := json.Marshal([]application.UploadMapping{{Kind: "115", ID: "0", Path: "/", LocalPath: source}})
	settings := func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_ENABLE": "true", "CLOUD_UPLOAD_PATHS": string(mappings)}, nil
	}
	repo := database.NewUploadRepository(db.SQLDB(), database.DialectSQLite)
	start := func() (*application.UploadService, func()) {
		s := application.NewUploadService(settings, repo, nil, nil)
		run, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); s.Run(run) }()
		return s, func() { cancel(); <-done }
	}
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("queue timeout")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	s, stop := start()
	wait(func() bool { return s.Files(1, 20).Total == 1 })
	request := func(action string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		uploadControl(s)(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"`+action+`"}`)))
		if w.Code != want {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
	request("pause", 200)
	request("pause", 409)
	stop()
	s, stop = start()
	defer stop()
	wait(func() bool { return s.Status().Actions["resume"] })
	if s.Files(1, 20).Items[0].State != "paused" {
		t.Fatal("pause not restored")
	}
	request("resume", 200)
	request("stop", 200)
	request("delete_all", 200)
	wait(func() bool { return s.Files(1, 20).Total == 0 })
	time.Sleep(1100 * time.Millisecond)
	if s.Files(1, 20).Total != 0 {
		t.Fatal("deleted tasks rediscovered")
	}
	records, err := repo.List(ctx)
	if err != nil || len(records) != 2 {
		t.Fatalf("dedupe receipts: %v %v", records, err)
	}
}

// TestUploadPagingRejectsInvalidBounds keeps polling responses bounded for every caller.
func TestUploadPagingRejectsInvalidBounds(t *testing.T) {
	handler := uploadFilesProgress(application.NewUploadService(nil, nil, nil, nil))
	for _, query := range []string{"page=0", "page=1000001", "page=bad", "page_size=101", "page_size=-1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", query, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?page=1&page_size=15", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("empty page: %d", response.Code)
	}
}
