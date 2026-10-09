package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// ErrScanTaskConflict 表示同类任务尚未结束、任务已替换或控制动作不适用于当前状态。
var ErrScanTaskConflict = errors.New("任务状态已变化，请刷新后重试")

type scanExecution struct {
	task     domain.ScanTask
	cancel   context.CancelFunc
	wake     chan struct{}
	lastSave time.Time
	saveErr  error
}

// ScanTasks 管理单实例内两类独立后台任务；请求断开不取消任务。
// 暂停在安全处理点确认；服务退出保留断点，启动后恢复运行任务但不自动解除暂停。
type ScanTasks struct {
	mu       sync.Mutex
	repo     ports.ScanTaskRepository
	ctx      context.Context
	cancel   context.CancelFunc
	active   map[string]*scanExecution
	wg       sync.WaitGroup
	closed   bool
	runners  map[string]func(context.Context, string) (any, error)
	journals map[string]*taskJournal
	files    *scanFileTree
}

// NewScanTasks 恢复上次运行遗留状态；必须在启动 HTTP 之前且每个服务进程仅调用一次。
func NewScanTasks(ctx context.Context, repo ports.ScanTaskRepository) (*ScanTasks, error) {
	if err := repo.Interrupt(ctx); err != nil {
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	return &ScanTasks{repo: repo, ctx: workerCtx, cancel: cancel, active: make(map[string]*scanExecution)}, nil
}

// Close 取消并等待本服务拥有的任务，保证数据库关闭前落下最后状态。
func (s *ScanTasks) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.wg.Wait() }

func (s *ScanTasks) save(e *scanExecution) error {
	e.task.UpdatedAt = time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.repo.Save(ctx, e.task)
	if err != nil {
		e.saveErr = err
		e.cancel()
	}
	e.lastSave = time.Now()
	return err
}

// Start 先提交任务快照再启动工作；同类活动任务拒绝重复提交，不同类型互不阻塞。
func (s *ScanTasks) Start(ctx context.Context, kind, mode string, run func(context.Context) (any, error)) (*domain.ScanTask, error) {
	return s.start(ctx, kind, mode, "", run)
}

// RegisterRunner binds a task kind to its restart-safe service entry point.
func (s *ScanTasks) RegisterRunner(kind string, run func(context.Context, string) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runners == nil {
		s.runners = map[string]func(context.Context, string) (any, error){}
	}
	s.runners[kind] = run
}

// Recover 在全部 runner 注册后恢复最新中断任务；暂停任务只重建等待中的执行器。
// 重复调用不会启动同一任务两次，缺少断点的历史任务及其他终态不自动执行。
func (s *ScanTasks) Recover(ctx context.Context) error {
	s.mu.Lock()
	kinds := make([]string, 0, len(s.runners))
	for kind := range s.runners {
		kinds = append(kinds, kind)
	}
	s.mu.Unlock()
	for _, kind := range kinds {
		task, err := s.repo.Latest(ctx, kind)
		if err != nil {
			return err
		}
		if task == nil || !task.CanRetry || (task.State != "interrupted" && task.State != "paused") {
			continue
		}
		if _, err := s.start(ctx, kind, "", task.ID, nil); err != nil && !errors.Is(err, ErrScanTaskConflict) {
			return err
		}
	}
	return nil
}

func (s *ScanTasks) start(ctx context.Context, kind, mode, retryID string, run func(context.Context) (any, error)) (*domain.ScanTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return nil, context.Canceled
	}
	if s.active[kind] != nil {
		return nil, ErrScanTaskConflict
	}
	latest, err := s.repo.Latest(ctx, kind)
	if err != nil {
		return nil, err
	}
	// 若最后一次终态落库失败，禁止用新任务覆盖尚未确认结束的持久化记录。
	if latest != nil && latest.Active() && !(latest.State == "paused" && retryID == latest.ID) {
		return nil, ErrScanTaskConflict
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(s.ctx)
	e := &scanExecution{task: domain.ScanTask{ID: hex.EncodeToString(id[:]), Kind: kind, Mode: mode, State: "running", Progress: domain.ScanProgress{Phase: "waiting"}, CreatedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")}, cancel: cancel, wake: make(chan struct{})}
	if retryID != "" {
		if latest == nil || latest.ID != retryID || !latest.CanRetry || s.runners[kind] == nil {
			cancel()
			return nil, ErrScanTaskConflict
		}
		e.task = *latest
		e.task.State, e.task.Error, e.task.CanRetry = "running", "", false
		if latest.State == "paused" {
			e.task.State = "paused"
		}
		runner := s.runners[kind]
		run = func(ctx context.Context) (any, error) {
			if err := scanCheckpoint(ctx); err != nil {
				return nil, err
			}
			return runner(ctx, e.task.Mode)
		}
	}
	if s.journals == nil {
		s.journals = map[string]*taskJournal{}
	}
	journal := s.journals[e.task.ID]
	if journal == nil {
		repo, _ := s.repo.(ports.TaskCheckpointRepository)
		journal = &taskJournal{repo: repo, id: e.task.ID}
		s.journals[e.task.ID] = journal
	}
	workerCtx = journalContext(workerCtx, journal)
	if err := completeTaskUnit(workerCtx, "initialized"); err != nil {
		cancel()
		return nil, err
	}
	if err := s.save(e); err != nil {
		cancel()
		return nil, err
	}
	s.active[kind] = e
	if kind == "strm" {
		s.files = newScanFileTree(e.task.ID)
		workerCtx = context.WithValue(workerCtx, scanFileTreeKey{}, s.files)
	}
	initial := e.task
	logging.Info(scanTaskCategory(kind), scanTaskName(kind)+" 任务已开始", "task_id", e.task.ID, "mode", mode)
	workerCtx = context.WithValue(workerCtx, scanCheckpointKey{}, func() error { return s.checkpoint(workerCtx, e) })
	workerCtx = WithScanProgress(workerCtx, func(p ScanProgress) {
		s.mu.Lock()
		defer s.mu.Unlock()
		previous := e.task.Progress
		e.task.Progress = p
		if previous.Phase != p.Phase || p.Phase == "completed" || time.Since(e.lastSave) >= 250*time.Millisecond {
			_ = s.save(e)
		}
	})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		result, err := run(workerCtx)
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case e.saveErr != nil:
			e.task.State = "failed"
			e.task.Error = "保存任务进度失败，请检查数据库"
		case e.task.State == "canceling":
			e.task.State = "canceled"
		case s.ctx.Err() != nil:
			if e.task.State == "paused" || e.task.State == "pausing" {
				e.task.State, e.task.Error = "paused", ""
			} else {
				e.task.State = "interrupted"
				e.task.Error = "服务停止，下次启动后自动恢复"
			}
		case err != nil:
			e.task.State = "failed"
			e.task.Error = "任务执行失败，请检查目录配置和服务状态"
		default:
			e.task.State = "completed"
			e.task.Progress.Percent = 100
		}
		e.task.Result, _ = json.Marshal(result)
		e.task.CanRetry = e.task.State != "completed" || scanResultFailed(result)
		_ = s.save(e)
		attrs := []any{"task_id", e.task.ID, "mode", mode, "state", e.task.State, "processed", e.task.Progress.Processed, "total", e.task.Progress.Total}
		if e.task.Error != "" {
			attrs = append(attrs, "error", e.task.Error)
		}
		if e.task.State == "completed" {
			logging.Info(scanTaskCategory(kind), scanTaskName(kind)+" 任务已完成", attrs...)
		} else {
			logging.Error(scanTaskCategory(kind), scanTaskName(kind)+" 任务未完成", attrs...)
		}
		delete(s.active, kind)
		if journal.repo != nil {
			delete(s.journals, e.task.ID)
		}
	}()
	return &initial, nil
}

