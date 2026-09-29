// Package scheduler manages background jobs as one process-scoped lifecycle.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bytemuse/backend/internal/logging"

	"github.com/robfig/cron/v3"
)

// JobResult contains structured values that are added to the completion log.
type JobResult map[string]any

// Job is one named cron callback. Run must honor context cancellation.
// Spec is the initial cron expression; an empty Spec registers the task without
// scheduling it until Apply supplies one.
type Job struct {
	Name string
	Spec string
	Run  func(context.Context) JobResult
}

// TaskInfo is the scheduler state exposed to the management API.
type TaskInfo struct {
	Name    string
	Spec    string
	LastRun *time.Time
	Running bool
}

// ErrTaskNotFound reports an unknown configured scheduler task.
var ErrTaskNotFound = errors.New("scheduler task not found")

// Manager owns one cron engine and makes Start and Stop idempotent.
type Manager struct {
	mu      sync.Mutex
	engine  *cron.Cron
	ctx     context.Context
	cancel  context.CancelFunc
	running atomic.Bool
	jobs    map[string]*jobState
}

type jobState struct {
	job     Job
	spec    string
	entryID cron.EntryID
	lastRun *time.Time
	running atomic.Bool
}

// New validates and registers all background jobs without starting goroutines.
// It schedules only the jobs that already carry a non-empty Spec; callers that keep
// cron expressions in configuration apply them through Apply after construction.
func New(jobs []Job) (*Manager, error) {
	engine := cron.New()
	manager := &Manager{engine: engine, jobs: make(map[string]*jobState, len(jobs))}
	ctx, cancel := context.WithCancel(context.Background())
	manager.ctx = ctx
	manager.cancel = cancel
	specs := make(map[string]string, len(jobs))
	for _, job := range jobs {
		if job.Name == "" || job.Run == nil {
			cancel()
			return nil, fmt.Errorf("scheduler job name and callback are required")
		}
		if _, exists := manager.jobs[job.Name]; exists {
			cancel()
			return nil, fmt.Errorf("scheduler job name must be unique: %q", job.Name)
		}
		manager.jobs[job.Name] = &jobState{job: job}
		if spec := strings.TrimSpace(job.Spec); spec != "" {
			specs[job.Name] = spec
		}
	}
	if err := manager.Apply(specs); err != nil {
		cancel()
		return nil, err
	}
	return manager, nil
}

// Apply reconciles the cron expression of every registered task without restarting the
// engine, so a configuration change takes effect on the next trigger instead of the next
// process start. An empty spec unschedules the task while keeping it manually runnable;
// tasks missing from specs are unscheduled. Validation is all-or-nothing: an unknown task
// name or an invalid expression is rejected before anything is changed, so a failed call
// leaves the running schedule untouched.
func (m *Manager) Apply(specs map[string]string) error {
	schedules := make(map[string]cron.Schedule, len(specs))
	for name, spec := range specs {
		if _, exists := m.jobs[name]; !exists {
			return fmt.Errorf("scheduler task is not registered: %q", name)
		}
		schedule, err := cron.ParseStandard(spec)
		if err != nil {
			return fmt.Errorf("register scheduler job %q: %w", name, err)
		}
		schedules[name] = schedule
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, state := range m.jobs {
		schedule, scheduled := schedules[name]
		if scheduled && state.entryID != 0 && state.spec == specs[name] {
			continue
		}
		if state.entryID != 0 {
			m.engine.Remove(state.entryID)
			state.entryID = 0
			state.spec = ""
		}
		if !scheduled {
			continue
		}
		state.entryID = m.engine.Schedule(schedule, cron.FuncJob(func() { m.execute(m.ctx, state) }))
		state.spec = specs[name]
	}
	return nil
}

// execute wraps all cron and manual executions with identical state tracking and logging.
func (m *Manager) execute(ctx context.Context, state *jobState) {
	if !state.running.CompareAndSwap(false, true) {
		logging.Info(logging.CategorySystem, "定时任务正在执行，忽略重复触发", "task", state.job.Name)
		return
	}
	m.executeClaimed(ctx, state)
}

func (m *Manager) executeClaimed(ctx context.Context, state *jobState) {
	now := time.Now().UTC()
	m.mu.Lock()
	state.lastRun = &now
	m.mu.Unlock()
	logging.Info(logging.CategorySystem, "定时任务开始执行", "task", state.job.Name)
	result := JobResult(nil)
	defer func() {
		state.running.Store(false)
		if recovered := recover(); recovered != nil {
			logging.Error(logging.CategorySystem, "定时任务执行失败", "task", state.job.Name, "error", fmt.Sprint(recovered))
			return
		}
		attrs := []any{"task", state.job.Name}
		for key, value := range result {
			attrs = append(attrs, key, value)
		}
		logging.Info(logging.CategorySystem, "定时任务执行完成", attrs...)
	}()
	result = state.job.Run(ctx)
}

// Start starts the cron engine exactly once for this manager instance.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running.Load() {
		return nil
	}
	m.engine.Start()
	m.running.Store(true)
	logging.Info(logging.CategorySystem, "后台调度器已启动")
	return nil
}

// Stop cancels job contexts and waits for cron callbacks until ctx expires.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.running.Load() {
		m.mu.Unlock()
		return nil
	}
	m.running.Store(false)
	m.cancel()
	stopped := m.engine.Stop()
	m.mu.Unlock()
	select {
	case <-stopped.Done():
		logging.Info(logging.CategorySystem, "后台调度器已停止")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Tasks returns one stable snapshot of configured task state, ordered by task name.
func (m *Manager) Tasks() []TaskInfo {
	m.mu.Lock()
	items := make([]TaskInfo, 0, len(m.jobs))
	for _, state := range m.jobs {
		item := TaskInfo{Name: state.job.Name, Spec: state.spec, Running: state.running.Load()}
		if state.lastRun != nil {
			lastRun := *state.lastRun
			item.LastRun = &lastRun
		}
		items = append(items, item)
	}
	m.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// RunNow starts one configured task asynchronously and rejects unknown or duplicate executions.
func (m *Manager) RunNow(name string) error {
	m.mu.Lock()
	state, exists := m.jobs[name]
	m.mu.Unlock()
	if !exists {
		return ErrTaskNotFound
	}
	if !state.running.CompareAndSwap(false, true) {
		return fmt.Errorf("task %q is already running", name)
	}
	go m.executeClaimed(m.ctx, state)
	return nil
}

// Running reports whether the scheduler currently accepts cron triggers.
func (m *Manager) Running() bool { return m.running.Load() }
