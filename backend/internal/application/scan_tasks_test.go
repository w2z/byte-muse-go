package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/database"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type memoryScanTasks struct {
	mu    sync.Mutex
	items map[string]domain.ScanTask
}

// TestScanTasksCloseAndReopen verifies real persisted checkpoints survive closing and reopening the database.
func TestScanTasksCloseAndReopen(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "paused"}[paused], func(t *testing.T) {
			ctx := context.Background()
			config := database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "restart.db")}
			store, err := database.Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			manager, err := NewScanTasks(ctx, database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite))
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			task, err := manager.Start(ctx, "strm", "full", func(worker context.Context) (any, error) {
				if err := completeTaskUnit(worker, "first"); err != nil {
					return nil, err
				}
				close(entered)
				<-worker.Done()
				return nil, worker.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				manager.Close()
				store.Close()
				t.Fatal("worker did not start")
			}
			if paused {
				if _, err := manager.Control(ctx, "strm", task.ID, "pause"); err != nil {
					t.Fatal(err)
				}
			}
			manager.Close()
			store.Close()
			store, err = database.Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			manager, err = NewScanTasks(ctx, database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite))
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			manager.RegisterRunner("strm", func(worker context.Context, mode string) (any, error) {
				done, err := taskUnitDone(worker, "first")
				if err != nil || !done || mode != "full" {
					return nil, errors.New("lost checkpoint or mode")
				}
				return nil, nil
			})
			if err := manager.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if paused {
				waitScanState(t, manager, "strm", "paused")
				if _, err := manager.Control(ctx, "strm", task.ID, "resume"); err != nil {
					t.Fatal(err)
				}
			}
			waitScanState(t, manager, "strm", "completed")
		})
	}
}

// TestScanTasksAutoRecover verifies restart policy and reuse of persisted task identity and checkpoints.
func TestScanTasksAutoRecover(t *testing.T) {
	for _, state := range []string{"running", "interrupted", "paused", "pausing", "canceling", "canceled", "completed", "failed"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "recover.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			repo := database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite)
			for _, kind := range []string{"library", "strm"} {
				task := domain.ScanTask{ID: kind, Kind: kind, State: state, Mode: "full", CanRetry: true}
				if err := repo.Save(ctx, task); err != nil {
					t.Fatal(err)
				}
				if err := repo.SaveCheckpoint(ctx, kind, journalKey("first"), []byte("true")); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := NewScanTasks(ctx, repo)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			for _, kind := range []string{"library", "strm"} {
				manager.RegisterRunner(kind, func(worker context.Context, mode string) (any, error) {
					if err := scanCheckpoint(worker); err != nil {
						return nil, err
					}
					done, err := taskUnitDone(worker, "first")
					if err != nil || !done || mode != "full" {
						return nil, errors.New("lost task input or checkpoint")
					}
					return nil, nil
				})
			}
			if err := manager.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if err := manager.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"library", "strm"} {
				want := state
				switch state {
				case "running", "interrupted":
					want = "completed"
				case "pausing", "paused":
					want = "paused"
				case "canceling":
					want = "canceled"
				}
				waitScanState(t, manager, kind, want)
				if want == "paused" {
					if _, err := manager.Control(ctx, kind, kind, "resume"); err != nil {
						t.Fatal(err)
					}
					waitScanState(t, manager, kind, "completed")
				}
			}
		})
	}
}

// TestScanTaskRetryAfterRestart keeps completed writes and rejects stale or duplicate retries.
func TestScanTaskRetryAfterRestart(t *testing.T) {
	ctx := context.Background()
	config := database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "retry.db")}
	store, err := database.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	manager, err := NewScanTasks(ctx, database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runner := func(worker context.Context) (any, error) {
		done, err := taskUnitDone(worker, "first")
		if err != nil {
			return nil, err
		}
		if !done {
			calls++
			if err := completeTaskUnit(worker, "first"); err != nil {
				return nil, err
			}
		}
		return nil, errors.New("second failed")
	}
	task, err := manager.Start(ctx, "library", "", runner)
	if err != nil {
		t.Fatal(err)
	}
	waitScanState(t, manager, "library", "failed")
	manager.Close()
	store.Close()
	store, err = database.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err = NewScanTasks(ctx, database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	manager.RegisterRunner("library", func(worker context.Context, _ string) (any, error) {
		done, err := taskUnitDone(worker, "first")
		if err != nil || !done {
			return nil, errors.New("lost checkpoint")
		}
		close(entered)
		<-release
		return domain.Pan115LibraryScanResult{}, nil
	})
	if _, err := manager.Control(ctx, "library", "stale", "retry"); !errors.Is(err, ErrScanTaskConflict) {
		t.Fatal(err)
	}
	next, err := manager.Control(ctx, "library", task.ID, "retry")
	if err != nil || next.ID != task.ID {
		t.Fatalf("%+v %v", next, err)
	}
	<-entered
	_, duplicateErr := manager.Control(ctx, "library", task.ID, "retry")
	close(release)
	if !errors.Is(duplicateErr, ErrScanTaskConflict) {
		t.Fatal(duplicateErr)
	}
	waitScanState(t, manager, "library", "completed")
	latest, _ := manager.Latest(ctx, "library")
	if latest.CanRetry || calls != 1 {
		t.Fatalf("%+v calls=%d", latest, calls)
	}
}

// TestStrmRetryPreservesFullGeneration confirms retry never clears already generated output.
func TestStrmRetryPreservesFullGeneration(t *testing.T) {
	root := t.TempDir()
	source := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{"root": {Files: []domain.Pan115File{
		{ID: "ok", Name: "SSIS-001.mp4", PickCode: "pc1"},
		{ID: "folder", Name: "child", IsDirectory: true},
	}}}}
	values := map[string]string{strmPathsSettingKey: strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "root", Path: "/source", LocalPath: "/movies"}})}
	service := newStrmTestService(t, root, source, nil, values)
	ctx := journalContext(context.Background(), &taskJournal{id: "test"})
	result, err := service.Scan(ctx, "http://play.test", domain.StrmGenerateFull)
	if err != nil || !scanResultFailed(result) {
		t.Fatalf("%+v %v", result, err)
	}
	existing := filepath.Join(root, "movies", "SSIS-001.mp4.strm")
	if err := os.WriteFile(existing, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	source.pages["folder"] = domain.Pan115FilePage{Files: []domain.Pan115File{{ID: "remaining", Name: "SSIS-002.mp4", PickCode: "pc2"}}}
	delete(source.pages, "root")
	values[strmPathsSettingKey] = "[]"
	result, err = service.Scan(ctx, "", domain.StrmGenerateFull)
	if err != nil || scanResultFailed(result) || result.Deleted != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	raw, _ := os.ReadFile(existing)
	if string(raw) != "preserved" {
		t.Fatal("rewrote completed file")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Join("movies", "child", "SSIS-002.mp4.strm")))); err != nil {
		t.Fatal(err)
	}
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
