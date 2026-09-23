// Package scheduler manages background jobs as one process-scoped lifecycle.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/robfig/cron/v3"
)

// Job is one named cron callback. Run must honor context cancellation.
type Job struct {
	Name string
	Spec string
	Run  func(context.Context)
}

// Manager owns one cron engine and makes Start and Stop idempotent.
type Manager struct {
	mu         sync.Mutex
	engine     *cron.Cron
	cancel     context.CancelFunc
	running    atomic.Bool
	startCount atomic.Int32
}

// New validates and registers all background jobs without starting goroutines.
func New(jobs []Job) (*Manager, error) {
	engine := cron.New()
	manager := &Manager{engine: engine}
	ctx, cancel := context.WithCancel(context.Background())
	manager.cancel = cancel
	for _, job := range jobs {
		if job.Name == "" || job.Run == nil {
			cancel()
			return nil, fmt.Errorf("scheduler job name and callback are required")
		}
		current := job
		if _, err := engine.AddFunc(current.Spec, func() { current.Run(ctx) }); err != nil {
			cancel()
			return nil, fmt.Errorf("register scheduler job %q: %w", current.Name, err)
		}
	}
	return manager, nil
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
	m.startCount.Add(1)
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
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Running reports whether the scheduler currently accepts cron triggers.
func (m *Manager) Running() bool { return m.running.Load() }

// StartCount exposes the underlying start count for lifecycle diagnostics and tests.
func (m *Manager) StartCount() int { return int(m.startCount.Load()) }
