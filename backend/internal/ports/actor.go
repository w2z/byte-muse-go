package ports

import (
	"context"

	"bytemuse/backend/internal/domain"
)

// ActorListQuery selects an actor tab and optional name keyword.
type ActorListQuery struct {
	Limit        int
	Offset       int
	Subscription string
	Keywords     string
}

// ActorRepository owns actor subscription dates without deleting actor identity rows.
type ActorRepository interface {
	List(ctx context.Context, query ActorListQuery) ([]domain.Actor, int, error)
	SaveSubscription(ctx context.Context, name, limitDate string) (domain.Actor, error)
	CancelSubscription(ctx context.Context, name string) (domain.Actor, error)
	// ActiveNames 返回已订阅演员名称，供追新任务按演员抓取作品。
	ActiveNames(ctx context.Context) ([]string, error)
	// Follow 为已订阅演员匹配本地影片并建立订阅；maxActors<0 表示不限制演员数。
	Follow(ctx context.Context, name string, maxActors int) (int, error)
}
