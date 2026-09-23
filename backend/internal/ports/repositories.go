// Package ports defines storage and readiness boundaries consumed by application services.
package ports

import (
	"context"
	"errors"

	"bytemuse/backend/internal/domain"
)

var (
	// ErrMediaNotFound reports an unknown media identity.
	ErrMediaNotFound = errors.New("media not found")
	// ErrSubscriptionNotFound reports an unknown subscription identity.
	ErrSubscriptionNotFound = errors.New("subscription not found")
	// ErrIdempotencyConflict reports reuse of a key for a different operation payload.
	ErrIdempotencyConflict = errors.New("idempotency key payload conflict")
)

// MediaListQuery is the normalized repository pagination request.
type MediaListQuery struct {
	Limit  int
	Offset int
}

// MediaRepository reads normalized catalog media without exposing database details.
type MediaRepository interface {
	List(ctx context.Context, query MediaListQuery) (domain.MediaPage, error)
	Get(ctx context.Context, id string) (domain.Media, error)
}

// CreateSubscription is an atomic idempotent subscription write request.
type CreateSubscription struct {
	IdempotencyKey string
	MediaID       string
	Mode          domain.SubscriptionMode
	Filter        map[string]any
}

// SubscriptionListQuery is the normalized repository pagination and status filter.
type SubscriptionListQuery struct {
	Limit  int
	Offset int
	Status domain.SubscriptionStatus
}

// SubscriptionRepository owns atomic create/replay and cancel transitions.
type SubscriptionRepository interface {
	Create(ctx context.Context, request CreateSubscription) (item domain.Subscription, created bool, err error)
	Cancel(ctx context.Context, id string) (item domain.Subscription, changed bool, err error)
	List(ctx context.Context, query SubscriptionListQuery) (domain.SubscriptionPage, error)
}

// DownloadListQuery is the normalized download task pagination and status filter.
type DownloadListQuery struct {
	Limit  int
	Offset int
	Status domain.DownloadStatus
}

// DownloadRepository reads download tasks independently from media and subscription state.
type DownloadRepository interface {
	List(ctx context.Context, query DownloadListQuery) (domain.DownloadPage, error)
}

// ReadinessProbe verifies dependencies required before traffic is accepted.
type ReadinessProbe interface {
	Ready(ctx context.Context) error
}

// HealthyIntegrationCounter reports currently healthy external integrations.
type HealthyIntegrationCounter interface {
	CountHealthy(ctx context.Context) (int, error)
}
