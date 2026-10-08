// Package application contains entry-point-neutral ByteMuse business workflows.
package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

var (
	// ErrInvalidPagination reports page values outside the public API contract.
	ErrInvalidPagination = errors.New("invalid pagination")
	// ErrInvalidVideoType 表示影片类型不在允许的筛选范围内。
	ErrInvalidVideoType = errors.New("invalid video type")
	// ErrInvalidSubscription reports a malformed subscription command.
	ErrInvalidSubscription = errors.New("invalid subscription")
	// ErrInvalidDownloadFilter reports unsupported status or time-range filters.
	ErrInvalidDownloadFilter = errors.New("invalid download filter")
)

// Page attaches normalized request pagination to a result collection.
type Page[T any] struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
	Items    []T `json:"items"`
}

// CatalogService owns media list and detail workflows.
type CatalogService struct {
	repository ports.MediaRepository
	translator TranslationClient
	writer     ports.MediaTranslationWriter
}

// NewCatalogService builds a media application service.
func NewCatalogService(repository ports.MediaRepository) *CatalogService {
	return &CatalogService{repository: repository}
}

// NewCatalogServiceWithTranslation builds a catalog service that lazily translates missing titles.
func NewCatalogServiceWithTranslation(repository ports.MediaRepository, translator TranslationClient, writer ports.MediaTranslationWriter) *CatalogService {
	return &CatalogService{repository: repository, translator: translator, writer: writer}
}

// List returns one validated page while preserving the repository's total count.
func (s *CatalogService) List(ctx context.Context, page, pageSize int, query ports.MediaListQuery) (Page[domain.Media], error) {
	if err := validateMediaFilters(&query); err != nil {
		return Page[domain.Media]{}, err
	}
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Media]{}, err
	}
	query.Limit = pageSize
	query.Offset = (page - 1) * pageSize
	result, err := s.repository.List(ctx, query)
	if err != nil {
		return Page[domain.Media]{}, fmt.Errorf("list media: %w", err)
	}
	items := nonNil(result.Items)
	s.applyTranslations(ctx, items)
	logging.Info(logging.CategoryCollection, "影片列表查询完成", "count", len(items))
	return Page[domain.Media]{Page: page, PageSize: pageSize, Total: result.Total, Items: items}, nil
}

// Get returns a media detail by stable identity.
func (s *CatalogService) Get(ctx context.Context, id string) (domain.Media, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return domain.Media{}, fmt.Errorf("get media: %w", err)
	}
	item = s.applyTranslation(ctx, item)
	logging.Info(logging.CategoryCollection, "影片详情查询完成", "code", item.Code)
	return item, nil
}

func (s *CatalogService) applyTranslations(ctx context.Context, items []domain.Media) {
	for i := range items {
		items[i] = s.applyTranslation(ctx, items[i])
	}
}

func (s *CatalogService) applyTranslation(ctx context.Context, item domain.Media) domain.Media {
	return applyMediaTranslation(ctx, item, s.translator, s.writer)
}

// applyMediaTranslation centralizes lazy title translation for all media read models.
func applyMediaTranslation(ctx context.Context, item domain.Media, translator TranslationClient, writer ports.MediaTranslationWriter) domain.Media {
	if translator == nil || item.TranslatedTitle != nil || strings.TrimSpace(item.Title) == "" {
		return item
	}
	translated, err := translator.Translate(ctx, TranslationRequest{Text: item.Title, TargetLanguage: "ZH-CN"})
	if err != nil {
		return item
	}
	value, ok := SanitizeTranslatedTitle(item.Title, translated)
	if !ok {
		return item
	}
	item.TranslatedTitle = &value
	if writer != nil {
		_ = writer.UpdateTranslatedTitle(ctx, item.ID, value)
	}
	return item
}

// CreateSubscriptionCommand carries the API idempotency key and subscription intent.
type CreateSubscriptionCommand struct {
	IdempotencyKey string
	MediaID        string
	Mode           domain.SubscriptionMode
	Filter         map[string]any
	// Label 是订阅对象的用户可读标识（通常是番号），只用于通知文案；为空时回退到媒体 ID。
	Label string
}

// UpdateSubscriptionCommand carries editable rules and the caller's optimistic version.
type UpdateSubscriptionCommand struct {
	ID              string
	Mode            domain.SubscriptionMode
	Filter          map[string]any
	ExpectedVersion int
}

