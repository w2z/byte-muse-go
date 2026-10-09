package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/scheduler"
	"github.com/go-chi/chi/v5"
)

// TestTaskScheduleRequiresSession 保证编辑入口与任务执行入口使用同一登录保护。
func TestTaskScheduleRequiresSession(t *testing.T) {
	response := httptest.NewRecorder()
	New(Dependencies{}).ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/v1/tasks/test/schedule", strings.NewReader(`{"cron":""}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

// TestTaskSchedulePersistence 验证七个任务共用真实设置仓储，非法输入不改计划，重建服务后恢复。
func TestTaskSchedulePersistence(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, DSN: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := database.NewSettingsRepository(store.SQLDB(), database.DialectSQLite)
	newRuntime := func() (*application.SettingsService, *scheduler.Manager) {
		t.Helper()
		svc, err := application.NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
		if err != nil {
			t.Fatal(err)
		}
		var jobs []scheduler.Job
		for _, def := range application.ScheduleDefinitions() {
			jobs = append(jobs, scheduler.Job{Name: def.Name, Run: func(context.Context) scheduler.JobResult { return nil }})
		}
		manager, err := scheduler.New(jobs)
		if err != nil {
			t.Fatal(err)
		}
		values, err := svc.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Apply(application.ScheduleSpecs(values.Values)); err != nil {
			t.Fatal(err)
		}
		svc.SetScheduleApplier(func(values map[string]string) error { return manager.Apply(application.ScheduleSpecs(values)) })
		return svc, manager
	}
	svc, manager := newRuntime()
	router := chi.NewRouter()
	router.Put("/tasks/{taskName}/schedule", updateScheduledTask(manager, svc))
	request := func(name, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/tasks/"+url.PathEscape(name)+"/schedule", strings.NewReader(body)))
		if response.Code != status {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		return response
	}
	for _, def := range application.ScheduleDefinitions() {
		result := request(def.Name, `{"cron":" 15 6 * * * "}`, 200)
		var task domain.ScheduledTask
		if err := json.Unmarshal(result.Body.Bytes(), &task); err != nil || task.Cron != "15 6 * * *" || task.Name != def.Name {
			t.Fatalf("task=%+v err=%v", task, err)
		}
		for _, body := range []string{`{"cron":"99 99 * * *"}`, `{}`, `{"cron":null}`, `{"cron":42}`} {
			request(def.Name, body, 400)
		}
	}
	request("不存在", `{"cron":"0 0 * * *"}`, 404)
	_, restarted := newRuntime()
	for _, task := range restarted.Tasks() {
		if task.Spec != "15 6 * * *" {
			t.Fatalf("restart task=%+v", task)
		}
	}
	request("清理系统日志", `{"cron":""}`, 200)
	_, restarted = newRuntime()
	for _, task := range restarted.Tasks() {
		want := "15 6 * * *"
		if task.Name == "清理系统日志" {
			want = ""
		}
		if task.Spec != want {
			t.Fatalf("pause task=%+v", task)
		}
	}
}
