package ports

import (
	"bytemuse/backend/internal/domain"
	"context"
)

// ActorRepository reads persisted actor subscriptions.
type ActorRepository interface {
	ListSubscribed(ctx context.Context, limit, offset int) ([]domain.Actor, int, error)
}