// SubscriptionService owns idempotent subscription state transitions.
type SubscriptionService struct {
	repository ports.SubscriptionRepository
	notifier   Notifier
}

// NewSubscriptionService builds a subscription application service.
func NewSubscriptionService(repository ports.SubscriptionRepository) *SubscriptionService {
	return &SubscriptionService{repository: repository}
}

// SetNotifier 注入业务通知出口；未注入时订阅流程不发送任何通知。
func (s *SubscriptionService) SetNotifier(notifier Notifier) { s.notifier = notifier }

// subscriptionLogAttrs 组装订阅日志属性：订阅日志一律用番号标识对象，番号不可用时省略该字段。
// 订阅 ID 是不对用户暴露的内部标识，写进日志无法与任何界面元素对应，因此不再出现在日志里。
func subscriptionLogAttrs(item domain.Subscription, extra ...any) []any {
	attrs := make([]any, 0, 2+len(extra))
	if item.Media != nil {
		if code := strings.TrimSpace(item.Media.Code); code != "" {
			attrs = append(attrs, "code", code)
		}
	}
	return append(attrs, extra...)
}

// Create atomically creates a subscription or replays the prior result for the same key.
func (s *SubscriptionService) Create(ctx context.Context, command CreateSubscriptionCommand) (domain.Subscription, bool, error) {
	if len(strings.TrimSpace(command.IdempotencyKey)) < 8 || strings.TrimSpace(command.MediaID) == "" || !validMode(command.Mode) {
		return domain.Subscription{}, false, ErrInvalidSubscription
	}
	if command.Filter == nil {
		command.Filter = map[string]any{}
	}
	item, created, err := s.repository.Create(ctx, ports.CreateSubscription{
		IdempotencyKey: command.IdempotencyKey,
		MediaID:        command.MediaID,
		Mode:           command.Mode,
		Filter:         command.Filter,
	})
	if err != nil {
		s.notifySubscribeFailed(ctx, command, err)
		logging.Error(logging.CategorySubscription, "订阅保存失败", "error", err.Error())
		return domain.Subscription{}, false, fmt.Errorf("create subscription: %w", err)
	}
	logging.Info(logging.CategorySubscription, "订阅已保存", subscriptionLogAttrs(item, "created", created)...)
	// 幂等重放不算新订阅，只有真正新建时才通知，避免同一请求重复推送。
	if created {
		s.notifySubscribe(ctx, command, item)
	}
	return item, created, nil
}

// notifySubscribe 通知订阅创建成功；文本优先使用番号与标题，配图使用影片封面，都不含任何凭据。
func (s *SubscriptionService) notifySubscribe(ctx context.Context, command CreateSubscriptionCommand, item domain.Subscription) {
	if s.notifier == nil {
		return
	}
	label, title, cover := strings.TrimSpace(command.Label), "", ""
	if item.Media != nil {
		if mediaCode := strings.TrimSpace(item.Media.Code); mediaCode != "" {
			label = mediaCode
		}
		title = MediaDisplayTitle(*item.Media)
		cover = MediaCover(*item.Media)
	}
	// 番号取不到时不回退到内部 media_id：推送里出现用户无法对应的标识，比没有标识更糟。
	s.notifier.Notify(ctx, NotificationSubscribe, NewNotificationMessage(label, "已加入订阅列表", "", "", "", title, cover))
}

// MediaDisplayTitle 返回影片的展示标题，优先使用译文；译名为空时回退原标题。
func MediaDisplayTitle(item domain.Media) string {
	if item.TranslatedTitle != nil {
		if translated := strings.TrimSpace(*item.TranslatedTitle); translated != "" {
			return translated
		}
	}
	return strings.TrimSpace(item.Title)
}

// MediaCover 返回影片的推送封面：优先横幅图，缺失时回退海报图，都没有则返回空串。
// 取值规则与数据库的 mediaCoverColumn 保持一致，推送配图只有这一处口径。
func MediaCover(item domain.Media) string {
	if item.BannerURL != nil {
		if banner := strings.TrimSpace(*item.BannerURL); banner != "" {
			return banner
		}
	}
	if item.PosterURL != nil {
		return strings.TrimSpace(*item.PosterURL)
	}
	return ""
}