func scanTaskName(kind string) string {
	if kind == "library" {
		return "扫描媒体库"
	}
	if kind == "strm" {
		return "生成 STRM"
	}
	return "媒体任务"
}

func scanTaskCategory(kind string) logging.Category {
	if kind == "library" {
		return logging.CategoryLibraryScan
	}
	if kind == "strm" {
		return logging.CategoryStrmGenerate
	}
	return logging.CategoryMedia
}

// Latest 每次读取数据库中的最新快照，页面刷新后仍得到相同任务标识与进度。
func (s *ScanTasks) Latest(ctx context.Context, kind string) (*domain.ScanTask, error) {
	return s.repo.Latest(ctx, kind)
}

// Get 为仍在等待结果的调用者读取指定任务，后续任务不会覆盖该结果。
func (s *ScanTasks) Get(ctx context.Context, id string) (*domain.ScanTask, error) {
	return s.repo.Get(ctx, id)
}

// Control 校验任务 ID 防止旧页面误操作新任务；重复暂停或取消具有幂等性。
func (s *ScanTasks) Control(ctx context.Context, kind, id, action string) (*domain.ScanTask, error) {
	if action == "retry" {
		return s.start(ctx, kind, "", id, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.active[kind]
	if e == nil || e.task.ID != id {
		return nil, ErrScanTaskConflict
	}
	switch action {
	case "pause":
		if e.task.State == "running" {
			e.task.State = "pausing"
		} else if e.task.State != "pausing" && e.task.State != "paused" {
			return nil, ErrScanTaskConflict
		}
	case "resume":
		if e.task.State != "paused" && e.task.State != "pausing" {
			return nil, ErrScanTaskConflict
		}
		e.task.State = "running"
	case "cancel":
		e.task.State = "canceling"
	default:
		return nil, ErrScanTaskConflict
	}
	if err := s.save(e); err != nil {
		return nil, err
	}
	close(e.wake)
	e.wake = make(chan struct{})
	if action == "cancel" {
		e.cancel()
	}
	task := e.task
	return &task, nil
}

// scanResultFailed includes isolated directory, download and final refresh failures.
func scanResultFailed(result any) bool {
	switch value := result.(type) {
	case domain.StrmScanResult:
		if value.Failed > 0 || value.DownloadFailed > 0 || (value.Emby.Attempted && !value.Emby.Refreshed) {
			return true
		}
		for _, entry := range value.Mappings {
			if entry.Message != "" {
				return true
			}
		}
	case domain.Pan115LibraryScanResult:
		for _, entry := range value.Directories {
			if entry.Message != "" {
				return true
			}
		}
	}
	return false
}

func (s *ScanTasks) checkpoint(ctx context.Context, e *scanExecution) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		if e.task.State == "pausing" {
			e.task.State = "paused"
			if err := s.save(e); err != nil {
				s.mu.Unlock()
				return err
			}
		}
		paused := e.task.State == "paused"
		wake := e.wake
		s.mu.Unlock()
		if !paused {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

type scanCheckpointKey struct{}

// scanCheckpoint 仅在后台任务上下文中等待暂停；同步调用沿用原有取消语义。
func scanCheckpoint(ctx context.Context) error {
	if fn, ok := ctx.Value(scanCheckpointKey{}).(func() error); ok {
		return fn()
	}
	return ctx.Err()
}
