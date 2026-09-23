package runtime_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	runtimeapp "bytemuse/backend/internal/runtime"
)

func TestApplicationGracefullyStopsHTTPAndScheduler(t *testing.T) {
	server := newServerStub()
	scheduler := &schedulerStub{}
	app := runtimeapp.New(server, scheduler, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	select {
	case <-server.started:
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("application did not finish graceful shutdown")
	}
	if !server.wasShutdown() {
		t.Fatal("HTTP server was not shut down")
	}
	if !scheduler.wasStopped() {
		t.Fatal("scheduler was not stopped")
	}
}

type serverStub struct {
	started  chan struct{}
	closed   chan struct{}
	mu       sync.Mutex
	shutdown bool
}

func newServerStub() *serverStub {
	return &serverStub{started: make(chan struct{}), closed: make(chan struct{})}
}

func (s *serverStub) ListenAndServe() error {
	close(s.started)
	<-s.closed
	return http.ErrServerClosed
}

func (s *serverStub) Shutdown(context.Context) error {
	s.mu.Lock()
	if !s.shutdown {
		s.shutdown = true
		close(s.closed)
	}
	s.mu.Unlock()
	return nil
}

func (s *serverStub) wasShutdown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown
}

type schedulerStub struct {
	mu      sync.Mutex
	started bool
	stopped bool
}

func (s *schedulerStub) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("started twice")
	}
	s.started = true
	return nil
}

func (s *schedulerStub) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return errors.New("stopped before start")
	}
	s.stopped = true
	return nil
}

func (s *schedulerStub) wasStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}
