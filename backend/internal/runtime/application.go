// Package runtime coordinates long-lived HTTP and scheduler lifecycles.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// HTTPServer is the subset of http.Server required by the application lifecycle.
type HTTPServer interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

// Scheduler is the process-scoped background scheduler lifecycle.
type Scheduler interface {
	Start(excluded ...string) error
	Stop(ctx context.Context) error
}

// Application starts and gracefully stops all process services together.
type Application struct {
	server          HTTPServer
	scheduler       Scheduler
	shutdownTimeout time.Duration
	startupExcludes []string
}

// New builds a process lifecycle coordinator. startupExcludes skips only the initial
// execution of the named tasks, without changing their cron schedules.
func New(server HTTPServer, scheduler Scheduler, shutdownTimeout time.Duration, startupExcludes ...string) *Application {
	return &Application{server: server, scheduler: scheduler, shutdownTimeout: shutdownTimeout, startupExcludes: append([]string(nil), startupExcludes...)}
}

// Run blocks until cancellation or an HTTP failure, then stops HTTP and scheduler.
func (a *Application) Run(ctx context.Context) error {
	if err := a.scheduler.Start(a.startupExcludes...); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- a.server.ListenAndServe() }()

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("serve HTTP: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown HTTP: %w", err)
	}
	if err := a.scheduler.Stop(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("stop scheduler: %w", err)
	}
	return runErr
}
