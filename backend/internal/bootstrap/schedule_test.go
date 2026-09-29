package bootstrap

import (
	"context"
	"testing"

	"bytemuse/backend/internal/scheduler"
)

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
	if len(specs) != 5 || specs["同步热门演员"] != "0 21 * * *" || specs["同步演员目录"] != actorCatalogSpec {
		t.Fatalf("演员目录应独立注册，追新与热门榜共用任务，得到 %d: %#v", len(specs), specs)
	}
}

// TestScheduleReconcileKeepsLogCleanup 端到端核对：按设置重排后，可配置任务取设置值，
// 固定日志清理任务保持 0 0 * * *，未配置的任务不排期。
func TestScheduleReconcileKeepsLogCleanup(t *testing.T) {
	jobs := configuredJobs()
	for _, job := range jobs {
		if job.Spec != "" {
			t.Fatalf("可配置任务不应带静态表达式: %s=%q", job.Name, job.Spec)
		}
	}
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
	if got["订阅下载"] != "" {
		t.Fatalf("未配置的订阅下载不应排期: %q", got["订阅下载"])
	}
}
