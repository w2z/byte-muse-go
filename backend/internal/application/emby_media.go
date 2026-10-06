package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EmbyMediaTask 是 STRM 媒体信息预热任务的可观测快照。
type EmbyMediaTask struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Processed int       `json:"processed"`
	Total     int       `json:"total"`
	Success   int       `json:"success"`
	Skipped   int       `json:"skipped"`
	Failed    int       `json:"failed"`
	Error     string    `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EmbyMediaService 串行预热 Emby 中缺少媒体信息的 STRM 项目；重复触发合并为下一轮。
type EmbyMediaService struct {
	settings func(context.Context) (map[string]string, error)
	http     *http.Client
	mu       sync.Mutex
	task     *EmbyMediaTask
	pending  bool
}

func NewEmbyMediaService(settings func(context.Context) (map[string]string, error)) *EmbyMediaService {
	return &EmbyMediaService{settings: settings, http: &http.Client{Timeout: 2 * time.Minute}}
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
	if s.task != nil && (s.task.State == "queued" || s.task.State == "running") {
		s.pending = true
		cp := *s.task
		s.mu.Unlock()
		return &cp, false, nil
	}
	id := fmt.Sprintf("emby-%d", time.Now().UnixNano())
	s.task = &EmbyMediaTask{ID: id, State: "queued", UpdatedAt: time.Now().UTC()}
	cp := *s.task
	s.mu.Unlock()
	go s.run(context.Background(), id)
	return &cp, true, nil
}

func (s *EmbyMediaService) run(ctx context.Context, id string) {
	s.mu.Lock()
	if s.task == nil || s.task.ID != id {
		s.mu.Unlock()
		return
	}
	s.task.State = "running"
	s.task.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()
	values, err := s.settings(ctx)
	if err != nil {
		s.finish(id, err)
		return
	}
	base := strings.TrimRight(strings.TrimSpace(values["EMBY_URL"]), "/")
	key := strings.TrimSpace(values["EMBY_API_KEY"])
	if base == "" || key == "" {
		s.finish(id, fmt.Errorf("未配置 Emby 地址或密钥"))
		return
	}
	err = s.streamItems(ctx, base, key, func(item embyItem) {
		s.mu.Lock()
		s.task.Total++
		s.task.UpdatedAt = time.Now().UTC()
		s.mu.Unlock()
		if !item.NeedsRefresh {
			s.mu.Lock()
			s.task.Skipped++
			s.task.Processed++
			s.task.UpdatedAt = time.Now().UTC()
			s.mu.Unlock()
			return
		}
		if err := s.probe(ctx, base, key, item.ID); err != nil {
			s.mu.Lock()
			s.task.Failed++
			s.task.Error = err.Error()
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			s.task.Success++
			s.mu.Unlock()
		}
		s.mu.Lock()
		s.task.Processed++
		s.task.UpdatedAt = time.Now().UTC()
		s.mu.Unlock()
		time.Sleep(2 * time.Second)
	})
	if err != nil {
		s.finish(id, err)
		return
	}
	s.mu.Lock()
	s.task.State = "completed"
	s.task.UpdatedAt = time.Now().UTC()
	again := s.pending
	s.pending = false
	if again {
		s.task.State = "queued"
		s.task.Processed, s.task.Total, s.task.Success, s.task.Skipped, s.task.Failed = 0, 0, 0, 0, 0
		s.task.Error = ""
	}
	next := s.task.ID
	s.mu.Unlock()
	if again {
		go s.run(ctx, next)
	}
}
func (s *EmbyMediaService) finish(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task != nil && s.task.ID == id {
		s.task.State = "failed"
		s.task.Error = err.Error()
		s.task.UpdatedAt = time.Now().UTC()
	}
}

type embyItem struct {
	ID           string `json:"Id"`
	Path         string `json:"Path"`
	NeedsRefresh bool
	MediaSources []struct {
		Path         string            `json:"Path"`
		MediaStreams []json.RawMessage `json:"MediaStreams"`
		RunTimeTicks int64             `json:"RunTimeTicks"`
	} `json:"MediaSources"`
}

func (s *EmbyMediaService) list(ctx context.Context, base, key string) ([]embyItem, error) {
	out := make([]embyItem, 0)
	err := s.streamItems(ctx, base, key, func(item embyItem) { out = append(out, item) })
	return out, err
}

// streamItems 单次读取 Emby 的完整 Items 数组，并在解出每个 STRM 后立即回调。
// Limit=0 避免分页；回调由任务负责更新动态总数和处理进度。
func (s *EmbyMediaService) streamItems(ctx context.Context, base, key string, onItem func(embyItem)) error {
	// Emby 将 Limit=0 解释为返回 0 条，不能用它表示“不限制”。
	// 使用一次足够大的上限读取完整媒体集，避免分页导致任务只看到第一页。
	u := base + "/emby/Items?Recursive=true&IncludeItemTypes=Movie,Episode&Fields=Path,MediaSources&Limit=10000&api_key=" + url.QueryEscape(key)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	res, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("获取 Emby 媒体列表失败: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("Emby 媒体列表返回状态码 %d", res.StatusCode)
	}
	decoder := json.NewDecoder(res.Body)
	root, err := decoder.Token()
	if err != nil || root != json.Delim('{') {
		return fmt.Errorf("解析 Emby 媒体列表失败")
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("解析 Emby 媒体列表失败")
		}
		keyToken, ok := token.(string)
		if !ok {
			continue
		}
		if keyToken != "Items" {
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return fmt.Errorf("解析 Emby 媒体列表失败")
			}
			continue
		}
		start, err := decoder.Token()
		if err != nil || start != json.Delim('[') {
			return fmt.Errorf("解析 Emby 媒体列表失败")
		}
		for decoder.More() {
			var it embyItem
			if err := decoder.Decode(&it); err != nil {
				return fmt.Errorf("解析 Emby 媒体列表失败")
			}
			itemPath := it.Path
			if itemPath == "" && len(it.MediaSources) > 0 {
				itemPath = it.MediaSources[0].Path
			}
			if strings.ToLower(filepath.Ext(itemPath)) != ".strm" || it.ID == "" {
				continue
			}
			missing := len(it.MediaSources) == 0
			for _, src := range it.MediaSources {
				if len(src.MediaStreams) == 0 || src.RunTimeTicks == 0 {
					missing = true
				}
			}
			it.NeedsRefresh = missing
			onItem(it)
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("解析 Emby 媒体列表失败")
		}
	}
	return nil
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
