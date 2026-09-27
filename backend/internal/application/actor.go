package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidActorLimitDate = errors.New("invalid actor limit date")

// ActorService exposes the legacy subscribed-actor view with normalized pagination.
type ActorService struct{ repository ports.ActorRepository }

// NewActorService constructs a subscribed actor query service.
func NewActorService(repository ports.ActorRepository) *ActorService {
	return &ActorService{repository: repository}
}

// List returns actors for a tab and optional name keyword.
func (s *ActorService) List(ctx context.Context, page, pageSize int, subscription, keywords string) (Page[domain.Actor], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Actor]{}, err
	}
	if subscription == "" {
		subscription = "all"
	}
	if subscription != "all" && subscription != "active" && subscription != "none" && subscription != "hot" {
		return Page[domain.Actor]{}, ErrInvalidPagination
	}
	items, total, err := s.repository.List(ctx, ports.ActorListQuery{Limit: pageSize, Offset: (page - 1) * pageSize, Subscription: subscription, Keywords: keywords})
	if err != nil {
		return Page[domain.Actor]{}, fmt.Errorf("list actors: %w", err)
	}
	logging.Info(logging.CategorySubscription, "演员列表查询完成", "count", len(items), "subscription", subscription)
	return Page[domain.Actor]{Page: page, PageSize: pageSize, Total: total, Items: nonNil(items)}, nil
}

// SaveSubscription creates or edits the cutoff date of an existing actor.
func (s *ActorService) SaveSubscription(ctx context.Context, name, limitDate string) (domain.Actor, error) {
	name, limitDate = strings.TrimSpace(name), strings.TrimSpace(limitDate)
	if name == "" {
		return domain.Actor{}, ports.ErrActorNotFound
	}
	if _, err := time.Parse("2006-01-02", limitDate); err != nil {
		return domain.Actor{}, fmt.Errorf("%w: %v", ErrInvalidActorLimitDate, err)
	}
	item, err := s.repository.SaveSubscription(ctx, name, limitDate)
	if err == nil {
		logging.Info(logging.CategorySubscription, "演员订阅已保存", "actor", name)
	}
	return item, err
}

// CancelSubscription clears only the cutoff date and keeps the actor row.
func (s *ActorService) CancelSubscription(ctx context.Context, name string) (domain.Actor, error) {
	name = strings.TrimSpace(name)
	item, err := s.repository.CancelSubscription(ctx, name)
	if err == nil {
		logging.Info(logging.CategorySubscription, "演员订阅已取消", "actor", name)
	}
	return item, err
}
