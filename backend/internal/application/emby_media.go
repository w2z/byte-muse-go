package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// EmbyMediaTask 是 STRM 媒体信息预热任务的可观测快照。
type EmbyMediaTask struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Phase     string    `json:"phase"`
	Processed int       `json:"processed"`
	Total     int       `json:"total"`
	Success   int       `json:"success"`
	Skipped   int       `json:"skipped"`
	Failed    int       `json:"failed"`
	Error     string    `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
	CanRetry  bool      `json:"can_retry"`
}

// EmbyMediaService 先快速扫描 STRM 媒体，再以默认并发数刷新缺失信息；重复触发合并为下一轮。
type EmbyMediaService struct {
	settings func(context.Context) (map[string]string, error)
	http     *http.Client
	mu       sync.Mutex
	task     *EmbyMediaTask
	pending  bool
	cancel   context.CancelFunc
	wake     chan struct{}
	repo     ports.TaskCheckpointRepository
	journal  *taskJournal
	saveErr  error
	wg       sync.WaitGroup
	closed   bool
}

const embyMediaPageSize = 200

// embyMediaWorkers 限制同时进行的 Emby 媒体探测数；115 请求仍遵守网盘客户端的独立限速。
const embyMediaWorkers = 10

func NewEmbyMediaService(settings func(context.Context) (map[string]string, error)) *EmbyMediaService {
	return &EmbyMediaService{settings: settings, http: &http.Client{Timeout: 2 * time.Minute}}
}

// Restore 恢复原任务与媒体断点；运行中任务自动继续，暂停和取消意图跨重启保留。
func (s *EmbyMediaService) Restore(ctx context.Context, repo ports.TaskCheckpointRepository) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.task != nil {
		return ErrScanTaskConflict
	}
	s.repo = repo
	index := &taskJournal{repo: repo, id: "emby-latest"}
	var task EmbyMediaTask
	found, err := index.load(ctx, journalKey("snapshot"), &task)
	if err != nil || !found {
		return err
	}
	s.task = &task
	s.journal = &taskJournal{repo: repo, id: task.ID}
	switch task.State {
	case "running", "queued", "interrupted":
		_, err = s.restartLocked(false)
		return err
	case "paused", "pausing":
		_, err = s.restartLocked(true)
		return err
	case "canceling":
		s.task.State, s.task.CanRetry = "canceled", true
	}
	return s.persistLocked()
}

func (s *EmbyMediaService) persistLocked() error {
	if s.repo == nil || s.task == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	index := &taskJournal{repo: s.repo, id: "emby-latest"}
	err := index.save(ctx, journalKey("snapshot"), s.task)
	if err != nil {
		s.saveErr = err
		if s.cancel != nil {
			s.cancel()
		}
	}
	return err
}

func (s *EmbyMediaService) Snapshot() *EmbyMediaTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task == nil {
		return nil
	}
	cp := *s.task
	return &cp
}

// Enqueue 请求异步刷新；已有任务运行时只合并一次后续请求。
func (s *EmbyMediaService) Enqueue(ctx context.Context) (*EmbyMediaTask, bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, false, context.Canceled
	}
	if s.task != nil && (s.task.State == "queued" || s.task.State == "running" || s.task.State == "pausing" || s.task.State == "paused" || s.task.State == "canceling") {
		s.pending = true
		cp := *s.task
		s.mu.Unlock()
		return &cp, false, nil
	}
	id := fmt.Sprintf("emby-%d", time.Now().UnixNano())
	s.task = &EmbyMediaTask{ID: id, State: "queued", UpdatedAt: time.Now().UTC()}
	s.journal = &taskJournal{repo: s.repo, id: id}
	s.saveErr = nil
	workerCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wake = make(chan struct{})
	if err := s.persistLocked(); err != nil {
		cancel()
		s.task.State, s.task.CanRetry = "failed", true
		s.task.Error = "保存任务快照失败，请检查数据库"
		s.mu.Unlock()
		return nil, false, err
	}
	cp := *s.task
	s.wg.Add(1)
	s.mu.Unlock()
	logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息任务已加入队列", "task_id", id, "queued", true)
	go func() { defer s.wg.Done(); s.run(workerCtx, id) }()
	return &cp, true, nil
}

func (s *EmbyMediaService) run(ctx context.Context, id string) {
	s.mu.Lock()
	if s.task == nil || s.task.ID != id {
		s.mu.Unlock()
		return
	}
	if s.task.State == "queued" {
		s.task.State = "running"
	}
	s.task.Phase = "scanning"
	s.task.UpdatedAt = time.Now().UTC()
	if s.journal == nil {
		s.journal = &taskJournal{repo: s.repo, id: id}
	}
	ctx = journalContext(ctx, s.journal)
	s.mu.Unlock()
	logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息开始扫描媒体库", "task_id", id)
	if checkpointErr := s.checkpoint(ctx, id); checkpointErr != nil {
		s.finish(id, checkpointErr)
		return
	}
	values, err := s.settings(ctx)
	if err != nil {
		s.finish(id, err)
		return
	}
	base := strings.TrimRight(strings.TrimSpace(values["EMBY_URL"]), "/")
	if _, err := loadTaskCheckpoint(ctx, journalKey("input", "emby-base"), &base); err != nil {
		s.finish(id, err)
		return
	}
	key := strings.TrimSpace(values["EMBY_API_KEY"])
	if base == "" || key == "" {
		s.finish(id, fmt.Errorf("未配置 Emby 地址或密钥"))
		return
	}
	if err := taskInput(ctx, "emby-base", &base); err != nil {
		s.finish(id, err)
		return
	}
	items, err := s.list(ctx, id, base, key)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.finishCanceled(id)
			return
		}
		s.finish(id, err)
		return
	}
	logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息扫描完成", "task_id", id, "total", len(items))
	s.mu.Lock()
	s.task.Total = len(items)
	s.task.Phase = "refreshing"
	s.task.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()
	logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息开始异步刷新", "task_id", id, "total", len(items), "workers", embyMediaWorkers)
	work := make(chan embyItem)
	var workers sync.WaitGroup
	for i := 0; i < embyMediaWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range work {
				if checkpointErr := s.checkpoint(ctx, id); checkpointErr != nil {
					return
				}
				attrs := item.logAttrs(id)
				done, doneErr := taskUnitDone(ctx, "emby-item", item.ID)
				if doneErr != nil {
					s.failWorker(doneErr)
					return
				}
				if done {
					s.updateTask(func(task *EmbyMediaTask) { task.Skipped++; task.Processed++ })
					continue
				}
				if !item.NeedsRefresh {
					if err := completeTaskUnit(ctx, "emby-item", item.ID); err != nil {
						s.failWorker(err)
						return
					}
					s.updateTask(func(task *EmbyMediaTask) { task.Skipped++; task.Processed++ })
					logging.Info(logging.CategoryStrmMedia, "跳过 STRM 视频信息刷新", append(attrs, "reason", "已有媒体流和时长信息")...)
					continue
				}
				logging.Info(logging.CategoryStrmMedia, "开始刷新 STRM 视频信息", attrs...)
				if probeErr := s.probe(ctx, base, key, item.ID); probeErr != nil {
					if ctx.Err() != nil {
						logging.Info(logging.CategoryStrmMedia, "STRM 视频信息刷新已取消", attrs...)
						return
					}
					s.updateTask(func(task *EmbyMediaTask) { task.Failed++; task.Error = probeErr.Error(); task.Processed++ })
					logging.Error(logging.CategoryStrmMedia, "STRM 视频信息刷新失败", append(attrs, "error", probeErr.Error())...)
				} else {
					if err := completeTaskUnit(ctx, "emby-item", item.ID); err != nil {
						s.failWorker(err)
						return
					}
					s.updateTask(func(task *EmbyMediaTask) { task.Success++; task.Processed++ })
					logging.Info(logging.CategoryStrmMedia, "STRM 视频信息刷新请求完成", attrs...)
				}
			}
		}()
	}
sendItems:
	for _, item := range items {
		if checkpointErr := s.checkpoint(ctx, id); checkpointErr != nil {
			break
		}
		select {
		case work <- item:
		case <-ctx.Done():
			break sendItems
		}
	}
	close(work)
	workers.Wait()
	s.mu.Lock()
	state := s.task.State
	if s.saveErr != nil || state == "failed" {
		state = "failed"
		s.task.Error = "保存任务断点失败，请检查数据库"
	} else if state == "canceling" || ctx.Err() != nil {
		state = s.stoppedStateLocked()
	} else {
		state = "completed"
	}
	s.task.State = state
	s.task.CanRetry = state != "completed" || s.task.Failed > 0
	s.task.UpdatedAt = time.Now().UTC()
	again := !s.closed && state == "completed" && !s.task.CanRetry && s.pending
	s.pending = false
	if again {
		s.wg.Add(1)
		s.task.ID = fmt.Sprintf("emby-%d", time.Now().UnixNano())
		s.journal = &taskJournal{repo: s.repo, id: s.task.ID}
		s.task.State = "queued"
		s.task.Phase = ""
		s.task.Processed, s.task.Total, s.task.Success, s.task.Skipped, s.task.Failed = 0, 0, 0, 0, 0
		s.task.Error = ""
		workerCtx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.wake = make(chan struct{})
		ctx = workerCtx
	}
	next := s.task.ID
	_ = s.persistLocked()
	total, success, skipped, failed := s.task.Total, s.task.Success, s.task.Skipped, s.task.Failed
	s.mu.Unlock()
	if again {
		go func() { defer s.wg.Done(); s.run(ctx, next) }()
	}
	if !again && state == "completed" {
		logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息任务已完成", "task_id", id, "total", total, "success", success, "skipped", skipped, "failed", failed)
	} else if !again && state == "canceled" {
		logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息任务已取消", "task_id", id, "processed", s.Snapshot().Processed, "total", total)
	}
}

// Close stops the owned worker before its checkpoint repository is closed.
func (s *EmbyMediaService) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// Control manages active tasks and retries terminal tasks using their original journal.
func (s *EmbyMediaService) Control(id, action string) (*EmbyMediaTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if action == "retry" {
		if s.closed || s.task == nil || s.task.ID != id || !s.task.CanRetry {
			return nil, ErrScanTaskConflict
		}
		return s.restartLocked(false)
	}
	if s.task == nil || s.task.ID != id || (s.task.State != "queued" && s.task.State != "running" && s.task.State != "pausing" && s.task.State != "paused" && s.task.State != "canceling") {
		return nil, ErrScanTaskConflict
	}
	switch action {
	case "pause":
		if s.task.State != "queued" && s.task.State != "running" {
			return nil, ErrScanTaskConflict
		}
		s.task.State = "pausing"
	case "resume":
		if s.task.State != "paused" && s.task.State != "pausing" {
			return nil, ErrScanTaskConflict
		}
		s.task.State = "running"
	case "cancel":
		if s.task.State == "canceling" {
			return nil, ErrScanTaskConflict
		}
		s.task.State = "canceling"
		if s.cancel != nil {
			s.cancel()
		}
	default:
		return nil, ErrScanTaskConflict
	}
	if s.wake != nil {
		close(s.wake)
		s.wake = make(chan struct{})
	}
	s.task.UpdatedAt = time.Now().UTC()
	cp := *s.task
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息任务控制", "task_id", id, "action", action, "state", cp.State)
	return &cp, nil
}

// restartLocked 复用原任务日志重建执行器；统计从已保存目录重新汇总，不重复累计旧计数。
func (s *EmbyMediaService) restartLocked(paused bool) (*EmbyMediaTask, error) {
	s.task.State, s.task.Error, s.task.CanRetry = "queued", "", false
	if paused {
		s.task.State = "paused"
	}
	s.task.UpdatedAt = time.Now().UTC()
	s.task.Processed, s.task.Total, s.task.Success, s.task.Skipped, s.task.Failed = 0, 0, 0, 0, 0
	s.pending, s.saveErr = false, nil
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wake = make(chan struct{})
	if err := s.persistLocked(); err != nil {
		cancel()
		s.task.State, s.task.CanRetry = "failed", true
		s.task.Error = "保存任务快照失败，请检查数据库"
		return nil, err
	}
	copy := *s.task
	s.wg.Add(1)
	go func() { defer s.wg.Done(); defer cancel(); s.run(ctx, copy.ID) }()
	return &copy, nil
}

// stoppedStateLocked 区分服务退出与用户停止，防止正常关闭被记成取消而失去自动恢复资格。
func (s *EmbyMediaService) stoppedStateLocked() string {
	if s.task.State == "canceling" || !s.closed {
		return "canceled"
	}
	if s.task.State == "paused" || s.task.State == "pausing" {
		return "paused"
	}
	return "interrupted"
}

func (s *EmbyMediaService) checkpoint(ctx context.Context, id string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if id == "" {
			return nil
		}
		s.mu.Lock()
		if s.task == nil || s.task.ID != id {
			s.mu.Unlock()
			return context.Canceled
		}
		if s.task.State == "pausing" {
			s.task.State = "paused"
			s.task.UpdatedAt = time.Now().UTC()
		}
		paused := s.task.State == "paused"
		wake := s.wake
		s.mu.Unlock()
		if !paused {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

func (s *EmbyMediaService) updateTask(update func(*EmbyMediaTask)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task == nil {
		return
	}
	update(s.task)
	s.task.UpdatedAt = time.Now().UTC()
	_ = s.persistLocked()
}

// failWorker cancels sibling workers; only the coordinator publishes the terminal state.
func (s *EmbyMediaService) failWorker(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveErr = err
	if s.cancel != nil {
		s.cancel()
	}
}
func (s *EmbyMediaService) finish(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if s.task != nil && s.task.ID == id {
			s.task.CanRetry = true
			_ = s.persistLocked()
		}
	}()
	if s.task != nil && s.task.ID == id {
		if s.saveErr == nil && (s.closed || errors.Is(err, context.Canceled) || s.task.State == "canceling") {
			s.task.State = s.stoppedStateLocked()
			s.task.Error = ""
			s.task.UpdatedAt = time.Now().UTC()
			logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息任务已停止", "task_id", id, "state", s.task.State, "processed", s.task.Processed, "total", s.task.Total)
			return
		}
		s.task.State = "failed"
		s.task.Error = err.Error()
		s.task.UpdatedAt = time.Now().UTC()
		logging.Error(logging.CategoryStrmMedia, "刷新 STRM 视频信息任务失败", "task_id", id, "error", err.Error())
	}
}

func (s *EmbyMediaService) finishCanceled(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task == nil || s.task.ID != id {
		return
	}
	s.task.State = s.stoppedStateLocked()
	s.task.CanRetry = true
	s.task.Error = ""
	s.task.UpdatedAt = time.Now().UTC()
	_ = s.persistLocked()
	logging.Info(logging.CategoryStrmMedia, "刷新 STRM 视频信息任务已停止", "task_id", id, "state", s.task.State, "processed", s.task.Processed, "total", s.task.Total)
}

type embyItem struct {
	ID           string `json:"Id"`
	Path         string `json:"Path"`
	NeedsRefresh bool
	MediaSources []struct {
		Path         string            `json:"Path"`
		MediaStreams []json.RawMessage `json:"MediaStreams"`
		RunTimeTicks int64             `json:"RunTimeTicks"`
	} `json:"MediaSources,omitempty"`
}

// logAttrs 用文件名识别单个视频，复用番号规则；兼容 Emby 返回的 Windows/Linux 路径，
// 不记录完整目录或 STRM 播放地址，无法识别番号时保留文件名而不编造番号。
func (item embyItem) logAttrs(taskID string) []any {
	filename := path.Base(strings.ReplaceAll(item.Path, "\\", "/"))
	attrs := make([]any, 0, 8)
	if code := domain.ExtractCode(filename); code != "" {
		attrs = append(attrs, "code", code)
	}
	return append(attrs, "filename", filename, "task_id", taskID)
}

func (s *EmbyMediaService) list(ctx context.Context, id, base, key string) ([]embyItem, error) {
	out := make([]embyItem, 0)
	err := s.streamItems(ctx, id, base, key, func(item embyItem) { out = append(out, item) })
	return out, err
}

// streamItems 按 StartIndex 自动读取 Emby 的全部媒体页，并在解出每个 STRM 后立即回调。
// 不依赖固定总上限；回调由任务负责更新动态总数和处理进度。
func (s *EmbyMediaService) streamItems(ctx context.Context, id, base, key string, onItem func(embyItem)) error {
	for start := 0; ; start += embyMediaPageSize {
		if err := s.checkpoint(ctx, id); err != nil {
			return err
		}
		var body struct {
			Items            []embyItem `json:"Items"`
			TotalRecordCount int        `json:"TotalRecordCount"`
		}
		pageKey := journalKey("emby-page", fmt.Sprint(start))
		found, err := loadTaskCheckpoint(ctx, pageKey, &body)
		if err != nil {
			return err
		}
		if !found {
			u := base + "/emby/Items?Recursive=true&IncludeItemTypes=Movie,Episode&Fields=Path,MediaSources&Limit=" + fmt.Sprint(embyMediaPageSize) + "&StartIndex=" + fmt.Sprint(start) + "&api_key=" + url.QueryEscape(key)
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			res, err := s.http.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("获取 Emby 媒体列表失败，请检查服务连接")
			}
			if res.StatusCode/100 != 2 {
				res.Body.Close()
				return fmt.Errorf("Emby 媒体列表返回状态码 %d", res.StatusCode)
			}
			err = json.NewDecoder(res.Body).Decode(&body)
			res.Body.Close()
			if err != nil {
				return fmt.Errorf("解析 Emby 媒体列表失败")
			}
			for index, item := range body.Items {
				itemPath := item.Path
				if itemPath == "" && len(item.MediaSources) > 0 {
					itemPath = item.MediaSources[0].Path
				}
				missing := len(item.MediaSources) == 0
				for _, source := range item.MediaSources {
					if len(source.MediaStreams) == 0 || source.RunTimeTicks == 0 {
						missing = true
					}
				}
				body.Items[index] = embyItem{}
				if strings.EqualFold(filepath.Ext(itemPath), ".strm") {
					body.Items[index] = embyItem{ID: item.ID, Path: path.Base(strings.ReplaceAll(itemPath, "\\", "/")), NeedsRefresh: missing}
				}
			}
			if err = saveTaskCheckpoint(ctx, pageKey, body); err != nil {
				return err
			}
		}
		for _, it := range body.Items {
			if it.ID == "" {
				continue
			}
			onItem(it)
		}
		if len(body.Items) == 0 || (body.TotalRecordCount > 0 && start+len(body.Items) >= body.TotalRecordCount) || (body.TotalRecordCount == 0 && len(body.Items) < embyMediaPageSize) {
			return nil
		}
	}
}
func (s *EmbyMediaService) probe(ctx context.Context, base, key, id string) error {
	u := base + "/emby/Items/" + url.PathEscape(id) + "/PlaybackInfo?api_key=" + url.QueryEscape(key)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(`{"IsPlayback":true}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("刷新媒体信息请求失败")
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("Emby 刷新媒体信息返回状态码 %d", res.StatusCode)
	}
	return nil
}

// Run 按设置间隔检查；设置未开启时保持空闲。
func (s *EmbyMediaService) Run(ctx context.Context) {
	for {
		values, err := s.settings(ctx)
		interval := 60 * time.Minute
		enabled := false
		if err == nil {
			enabled = values[strmEmbyMediaEnableSettingKey] == "true"
			if n, e := time.ParseDuration(strings.TrimSpace(values[strmEmbyMediaIntervalSettingKey]) + "m"); e == nil && n > 0 {
				interval = n
			}
		}
		if enabled {
			select {
			case <-time.After(interval):
				_, _, _ = s.Enqueue(ctx)
			case <-ctx.Done():
				return
			}
		} else {
			select {
			case <-time.After(30 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}
