package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"fmt"
)

// ActorService exposes the legacy subscribed-actor view with normalized pagination.
type ActorService struct{ repository ports.ActorRepository }

// NewActorService constructs a subscribed actor query service.
func NewActorService(repository ports.ActorRepository) *ActorService {
	return &ActorService{repository: repository}
}

// List returns only actors with an active legacy subscription date.
func (s *ActorService) List(ctx context.Context, page, pageSize int) (Page[domain.Actor], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Actor]{}, err
	}
	items, total, err := s.repository.ListSubscribed(ctx, pageSize, (page-1)*pageSize)
	if err != nil {
		return Page[domain.Actor]{}, fmt.Errorf("list actors: %w", err)
	}
	return Page[domain.Actor]{Page: page, PageSize: pageSize, Total: total, Items: nonNil(items)}, nil
}
