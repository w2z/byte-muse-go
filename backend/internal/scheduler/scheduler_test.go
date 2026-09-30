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

func TestStartRunsEachNonLogTaskOnce(t *testing.T) {
	var mu sync.Mutex
	runs := map[string]int{}
	manager, err := New([]Job{
		{Name: "采集任务", Run: func(context.Context) JobResult { mu.Lock(); runs["采集任务"]++; mu.Unlock(); return nil }},
		{Name: "清理系统日志", Run: func(context.Context) JobResult { mu.Lock(); runs["清理系统日志"]++; mu.Unlock(); return nil }},
		{Name: "演员任务", Run: func(context.Context) JobResult { mu.Lock(); runs["演员任务"]++; mu.Unlock(); return nil }},
	})
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}
	if err := manager.Start("清理系统日志"); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	if err := manager.Start("清理系统日志"); err != nil {
		t.Fatalf("start scheduler twice: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Stop(ctx); err != nil {
			t.Errorf("stop scheduler: %v", err)
		}
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		completed := runs["采集任务"] == 1 && runs["演员任务"] == 1
		mu.Unlock()
		if completed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if runs["采集任务"] != 1 || runs["演员任务"] != 1 || runs["清理系统日志"] != 0 {
		t.Fatalf("unexpected startup runs: %#v", runs)
	}
}

func TestStartDoesNotOverlapTaskAlreadyRunning(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	runs := 0
	manager, err := New([]Job{{
		Name: "任务",
		Run: func(context.Context) JobResult {
			mu.Lock()
			runs++
			mu.Unlock()
			started <- struct{}{}
			<-release
			return nil
		},
	}})
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}
	if err := manager.RunNow("任务"); err != nil {
		t.Fatalf("run task now: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	if err := manager.Start(); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Stop(ctx); err != nil {
			t.Errorf("stop scheduler: %v", err)
		}
	})
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Fatalf("startup run overlapped active task: %d runs", runs)
	}
}

func TestStopWaitsForStartupTasks(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseTask := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseTask()
	manager, err := New([]Job{{Name: "任务", Run: func(ctx context.Context) JobResult {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- manager.Start() }()
	select {
	case err := <-startResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("startup blocked on a task")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup task did not run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := manager.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop must wait for startup task, got %v", err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("stop did not cancel startup context")
	}
	waiting := make(chan error, 1)
	go func() { waiting <- manager.Stop(context.Background()) }()
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer retryCancel()
	retry := make(chan error, 1)
	go func() { retry <- manager.Stop(retryCtx) }()
	select {
	case err := <-retry:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stop retry returned before task finished: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent stop ignored its deadline")
	}
	releaseTask()
	select {
	case err := <-waiting:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not finish after startup task returned")
	}
	if err := manager.Start(); err == nil {
		t.Fatal("stopped manager must not dispatch tasks with a cancelled context")
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

// TestApplyReconcilesSpecs 验证保存设置后无需重启即可重排 cron：补排、改表达式、清空停用，
// 未知任务与非法表达式整批拒绝且不改变当前排期。
func TestApplyReconcilesSpecs(t *testing.T) {
	manager, err := New([]Job{{Name: "任务", Run: func(context.Context) JobResult { return nil }}})
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}
	spec := func() string {
		items := manager.Tasks()
		if len(items) != 1 {
			t.Fatalf("unexpected tasks: %#v", items)
		}
		return items[0].Spec
	}
	if got := spec(); got != "" {
		t.Fatalf("task without a spec must start unscheduled, got %q", got)
	}
	if err := manager.Apply(map[string]string{"任务": "0 20 * * *"}); err != nil {
		t.Fatalf("schedule task: %v", err)
	}
	if got := spec(); got != "0 20 * * *" {
		t.Fatalf("scheduled spec = %q", got)
	}
	if err := manager.Apply(map[string]string{"任务": "*/5 * * * *"}); err != nil {
		t.Fatalf("reschedule task: %v", err)
	}
	if got := spec(); got != "*/5 * * * *" {
		t.Fatalf("rescheduled spec = %q", got)
	}
	if err := manager.Apply(map[string]string{"任务": "0 1 * * *", "不存在": "0 1 * * *"}); err == nil {
		t.Fatal("expected an unknown task name to be rejected")
	}
	if err := manager.Apply(map[string]string{"任务": "not a cron"}); err == nil {
		t.Fatal("expected an invalid expression to be rejected")
	}
	if got := spec(); got != "*/5 * * * *" {
		t.Fatalf("a rejected apply changed the schedule: %q", got)
	}
	if err := manager.Apply(map[string]string{}); err != nil {
		t.Fatalf("unschedule task: %v", err)
	}
	if got := spec(); got != "" {
		t.Fatalf("unscheduled spec = %q", got)
	}
	if err := manager.RunNow("任务"); err != nil {
		t.Fatalf("an unscheduled task must stay manually runnable: %v", err)
	}
}

// TestApplyWorksWhileRunning 覆盖真实故障场景：调度器已启动时保存设置必须能完成重排，不能阻塞。
func TestApplyWorksWhileRunning(t *testing.T) {
	manager, err := New([]Job{{Name: "任务", Spec: "0 20 * * *", Run: func(context.Context) JobResult { return nil }}})
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}
	if err := manager.Start(); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	applied := make(chan error, 1)
	go func() { applied <- manager.Apply(map[string]string{"任务": "*/1 * * * *"}) }()
	select {
	case err := <-applied:
		if err != nil {
			t.Fatalf("apply while running: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("apply blocked while the scheduler was running")
	}
	if got := manager.Tasks()[0].Spec; got != "*/1 * * * *" {
		t.Fatalf("running reschedule spec = %q", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("stop scheduler: %v", err)
	}
}