// notifySubscribeFailed 通知订阅创建失败；原因直接使用仓储错误，便于定位番号不在库等具体问题。
func (s *SubscriptionService) notifySubscribeFailed(ctx context.Context, command CreateSubscriptionCommand, cause error) {
	if s.notifier == nil {
		return
	}
	label := strings.TrimSpace(command.Label)
	s.notifier.Notify(ctx, NotificationSubscribeFailed, NewNotificationMessage(label, "订阅失败", "", "", "", "原因："+cause.Error(), ""))
}

// Cancel removes an active subscription; a repeated call returns ErrSubscriptionNotFound.
func (s *SubscriptionService) Cancel(ctx context.Context, id string) (domain.Subscription, error) {
	item, _, err := s.repository.Cancel(ctx, id)
	if err != nil {
		logging.Error(logging.CategorySubscription, "取消订阅失败", "error", err.Error())
		return domain.Subscription{}, fmt.Errorf("cancel subscription: %w", err)
	}
	logging.Info(logging.CategorySubscription, "订阅已取消", subscriptionLogAttrs(item)...)
	return item, nil
}

// Update replaces editable rules on an active subscription.
func (s *SubscriptionService) Update(ctx context.Context, command UpdateSubscriptionCommand) (domain.Subscription, error) {
	if strings.TrimSpace(command.ID) == "" || command.ExpectedVersion < 1 || !validMode(command.Mode) {
		return domain.Subscription{}, ErrInvalidSubscription
	}
	if command.Filter == nil {
		command.Filter = map[string]any{}
	}
	item, err := s.repository.Update(ctx, ports.UpdateSubscription{ID: command.ID, Mode: command.Mode, Filter: command.Filter, ExpectedVersion: command.ExpectedVersion})
	if err != nil {
		logging.Error(logging.CategorySubscription, "编辑订阅失败", "error", err.Error())
		return domain.Subscription{}, fmt.Errorf("update subscription: %w", err)
	}
	logging.Info(logging.CategorySubscription, "订阅已编辑", subscriptionLogAttrs(item)...)
	return item, nil
}

// List returns one validated page of subscriptions.
func (s *SubscriptionService) List(ctx context.Context, page, pageSize int, status domain.SubscriptionStatus) (Page[domain.Subscription], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Subscription]{}, err
	}
	result, err := s.repository.List(ctx, ports.SubscriptionListQuery{Limit: pageSize, Offset: (page - 1) * pageSize, Status: status})
	if err != nil {
		return Page[domain.Subscription]{}, fmt.Errorf("list subscriptions: %w", err)
	}
	logging.Info(logging.CategorySubscription, "订阅列表查询完成", "count", len(result.Items), "codes", subscriptionCodes(result.Items))
	return Page[domain.Subscription]{Page: page, PageSize: pageSize, Total: result.Total, Items: nonNil(result.Items)}, nil
}

// subscriptionCodes 汇总本次订阅查询命中影片的番号，供日志单行展示；媒体缺失或番号为空时跳过。
func subscriptionCodes(items []domain.Subscription) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		if item.Media == nil {
			continue
		}
		if code := strings.TrimSpace(item.Media.Code); code != "" {
			codes = append(codes, code)
		}
	}
	return codes
}

// DownloadService owns download task queries.
type DownloadService struct {
	repository ports.DownloadRepository
	controls   ports.DownloadControlRepository
	clients    func(context.Context) (map[string]ports.DownloadController, error)
}

// NewDownloadService builds a download task application service.
func NewDownloadService(repository ports.DownloadRepository) *DownloadService {
	return &DownloadService{repository: repository}
}

// List returns one validated page of download tasks.
func (s *DownloadService) List(ctx context.Context, page, pageSize int, status domain.DownloadStatus) (Page[domain.DownloadTask], error) {
	return s.ListFiltered(ctx, page, pageSize, ports.DownloadListQuery{Status: status})
}

