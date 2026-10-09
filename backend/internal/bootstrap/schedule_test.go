package bootstrap

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/scheduler"
)

// TestLogCleanupClearsAllAges 验证任务不保留近期日志，空库可重跑且清理后仍可写入。
func TestLogCleanupClearsAllAges(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, DSN: filepath.Join(t.TempDir(), "logs.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := database.NewLogRepository(store.SQLDB(), database.DialectSQLite)
	logger := logging.New(&bytes.Buffer{})
	logger.SetStore(repo)
	for _, age := range []time.Duration{0, time.Hour, 31 * 24 * time.Hour} {
		if err := repo.Append(ctx, logging.Record{Time: time.Now().UTC().Add(-age), Level: logging.LevelInfo, Category: logging.CategorySystem, Message: "cleanup fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	run := logCleanupJob(logger)
	if got := run(ctx); got["deleted"] != 3 {
		t.Fatalf("must clear all ages: %#v", got)
	}
	if _, total, err := logger.Search(ctx, logging.Query{}); err != nil || total != 0 {
		t.Fatalf("remaining=%d err=%v", total, err)
	}
	if got := run(ctx); got["deleted"] != 0 {
		t.Fatalf("empty rerun: %#v", got)
	}
	logger.Info(logging.CategorySystem, "after cleanup")
	if _, total, err := logger.Search(ctx, logging.Query{}); err != nil || total != 1 {
		t.Fatalf("new logs=%d err=%v", total, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if got := run(ctx); got["success"] != false {
		t.Fatalf("failed delete must not succeed: %#v", got)
	}
}

// TestScheduleSpecsAlwaysIncludesLogCleanup 防止保存设置后的重排把固定的日志清理任务误停：
// Apply 以“未出现在期望集合中即取消排期”为准，所以期望集合必须始终包含日志清理。
func TestScheduleSpecsAlwaysIncludesLogCleanup(t *testing.T) {
	specs := scheduleSpecs(map[string]string{
		"RANK_SCHEDULE_TIME":     "0 20 * * *",
		"ACTOR_SCHEDULE_TIME":    "0 21 * * *",
		"TAG_SCHEDULE_TIME":      "",
		"DOWNLOAD_SCHEDULE_TIME": " 0 22 * * * ",
	})
	if got := specs[logCleanupTaskName]; got != logCleanupSpec {
		t.Fatalf("日志清理任务必须始终排期，得到 %q", got)
	}
	if _, ok := specs["标签追新"]; ok {
		t.Fatalf("空设置不应排期: %#v", specs)
	}
	if specs["订阅下载"] != "0 22 * * *" {
		t.Fatalf("设置值应去除首尾空白: %q", specs["订阅下载"])
	}
	if len(specs) != 6 || specs["同步热门演员"] != "0 21 * * *" || specs["同步上新"] != releaseTodaySpec || specs["同步演员目录"] != actorCatalogSpec {
		t.Fatalf("上新、演员目录应独立注册，得到 %d: %#v", len(specs), specs)
	}
}

// TestScheduleReconcileKeepsFixedJobs 端到端核对：按设置重排后，可配置任务取设置值，
// 固定上新和日志清理任务保持排期，未配置的任务不排期。
func TestScheduleReconcileKeepsFixedJobs(t *testing.T) {
	jobs := configuredJobs()
	for _, job := range jobs {
		if job.Spec != "" {
			t.Fatalf("可配置任务不应带静态表达式: %s=%q", job.Name, job.Spec)
		}
	}
	jobs = append(jobs, scheduler.Job{Name: "同步上新", Spec: releaseTodaySpec, Run: func(context.Context) scheduler.JobResult { return nil }})
	jobs = append(jobs, scheduler.Job{Name: logCleanupTaskName, Spec: logCleanupSpec, Run: func(context.Context) scheduler.JobResult { return nil }})
	jobs = append(jobs, scheduler.Job{Name: "同步演员目录", Run: func(context.Context) scheduler.JobResult { return nil }})
	manager, err := scheduler.New(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(scheduleSpecs(map[string]string{"RANK_SCHEDULE_TIME": "*/5 * * * *"})); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, task := range manager.Tasks() {
		got[task.Name] = task.Spec
	}
	if got["同步榜单"] != "*/5 * * * *" {
		t.Fatalf("同步榜单 = %q", got["同步榜单"])
	}
	if got[logCleanupTaskName] != logCleanupSpec {
		t.Fatalf("日志清理被重排后丢失: %q", got[logCleanupTaskName])
	}
	if got["同步上新"] != releaseTodaySpec {
		t.Fatalf("同步上新被重排后丢失: %q", got["同步上新"])
	}
	if got["订阅下载"] != "" {
		t.Fatalf("未配置的订阅下载不应排期: %q", got["订阅下载"])
	}
}
