package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type memoryScanTasks struct {
	mu    sync.Mutex
	items map[string]domain.ScanTask
}

func (r *memoryScanTasks) Save(_ context.Context, t domain.ScanTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[t.Kind] = t
	return nil
}
func (r *memoryScanTasks) Latest(_ context.Context, k string) (*domain.ScanTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.items[k]
	if !ok {
		return nil, nil
	}
	return &t, nil
}
func (r *memoryScanTasks) Interrupt(context.Context) error { return nil }

func (r *memoryScanTasks) Get(_ context.Context, id string) (*domain.ScanTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.items {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, nil
}

func TestScanTasksPauseResumeCancelAndIsolation(t *testing.T) {
	ctx := context.Background()
	repo := &memoryScanTasks{items: map[string]domain.ScanTask{}}
	service, err := NewScanTasks(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	entered := make(chan struct{})
	proceed := make(chan struct{})
	advanced := make(chan struct{})
	task, err := service.Start(ctx, "library", "", func(ctx context.Context) (any, error) {
		reportScanProgress(ctx, "discovering", 2, 8, "/movies")
		close(entered)
		<-proceed
		if err := scanCheckpoint(ctx); err != nil {
			return nil, err
		}
		close(advanced)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err = service.Start(ctx, "library", "", nil); !errors.Is(err, ErrScanTaskConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	other, err := service.Start(ctx, "strm", "incremental", func(ctx context.Context) (any, error) { <-ctx.Done(); return nil, ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Control(ctx, "library", task.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	waitScanState(t, service, "library", "paused")
	select {
	case <-advanced:
		t.Fatal("advanced while paused")
	default:
	}
	got, _ := service.Latest(ctx, "library")
	if got.Progress.Total != 8 || got.Progress.Percent != 25 {
		t.Fatalf("progress %+v", got)
	}
	if _, err = service.Control(ctx, "library", "stale", "cancel"); !errors.Is(err, ErrScanTaskConflict) {
		t.Fatalf("stale control: %v", err)
	}
	if _, err = service.Control(ctx, "library", task.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-advanced:
	case <-time.After(time.Second):
		t.Fatal("resume did not advance")
	}
	if _, err = service.Control(ctx, "library", task.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitScanState(t, service, "library", "canceled")
	otherState, _ := service.Latest(ctx, "strm")
	if otherState.ID != other.ID || otherState.State != "running" {
		t.Fatal("cross-task cancellation")
	}
	service.Close()
	waitScanState(t, service, "strm", "interrupted")
}

func waitScanState(t *testing.T, s *ScanTasks, kind, state string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := s.Latest(context.Background(), kind)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil && got.State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	got, _ := s.Latest(context.Background(), kind)
	t.Fatalf("wanted %s got %+v", state, got)
}

// TestScanTaskCanceledCleanupPreservesFiles 防止取消后的全量清理继续删除已有文件。
func TestScanTaskCanceledCleanupPreservesFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "movie.strm")
	if err := os.WriteFile(target, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	count, err := clearStrmContentContext(ctx, root, root)
	if !errors.Is(err, context.Canceled) || count != 0 {
		t.Fatalf("cleanup: %d %v", count, err)
	}
	if _, err = os.Stat(target); err != nil {
		t.Fatal("canceled task deleted file", err)
	}
}
