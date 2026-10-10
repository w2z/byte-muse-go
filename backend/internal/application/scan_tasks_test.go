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
	"testing/synctest"
	"time"
)

type memoryScanTasks struct {
	mu    sync.Mutex
	items map[string]domain.ScanTask
}

// restartProgressRepository 记录真实 SQLite 快照，用于检查恢复期间是否曾将进度归零。
type restartProgressRepository struct {
	*database.ScanTaskRepository
	mutex    sync.Mutex
	progress []ScanProgress
}

func (repo *restartProgressRepository) Save(ctx context.Context, task domain.ScanTask) error {
	repo.mutex.Lock()
	repo.progress = append(repo.progress, task.Progress)
	repo.mutex.Unlock()
	return repo.ScanTaskRepository.Save(ctx, task)
}

// TestStrmRestartRestoresProgressAndPendingWork 通过关闭并重开 SQLite 验证旧断点恢复，不重复查询已保存页或写入完成文件。
func TestStrmRestartRestoresProgressAndPendingWork(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "restart-progress.db")}
	store, err := database.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite)
	task := domain.ScanTask{ID: "restart-progress", Kind: "strm", Mode: "full", State: "paused", CanRetry: true, Progress: ScanProgress{Phase: "processing", Processed: 2, Total: 3, Percent: 66, Current: "/source"}}
	if err := repo.Save(ctx, task); err != nil {
		t.Fatal(err)
	}
	worker := journalContext(ctx, &taskJournal{repo: repo, id: task.ID})
	mapping := domain.StrmMapping{Kind: "115", ID: "root", Path: "/source", LocalPath: "/movies"}
	values := map[string]string{strmPathsSettingKey: strmTestMappings(t, []domain.StrmMapping{mapping}), strmPlayBaseSettingKey: "http://play.test"}
	page := domain.Pan115FilePage{Files: []domain.Pan115File{
		{ID: "done-first", Name: "first.mp4", PickCode: "first"},
		{ID: "pending", Name: "pending.mp4", PickCode: "pending"},
		{ID: "child", Name: "child", IsDirectory: true},
		{ID: "done-last", Name: "last.mp4", PickCode: "last"},
	}}
	if err := saveTaskCheckpoint(worker, journalKey("115-page", "root", "0"), page); err != nil {
		t.Fatal(err)
	}
	if err := saveTaskCheckpoint(worker, journalKey("cleanup", mapping.Kind, mapping.ID, mapping.LocalPath), true); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "movies")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct{ id, name string }{{"done-first", "first.strm"}, {"done-last", "last.strm"}} {
		if err := completeTaskUnit(worker, "strm-file", target, "", file.id); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, file.name), []byte("preserved"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	store, err = database.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recorded := &restartProgressRepository{ScanTaskRepository: database.NewScanTaskRepository(store.SQLDB(), database.DialectSQLite)}
	manager, err := NewScanTasks(ctx, recorded)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	api := &retryStrmPan115Stub{}
	api.list = func(_ context.Context, directory string, offset, limit int) (domain.Pan115FilePage, error) {
		if directory != "child" || offset != 0 {
			t.Errorf("重新请求已缓存目录: %s offset=%d", directory, offset)
		}
		snapshot, err := manager.Latest(ctx, "strm")
		if err != nil || snapshot.Progress.Total != 3 || snapshot.Progress.Processed < 2 {
			t.Errorf("请求未扫描目录前未恢复计数: %+v %v", snapshot, err)
		}
		return domain.Pan115FilePage{}, nil
	}
	service := newStrmTestService(t, root, api, nil, values)
	manager.RegisterRunner("strm", func(worker context.Context, mode string) (any, error) {
		return service.Scan(worker, "", domain.StrmGenerateMode(mode))
	})
	if err := manager.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Control(ctx, "strm", task.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	waitScanState(t, manager, "strm", "completed")
	latest, _ := manager.Latest(ctx, "strm")
	if latest.Progress.Total != 3 || latest.Progress.Processed != 3 {
		t.Fatalf("最终计数不符: %+v", latest.Progress)
	}
	recorded.mutex.Lock()
	defer recorded.mutex.Unlock()
	for _, progress := range recorded.progress {
		if progress.Total < 3 || progress.Processed < 2 {
			t.Errorf("恢复期间计数回退: %+v", progress)
		}
	}
	for _, name := range []string{"first.strm", "last.strm"} {
		raw, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(raw) != "preserved" {
			t.Errorf("已完成文件被改写: %s %v", name, err)
		}
	}
	pending, err := filepath.Glob(filepath.Join(target, "pending*.strm"))
	if err != nil || len(pending) != 1 {
		t.Fatalf("未完成文件未生成: %v %v", pending, err)
	}
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
	ctx = context.WithValue(ctx, strmRetryKey{}, &strmRetryPolicy{wait: func(waitCtx context.Context, _ string, _ error) error {
		for {
			done, err := taskUnitDone(waitCtx, "strm-file", filepath.Join(root, "movies"), "", "ok")
			if err != nil {
				return err
			}
			if done {
				return context.Canceled
			}
			select {
			case <-waitCtx.Done():
				return waitCtx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}})
	result, err := service.Scan(ctx, "http://play.test", domain.StrmGenerateFull)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%+v %v", result, err)
	}
	existing := filepath.Join(root, "movies", "SSIS-001.strm")
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
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Join("movies", "child", "SSIS-002.strm")))); err != nil {
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

// TestScanTaskResumeClearsStaleCooldownProgress verifies manual resume does not keep showing an old 115 cooldown.
func TestScanTaskResumeClearsStaleCooldownProgress(t *testing.T) {
	ctx := context.Background()
	repo := &memoryScanTasks{items: map[string]domain.ScanTask{}}
	service, err := NewScanTasks(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	entered := make(chan struct{})
	continueRun := make(chan struct{})
	task, err := service.Start(ctx, "strm", "incremental", func(worker context.Context) (any, error) {
		reportScanProgress(worker, "cooling", 2, 5, "/影片")
		close(entered)
		<-continueRun
		if err := scanCheckpoint(worker); err != nil {
			return nil, err
		}
		<-worker.Done()
		return nil, worker.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := service.Control(ctx, "strm", task.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	close(continueRun)
	waitScanState(t, service, "strm", "paused")
	resumed, err := service.Control(ctx, "strm", task.ID, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Progress.Phase == "cooling" {
		t.Fatalf("继续后仍保留过期限流阶段: %+v", resumed.Progress)
	}
	if resumed.Progress.Phase != "processing" {
		t.Fatalf("继续后阶段 = %q，期望 processing", resumed.Progress.Phase)
	}
	if resumed.Progress.Processed != 2 || resumed.Progress.Total != 5 || resumed.Progress.Percent != 40 || resumed.Progress.Current != "正在恢复任务，等待执行结果" {
		t.Fatalf("恢复时丢失计数或残留旧提示: %+v", resumed.Progress)
	}
	if _, err := service.Control(ctx, "strm", task.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitScanState(t, service, "strm", "canceled")
}

// TestScanTaskResumeAfterLongCooldownPause 验证暂停时间计入冷却，继续立即重试而非重新等待一分钟。
func TestScanTaskResumeAfterLongCooldownPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		manager, err := NewScanTasks(ctx, &memoryScanTasks{items: map[string]domain.ScanTask{}})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		attempts := 0
		probe := make(chan struct{})
		release := make(chan struct{})
		task, err := manager.Start(ctx, "strm", "incremental", func(worker context.Context) (any, error) {
			worker = context.WithValue(worker, strmRetryKey{}, &strmRetryPolicy{})
			worker = context.WithValue(worker, strmRetryProgressKey{}, func() {
				reportScanProgress(worker, "cooling", 2, 5, pan115CooldownNotice("/影片", time.Minute))
			})
			return nil, retryStrmScan(worker, func() error {
				attempts++
				if attempts == 1 {
					return errors.New("已达到当前访问上限")
				}
				if attempts > 2 {
					return nil
				}
				close(probe)
				select {
				case <-release:
					return errors.New("已达到当前访问上限")
				case <-worker.Done():
					return worker.Err()
				}
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, err := manager.Control(ctx, "strm", task.ID, "pause"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Hour)
		synctest.Wait()
		paused, _ := manager.Latest(ctx, "strm")
		if paused.State != "paused" || attempts != 1 {
			t.Fatalf("paused=%+v attempts=%d", paused, attempts)
		}
		before := time.Now()
		if _, err := manager.Control(ctx, "strm", task.ID, "resume"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case <-probe:
		default:
			t.Fatal("已过期冷却未立即重试")
		}
		if time.Since(before) != 0 || attempts != 2 {
			t.Fatalf("elapsed=%s attempts=%d", time.Since(before), attempts)
		}
		resumed, _ := manager.Latest(ctx, "strm")
		if resumed.Progress.Phase == "cooling" {
			t.Fatal("探测请求仍显示旧限流")
		}
		close(release)
		synctest.Wait()
		relimited, _ := manager.Latest(ctx, "strm")
		if relimited.Progress.Phase != "cooling" || attempts != 2 {
			t.Fatalf("真实再次限流必须重新等待: %+v attempts=%d", relimited, attempts)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		finished, _ := manager.Latest(ctx, "strm")
		if finished.State != "completed" || attempts != 3 {
			t.Fatalf("%+v", finished)
		}
	})
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
