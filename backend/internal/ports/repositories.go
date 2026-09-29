// Package ports defines storage and readiness boundaries consumed by application services.
package ports

import (
	"context"
	"errors"
	"time"

	"bytemuse/backend/internal/domain"
)

var (
	// ErrMediaNotFound reports an unknown media identity.
	ErrMediaNotFound = errors.New("media not found")
	// ErrSubscriptionNotFound reports an unknown subscription identity.
	ErrSubscriptionNotFound = errors.New("subscription not found")
	// ErrIdempotencyConflict reports reuse of a key for a different operation payload.
	ErrIdempotencyConflict = errors.New("idempotency key payload conflict")
	// ErrVersionConflict reports an optimistic concurrency conflict.
	ErrVersionConflict = errors.New("version conflict")
	// ErrSubscriptionInactive reports attempts to edit a canceled subscription.
	ErrSubscriptionInactive = errors.New("subscription inactive")
	// ErrActiveSubscriptionExists reports a second active subscription for one media item.
	ErrActiveSubscriptionExists = errors.New("active subscription already exists")
	// ErrActorNotFound reports an unknown actor identity.
	ErrActorNotFound = errors.New("actor not found")
)

// MaxPageSize 是列表查询每页条数的唯一上限，HTTP 层、业务层与仓储层都必须引用它。
//
// 三处曾各自写死 100：HTTP 与业务层拒绝超限请求，仓储层则把超限值静默降级
// （limit 回落 50、日志 page_size 回落 20），于是 page_size=200 会得到
// “响应声明 200、实际只返回 50 条”的错误组合。收敛成一个常量后不会再漂移。
// 前端分页条的每页条数选项（frontend/src/shared/ui/ListPagination.tsx 的 PAGE_SIZE_OPTIONS）
// 不得超过该值。
const MaxPageSize = 200

// MediaListQuery is the normalized repository pagination request.
type MediaListQuery struct {
	Limit              int
	Offset             int
	Search             string
	SubscriptionStatus string
	DownloadStatus     string
	LibraryStatus      string
	VideoType          string // 空串不限；unknown 查询尚未分类。
}

// MediaRepository reads normalized catalog media without exposing database details.
type MediaRepository interface {
	List(ctx context.Context, query MediaListQuery) (domain.MediaPage, error)
	Get(ctx context.Context, id string) (domain.Media, error)
}

// MediaTranslationWriter 持久化成功翻译后的媒体标题。
type MediaTranslationWriter interface {
	UpdateTranslatedTitle(ctx context.Context, id, translatedTitle string) error
}

// CreateSubscription is an atomic idempotent subscription write request.
type CreateSubscription struct {
	IdempotencyKey string
	MediaID        string
	Mode           domain.SubscriptionMode
	Filter         map[string]any
}

// UpdateSubscription replaces editable rules on one active subscription.
type UpdateSubscription struct {
	ID              string
	Mode            domain.SubscriptionMode
	Filter          map[string]any
	ExpectedVersion int
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
	Update(ctx context.Context, request UpdateSubscription) (domain.Subscription, error)
	Cancel(ctx context.Context, id string) (item domain.Subscription, changed bool, err error)
	List(ctx context.Context, query SubscriptionListQuery) (domain.SubscriptionPage, error)
}

// DownloadListQuery is the normalized download task pagination and status filter.
type DownloadListQuery struct {
	Limit          int
	Offset         int
	Status         domain.DownloadStatus
	TransferStatus string
	// MediaID 按影片精确定位下载任务，供渠道卡片等需要「某部影片的当前任务」的场景使用。
	MediaID                                        string
	AddedFrom, AddedTo, CompletedFrom, CompletedTo *time.Time
}

// DownloadRepository reads download tasks independently from media and subscription state.
type DownloadRepository interface {
	List(ctx context.Context, query DownloadListQuery) (domain.DownloadPage, error)
}

// ReadinessProbe verifies dependencies required before traffic is accepted.
type ReadinessProbe interface {
	Ready(ctx context.Context) error
}
