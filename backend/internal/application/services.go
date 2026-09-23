// Package application contains entry-point-neutral ByteMuse business workflows.
package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
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
type CatalogService struct{ repository ports.MediaRepository }

// NewCatalogService builds a media application service.
func NewCatalogService(repository ports.MediaRepository) *CatalogService {
	return &CatalogService{repository: repository}
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
	return Page[domain.Media]{Page: page, PageSize: pageSize, Total: result.Total, Items: nonNil(result.Items)}, nil
}

// Get returns a media detail by stable identity.
func (s *CatalogService) Get(ctx context.Context, id string) (domain.Media, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return domain.Media{}, fmt.Errorf("get media: %w", err)
	}
	return item, nil
}

// CreateSubscriptionCommand carries the API idempotency key and subscription intent.
type CreateSubscriptionCommand struct {
	IdempotencyKey string
	MediaID       string
	Mode          domain.SubscriptionMode
	Filter        map[string]any
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
		MediaID:       command.MediaID,
		Mode:          command.Mode,
		Filter:        command.Filter,
	})
	if err != nil {
		return domain.Subscription{}, false, fmt.Errorf("create subscription: %w", err)
	}
	return item, created, nil
}

// Cancel transitions an active subscription to canceled; repeated calls return the same state.
func (s *SubscriptionService) Cancel(ctx context.Context, id string) (domain.Subscription, error) {
	item, _, err := s.repository.Cancel(ctx, id)
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("cancel subscription: %w", err)
	}
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
	return Page[domain.DownloadTask]{Page: page, PageSize: pageSize, Total: result.Total, Items: nonNil(result.Items)}, nil
}

// DashboardService aggregates independent repository status dimensions.
type DashboardService struct {
	media         ports.MediaRepository
	subscriptions ports.SubscriptionRepository
	downloads     ports.DownloadRepository
	integrations  ports.HealthyIntegrationCounter
}

// NewDashboardService builds the dashboard aggregation workflow.
func NewDashboardService(media ports.MediaRepository, subscriptions ports.SubscriptionRepository, downloads ports.DownloadRepository, integrations ports.HealthyIntegrationCounter) *DashboardService {
	return &DashboardService{media: media, subscriptions: subscriptions, downloads: downloads, integrations: integrations}
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
	healthy, err := s.integrations.CountHealthy(ctx)
	if err != nil {
		return domain.Dashboard{}, fmt.Errorf("count healthy integrations: %w", err)
	}
	return domain.Dashboard{
		ActiveSubscriptions: subscriptions.Total,
		CompletedDownloads:  downloads.Total,
		MediaCount:           media.Total,
		HealthyIntegrations:  healthy,
	}, nil
}

// SystemService reports process metadata without exposing secrets.
type SystemService struct {
	version          string
	databaseDriver   string
	demoSeedEnabled  bool
	startedAt        time.Time
	schedulerRunning func() bool
}

// NewSystemService builds the system status and non-sensitive settings service.
func NewSystemService(version, databaseDriver string, demoSeedEnabled bool, startedAt time.Time, schedulerRunning func() bool) *SystemService {
	return &SystemService{version: version, databaseDriver: databaseDriver, demoSeedEnabled: demoSeedEnabled, startedAt: startedAt, schedulerRunning: schedulerRunning}
}

// Status returns current non-sensitive process state.
func (s *SystemService) Status() domain.SystemStatus {
	return domain.SystemStatus{Version: s.version, DatabaseDriver: s.databaseDriver, SchedulerRunning: s.schedulerRunning(), StartedAt: s.startedAt}
}

// Settings returns only runtime values explicitly allowed by the public contract.
func (s *SystemService) Settings() domain.SystemSettings {
	return domain.SystemSettings{DatabaseDriver: s.databaseDriver, DemoSeedEnabled: s.demoSeedEnabled}
}

func validatePagination(page, pageSize int) error {
	if page < 1 || pageSize < 1 || pageSize > 100 {
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
