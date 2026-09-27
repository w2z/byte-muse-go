package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunNowTracksLastRunAndRunningState(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	manager, err := New([]Job{{
		Name: "测试任务",
		Spec: "* * * * *",
		Run: func(context.Context) {
			started <- struct{}{}
			<-release
		},
	}})
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}

	if err := manager.RunNow("测试任务"); err != nil {
		t.Fatalf("run task now: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	items := manager.Tasks()
	if len(items) != 1 || !items[0].Running || items[0].LastRun == nil {
		t.Fatalf("unexpected running state: %#v", items)
	}
	if err := manager.RunNow("测试任务"); err == nil {
		t.Fatal("expected duplicate run to be rejected")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !manager.Tasks()[0].Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not finish")
}

func TestRunNowReportsUnknownTask(t *testing.T) {
	manager, err := New([]Job{{Name: "任务", Spec: "* * * * *", Run: func(context.Context) {}}})
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}
	if !errors.Is(manager.RunNow("不存在"), ErrTaskNotFound) {
		t.Fatal("expected ErrTaskNotFound")
	}
}

func TestNewRejectsDuplicateTaskNames(t *testing.T) {
	_, err := New([]Job{
		{Name: "重复", Spec: "* * * * *", Run: func(context.Context) {}},
		{Name: "重复", Spec: "* * * * *", Run: func(context.Context) {}},
	})
	if err == nil {
		t.Fatal("expected duplicate task names to be rejected")
	}
}
