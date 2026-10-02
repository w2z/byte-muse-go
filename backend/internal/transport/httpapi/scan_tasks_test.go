package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/database"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestManagedScanDisconnectAndRestart 使用真实 SQLite 验证请求断开不取消后台任务，
// 数据库重新打开后仍有进度，重启恢复不重新执行任务。
func TestManagedScanDisconnectAndRestart(t *testing.T) {
	ctx := context.Background()
	cfg := database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "scan.db")}
	store, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	manager, err := application.NewScanTasks(ctx, database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite))
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan context.Context, 1)
	requestCtx, cancel := context.WithCancel(ctx)
	r := httptest.NewRequest(http.MethodPost, "/strm/scan", nil).WithContext(requestCtx)
	r.Header.Set("Prefer", "respond-async")
	w := httptest.NewRecorder()
	managedScan(w, r, manager, "strm", "incremental", func(worker context.Context) (int, error) { entered <- worker; <-worker.Done(); return 0, worker.Err() }, writeStrmError)
	if w.Code != 202 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var task domain.ScanTask
	if err = json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	worker := <-entered
	cancel()
	if worker.Err() != nil {
		t.Fatal("client disconnect canceled worker")
	}
	w = httptest.NewRecorder()
	scanTaskEndpoint(manager, "strm", false)(w, httptest.NewRequest("GET", "/strm/scan/task", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	manager.Close()
	store.Close()
	store, err = database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err = application.NewScanTasks(ctx, database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	got, err := manager.Latest(ctx, "strm")
	if err != nil || got.ID != task.ID || got.State != "interrupted" {
		t.Fatalf("restart %+v %v", got, err)
	}
	// 完成空任务后保留最终结果与 100%，不会将重启状态误报成功。
	_, err = manager.Start(ctx, "strm", "incremental", func(context.Context) (any, error) { return map[string]int{"files": 0}, nil })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, _ = manager.Latest(ctx, "strm")
		if got.State == "completed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got.State != "completed" || got.Progress.Percent != 100 || string(got.Result) != `{"files":0}` {
		t.Fatalf("completion %+v", got)
	}
}
