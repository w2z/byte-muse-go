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
}
