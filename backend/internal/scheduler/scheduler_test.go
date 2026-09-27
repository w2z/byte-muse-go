package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"bytemuse/backend/internal/logging"
)

type testLogStore struct {
	mu      sync.Mutex
	records []logging.Record
}

func (s *testLogStore) Append(_ context.Context, record logging.Record) error {
	s.mu.Lock()
	s.records = append(s.records, record)
	s.mu.Unlock()
	return nil
}

func (s *testLogStore) Search(context.Context, logging.Query) ([]logging.Record, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]logging.Record(nil), s.records...), len(s.records), nil
}

func (*testLogStore) Clear(context.Context, logging.Query) (int, error)    { return 0, nil }
func (*testLogStore) DeleteBefore(context.Context, time.Time) (int, error) { return 0, nil }

func (s *testLogStore) hasCompletion(deleted int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.Message != "定时任务执行完成" || record.Attrs["task"] != "测试任务" {
			continue
		}
		value, ok := record.Attrs["deleted"].(int64)
		if ok && value == deleted {
			return true
		}
	}
	return false
}

func TestRunNowIncludesJobResultInCompletionLog(t *testing.T) {
	store := &testLogStore{}
	previous := logging.Default
	logging.Default = logging.New(nil)
	logging.Default.SetStore(store)
	t.Cleanup(func() { logging.Default = previous })

	manager, err := New([]Job{{
		Name: "测试任务",
		Spec: "* * * * *",
		Run: func(context.Context) JobResult {
			return JobResult{"deleted": int64(12)}
		},
	}})
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}
	if err := manager.RunNow("测试任务"); err != nil {
		t.Fatalf("run task now: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !store.hasCompletion(12) {
		time.Sleep(10 * time.Millisecond)
	}
	if !store.hasCompletion(12) {
		t.Fatal("completion log did not include job result")
	}
}

func TestRunNowTracksLastRunAndRunningState(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	manager, err := New([]Job{{
		Name: "测试任务",
		Spec: "* * * * *",
		Run: func(context.Context) JobResult {
			started <- struct{}{}
			<-release
			return nil
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
	manager, err := New([]Job{{Name: "任务", Spec: "* * * * *", Run: func(context.Context) JobResult { return nil }}})
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}
	if !errors.Is(manager.RunNow("不存在"), ErrTaskNotFound) {
		t.Fatal("expected ErrTaskNotFound")
	}
}

func TestNewRejectsDuplicateTaskNames(t *testing.T) {
	_, err := New([]Job{
		{Name: "重复", Spec: "* * * * *", Run: func(context.Context) JobResult { return nil }},
		{Name: "重复", Spec: "* * * * *", Run: func(context.Context) JobResult { return nil }},
	})
	if err == nil {
		t.Fatal("expected duplicate task names to be rejected")
	}
}
