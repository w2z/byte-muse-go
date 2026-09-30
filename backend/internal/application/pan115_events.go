package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

const pan115EventCursorKey = "INTERNAL_PAN115_EVENT_CURSOR"

// EventAccountID 返回本地已绑定账号标识，避免每轮监听重复请求账号资料。
func (s *Pan115Service) EventAccountID(ctx context.Context) (string, error) {
	account, found, err := s.accounts.Load(ctx)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrPan115NotLinked
	}
	return account.UserID, nil
}

// LifeEvents 复用已存在的 115 客户端及限流器，Cookie 仅用于生活事件接口。
func (s *Pan115Service) LifeEvents(ctx context.Context, cookie string, offset, limit int) (pan115.LifePage, error) {
	source, ok := s.client.(pan115LifeSource)
	if !ok {
		return pan115.LifePage{}, fmt.Errorf("115 客户端不支持生活事件")
	}
	return source.LifeEvents(ctx, cookie, offset, limit)
}

// pan115LifeSource 只读取生活事件；不会更改远端文件或生活记录开关。
type pan115LifeSource interface {
	LifeEvents(context.Context, string, int, int) (pan115.LifePage, error)
}

// pan115EventCursor 只在 STRM 全部成功后前移；账号切换重新建立基线。
type pan115EventCursor struct {
	UserID string
	ID     int64
	Since  int64
}

// Pan115EventService 合并生活事件触发 STRM 更新，串行执行并持久化成功游标。
// 只增加或更新本地文件，不删除源文件或旧 STRM；失败批次可安全重试。
type Pan115EventService struct {
	source     pan115LifeSource
	strm       *StrmService
	repository ports.SettingsRepository
	settings   func(context.Context) (map[string]string, error)
	account    func(context.Context) (string, error)
	mu         sync.Mutex
}

// NewPan115EventService 组装事件消费者；内部游标不进入用户可写设置白名单。
func NewPan115EventService(source pan115LifeSource, strm *StrmService, repository ports.SettingsRepository, settings func(context.Context) (map[string]string, error), account func(context.Context) (string, error)) *Pan115EventService {
	return &Pan115EventService{source: source, strm: strm, repository: repository, settings: settings, account: account}
}

