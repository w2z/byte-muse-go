package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidActorLimitDate = errors.New("invalid actor limit date")

// ErrActorFollowPending 表示规则已保存，追新因异常未完成，后续可安全重试。
var ErrActorFollowPending = errors.New("actor rule saved, follow pending")

// defaultMaxActors 与旧版默认配置一致，设置缺失或非法时回退。
const defaultMaxActors = 3

// ActorService exposes the legacy subscribed-actor view with normalized pagination.
type ActorService struct {
	repository ports.ActorRepository
	settings   func(context.Context) (map[string]string, error)
}

// NewActorService constructs a subscribed actor query service.
func NewActorService(repository ports.ActorRepository) *ActorService {
	return &ActorService{repository: repository}
}

// SetSettingsLoader 注入设置读取；未注入时演员数上限使用旧版默认值。
func (s *ActorService) SetSettingsLoader(load func(context.Context) (map[string]string, error)) {
	s.settings = load
}

// maxActors 读取演员数上限；缺省或非法时回退默认值，0 表示不订阅任何作品。
func (s *ActorService) maxActors(ctx context.Context) int {
	if s.settings == nil {
		return defaultMaxActors
	}
	values, err := s.settings(ctx)
	if err != nil {
		return defaultMaxActors
	}
	raw := strings.TrimSpace(values["MAX_ACTOR"])
	if raw == "" {
		return defaultMaxActors
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return defaultMaxActors
	}
	return n
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
	if err != nil {
		return item, err
	}
	logging.Info(logging.CategorySubscription, "演员订阅已保存", "actor", name)
	if _, followErr := s.Follow(ctx, name); followErr != nil {
		return item, ErrActorFollowPending
	}
	return item, nil
}

// Follow 为保存规则和定时任务提供同一入口；日志记录真实创建数量和失败原因。
func (s *ActorService) Follow(ctx context.Context, name string) (int, error) {
	n, e := s.repository.Follow(ctx, strings.TrimSpace(name), s.maxActors(ctx))
	if e != nil {
		logging.Error(logging.CategorySubscription, "演员追新未完成，已保存规则将在后续任务继续处理", "created", n, "error", e.Error())
	} else {
		logging.Info(logging.CategorySubscription, "演员追新完成", "created", n)
	}
	return n, e
}

// ActiveNames 返回已订阅演员名称，供追新任务按演员抓取作品。
func (s *ActorService) ActiveNames(ctx context.Context) ([]string, error) {
	return s.repository.ActiveNames(ctx)
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
