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
	// ErrInvalidSubscription reports a malformed subscription command.
	ErrInvalidSubscription = errors.New("invalid subscription")
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
func (s *CatalogService) List(ctx context.Context, page, pageSize int) (Page[domain.Media], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Media]{}, err
	}
	result, err := s.repository.List(ctx, ports.MediaListQuery{Limit: pageSize, Offset: (page - 1) * pageSize})
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
	logging.Info(logging.CategoryCollection, "影片详情查询完成", "media_id", id)
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
	if err != nil || strings.TrimSpace(translated) == "" {
		return item
	}
	value := strings.TrimSpace(translated)
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
}

// UpdateSubscriptionCommand carries editable rules and the caller's optimistic version.
type UpdateSubscriptionCommand struct {
	ID              string
	Mode            domain.SubscriptionMode
	Filter          map[string]any
	ExpectedVersion int
}

// SubscriptionService owns idempotent subscription state transitions.
type SubscriptionService struct{ repository ports.SubscriptionRepository }

// NewSubscriptionService builds a subscription application service.
func NewSubscriptionService(repository ports.SubscriptionRepository) *SubscriptionService {
	return &SubscriptionService{repository: repository}
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
		return domain.Subscription{}, false, fmt.Errorf("create subscription: %w", err)
	}
	logging.Info(logging.CategorySubscription, "订阅业务已创建", "media_id", command.MediaID, "created", created)
	return item, created, nil
}

// Cancel removes an active subscription; a repeated call returns ErrSubscriptionNotFound.
func (s *SubscriptionService) Cancel(ctx context.Context, id string) (domain.Subscription, error) {
	item, _, err := s.repository.Cancel(ctx, id)
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("cancel subscription: %w", err)
	}
	logging.Info(logging.CategorySubscription, "订阅记录已删除", "subscription_id", id)
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
		return domain.Subscription{}, fmt.Errorf("update subscription: %w", err)
	}
	logging.Info(logging.CategorySubscription, "订阅业务已更新", "subscription_id", command.ID)
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
	logging.Info(logging.CategorySubscription, "订阅列表查询完成", "count", len(result.Items))
	return Page[domain.Subscription]{Page: page, PageSize: pageSize, Total: result.Total, Items: nonNil(result.Items)}, nil
}

// DownloadService owns download task queries.
type DownloadService struct{ repository ports.DownloadRepository }

// NewDownloadService builds a download task application service.
func NewDownloadService(repository ports.DownloadRepository) *DownloadService {
	return &DownloadService{repository: repository}
}

// List returns one validated page of download tasks.
func (s *DownloadService) List(ctx context.Context, page, pageSize int, status domain.DownloadStatus) (Page[domain.DownloadTask], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.DownloadTask]{}, err
	}
	result, err := s.repository.List(ctx, ports.DownloadListQuery{Limit: pageSize, Offset: (page - 1) * pageSize, Status: status})
	if err != nil {
		return Page[domain.DownloadTask]{}, fmt.Errorf("list downloads: %w", err)
	}
	logging.Info(logging.CategoryDownload, "下载任务列表查询完成", "count", len(result.Items))
	return Page[domain.DownloadTask]{Page: page, PageSize: pageSize, Total: result.Total, Items: nonNil(result.Items)}, nil
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
