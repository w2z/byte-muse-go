package scheduler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"bytemuse/backend/internal/scheduler"
)

func TestSchedulerStartsOnlyOnceAndStops(t *testing.T) {
	var starts atomic.Int32
	manager, err := scheduler.New([]scheduler.Job{{
		Name: "probe",
		Spec: "@every 10ms",
		Run:  func(context.Context) { starts.Add(1) },
	}})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	if err := manager.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := manager.Start(); err != nil {
		t.Fatalf("idempotent second start: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for starts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if starts.Load() == 0 {
		t.Fatal("scheduled job did not run")
	}
	if manager.StartCount() != 1 {
		t.Fatalf("underlying start count = %d, want 1", manager.StartCount())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("stop scheduler: %v", err)
	}
	if manager.Running() {
		t.Fatal("scheduler still reports running after stop")
	}
}