// Run 随 serve 启停，每 30 秒读取最新设置；失败采用一分钟退避，停用无需重启。
func (s *Pan115EventService) Run(ctx context.Context) {
	lastError := ""
	for ctx.Err() == nil {
		err := s.Poll(ctx)
		delay := 30 * time.Second
		if err != nil && ctx.Err() == nil {
			delay = time.Minute
			if err.Error() != lastError {
				logging.Default.Error(logging.CategoryMedia, "115 事件监听失败", "error", err.Error())
				lastError = err.Error()
			}
		} else if err == nil && lastError != "" {
			logging.Default.Info(logging.CategoryMedia, "115 事件监听已恢复")
			lastError = ""
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Poll 消费一批倒序事件，所有分页成功才处理；扫描失败或游标落库失败保留旧游标。
func (s *Pan115EventService) Poll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if values["PAN115_EVENT_ENABLE"] != "true" {
		return nil
	}
	cookie := strings.TrimSpace(values["PAN115_COOKIE"])
	userID := pan115.LifeCookieUserID(cookie)
	if userID == "" {
		return fmt.Errorf("请配置有效的 115 事件 Cookie（UID、CID、SEID）")
	}
	bound, err := s.account(ctx)
	if err != nil {
		return err
	}
	if bound != userID {
		return fmt.Errorf("115 事件 Cookie 与 OpenAPI 绑定账号不一致")
	}
	mappings, err := parseStrmMappings(values[strmPathsSettingKey])
	if err != nil {
		return err
	}
	active := make([]domain.StrmMapping, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping.Kind == domain.StrmKindPan115 {
			active = append(active, mapping)
		}
	}
	if len(active) == 0 {
		return fmt.Errorf("115 事件监听需要至少一条 115 STRM 映射")
	}
	base := strings.TrimRight(strings.TrimSpace(values[strmPlayBaseSettingKey]), "/")
	if base == "" {
		return fmt.Errorf("115 事件监听需要配置 STRM 文件播放地址")
	}
	items, err := s.repository.List(ctx)
	if err != nil {
		return err
	}
	cursor := pan115EventCursor{}
	for _, item := range items {
		if item.Key == pan115EventCursorKey {
			if err := json.Unmarshal([]byte(item.Value), &cursor); err != nil {
				return fmt.Errorf("115 事件游标损坏，停止消费")
			}
		}
	}
	initial := cursor.UserID != userID
	if initial {
		cursor = pan115EventCursor{UserID: userID, Since: time.Now().Unix()}
	}
	next := cursor
	dirty := false
	reached := false
	previousID := int64(0)
	for offset := 0; offset < 10000; {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := s.source.LifeEvents(ctx, cookie, offset, 64)
		if err != nil {
			return err
		}
		if len(page.Events) == 0 {
			if offset < page.Total {
				return fmt.Errorf("115 事件分页意外为空，保留游标")
			}
			reached = true
			break
		}
		for _, event := range page.Events {
			if previousID != 0 && event.ID > previousID {
				return fmt.Errorf("115 事件顺序发生变化，稍后重试")
			}
			previousID = event.ID
			if event.ID > next.ID {
				next.ID = event.ID
			}
			if initial {
				continue
			}
			if event.ID <= cursor.ID || (cursor.ID == 0 && event.UpdatedAt < cursor.Since) {
				reached = true
				break
			}
			switch event.Type {
			case 1, 2, 5, 6, 14, 17, 18, 20, 23, 24:
				dirty = true
			}
		}
		offset += len(page.Events)
		if !initial && !reached && offset >= 10000 {
			break
		}
		if initial || reached || offset >= page.Total {
			reached = true
			break
		}
	}
	// 超过 Web 最近一万条窗口时执行完整映射扫描以补偿遗漏；成功后才能前移。
	if !reached {
		dirty = true
		logging.Default.Error(logging.CategoryMedia, "115 事件超出历史窗口，执行映射补偿扫描")
	}
	if dirty {
		result, err := s.strm.scanPan115EventMappings(ctx, active, base, values)
		if err != nil {
			return err
		}
		logging.Default.Info(logging.CategoryMedia, "115 事件已同步 STRM", "files", result.Files, "created", result.Created)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if initial || next.ID != cursor.ID {
		encoded, err := json.Marshal(next)
		if err != nil {
			return err
		}
		return s.repository.Upsert(ctx, []ports.StoredSetting{{Key: pan115EventCursorKey, Value: string(encoded)}})
	}
	return nil
}

// scanPan115EventMappings 复用手动生成的映射与过滤规则，任意目录失败都阻止事件确认。
func (s *StrmService) scanPan115EventMappings(ctx context.Context, mappings []domain.StrmMapping, base string, values map[string]string) (domain.StrmScanResult, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	result := domain.StrmScanResult{}
	for _, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		files, collectErr := s.collectMappingFiles(ctx, mapping)
		entry := s.scanMapping(ctx, s.root, mapping, base, files, collectErr, func() {})
		if entry.Message != "" || entry.Failed > 0 {
			return result, fmt.Errorf("115 事件 STRM 映射 %s 失败：%s（失败文件 %d）", mapping.LocalPath, entry.Message, entry.Failed)
		}
		result.Files += entry.Files
		result.Created += entry.Created
	}
	result.Emby = s.refreshEmby(ctx, values)
	if result.Emby.Attempted && !result.Emby.Refreshed {
		// 网络错误可能包含 api_key 查询串，后台持久化日志只记录固定原因。
		return result, fmt.Errorf("STRM 已写入，但 Emby 刷新失败，请检查 Emby 配置与连接")
	}
	return result, nil
}
