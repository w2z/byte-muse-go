package ports

import (
	"context"

	"bytemuse/backend/internal/domain"
)

// ActorListQuery selects all, active, or unsubscribed actors.
type ActorListQuery struct {
	Limit        int
	Offset       int
	Subscription string
}

// ActorRepository owns actor subscription dates without deleting actor identity rows.
type ActorRepository interface {
	List(ctx context.Context, query ActorListQuery) ([]domain.Actor, int, error)
	SaveSubscription(ctx context.Context, name, limitDate string) (domain.Actor, error)
	CancelSubscription(ctx context.Context, name string) (domain.Actor, error)
}