// ListFiltered validates and applies server-side download state and time filters before pagination.
func (s *DownloadService) ListFiltered(ctx context.Context, page, pageSize int, query ports.DownloadListQuery) (Page[domain.DownloadTask], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.DownloadTask]{}, err
	}
	if query.TransferStatus != "" && query.TransferStatus != "downloading" && query.TransferStatus != "paused" && query.TransferStatus != "stopped" && query.TransferStatus != "failed" && query.TransferStatus != "completed" {
		return Page[domain.DownloadTask]{}, ErrInvalidDownloadFilter
	}
	if query.AddedFrom != nil && query.AddedTo != nil && !query.AddedFrom.Before(*query.AddedTo) || query.CompletedFrom != nil && query.CompletedTo != nil && !query.CompletedFrom.Before(*query.CompletedTo) {
		return Page[domain.DownloadTask]{}, ErrInvalidDownloadFilter
	}
	query.Limit, query.Offset = pageSize, (page-1)*pageSize
	result, err := s.repository.List(ctx, query)
	if err != nil {
		return Page[domain.DownloadTask]{}, fmt.Errorf("list downloads: %w", err)
	}
	s.decorateActions(ctx, result.Items)
	logging.Info(logging.CategoryDownload, "下载任务查询完成", "count", len(result.Items), "codes", downloadCodes(result.Items))
	return Page[domain.DownloadTask]{Page: page, PageSize: pageSize, Total: result.Total, Items: nonNil(result.Items)}, nil
}

// LatestForMedia 返回该影片最近的一条下载任务，供渠道卡片按最新状态给出可执行按钮。
// 没有任务时返回 nil；可用操作沿用列表的下载器能力判定，卡片不重复实现状态规则。
func (s *DownloadService) LatestForMedia(ctx context.Context, mediaID string) (*domain.DownloadTask, error) {
	if strings.TrimSpace(mediaID) == "" {
		return nil, nil
	}
	page, err := s.repository.List(ctx, ports.DownloadListQuery{MediaID: mediaID, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("latest download for media: %w", err)
	}
	if len(page.Items) == 0 {
		return nil, nil
	}
	items := []domain.DownloadTask{page.Items[0]}
	s.decorateActions(ctx, items)
	return &items[0], nil
}

// downloadCodes 汇总本次下载任务查询命中影片的番号，供日志单行展示；缺少关联影片或番号为空时跳过。
func downloadCodes(items []domain.DownloadTask) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		if item.Code == nil {
			continue
		}
		if code := strings.TrimSpace(*item.Code); code != "" {
			codes = append(codes, code)
		}
	}
	return codes
}

// DashboardService aggregates independent repository status dimensions.
type DashboardService struct {
	media         ports.MediaRepository
	subscriptions ports.SubscriptionRepository
	downloads     ports.DownloadRepository
}

// NewDashboardService builds the dashboard aggregation workflow.
func NewDashboardService(media ports.MediaRepository, subscriptions ports.SubscriptionRepository, downloads ports.DownloadRepository) *DashboardService {
	return &DashboardService{media: media, subscriptions: subscriptions, downloads: downloads}
}

// Get reads repository totals using the status filters defined by the public contract.
func (s *DashboardService) Get(ctx context.Context) (domain.Dashboard, error) {
	media, err := s.media.List(ctx, ports.MediaListQuery{Limit: 1})
	if err != nil {
		return domain.Dashboard{}, fmt.Errorf("count media: %w", err)
	}
	subscriptions, err := s.subscriptions.List(ctx, ports.SubscriptionListQuery{Limit: 1, Status: domain.SubscriptionStatusActive})
	if err != nil {
		return domain.Dashboard{}, fmt.Errorf("count active subscriptions: %w", err)
	}
	downloads, err := s.downloads.List(ctx, ports.DownloadListQuery{Limit: 1, Status: domain.DownloadStatusCompleted})
	if err != nil {
		return domain.Dashboard{}, fmt.Errorf("count completed downloads: %w", err)
	}
	return domain.Dashboard{
		ActiveSubscriptions: subscriptions.Total,
		CompletedDownloads:  downloads.Total,
		MediaCount:          media.Total,
	}, nil
}

func validatePagination(page, pageSize int) error {
	if page < 1 || pageSize < 1 || pageSize > ports.MaxPageSize {
		return ErrInvalidPagination
	}
	return nil
}

func validMode(mode domain.SubscriptionMode) bool {
	return mode == domain.SubscriptionModeStrict || mode == domain.SubscriptionModePreload
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// validateMediaFilters 统一所有影片视图的类型校验，unknown 表示未分类。
func validateMediaFilters(query *ports.MediaListQuery) error {
	query.VideoType = strings.TrimSpace(query.VideoType)
	if query.VideoType != "" && query.VideoType != "unknown" && !domain.ValidVideoType(query.VideoType) {
		return ErrInvalidVideoType
	}
	return nil
}
