// Package memory provides transient adapters for explicit non-production use.
package memory

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

type idempotencyRecord struct {
	fingerprint string
	id          string
}

// SubscriptionRepository is a concurrency-safe transient implementation of the subscription port.
type SubscriptionRepository struct {
	mu    sync.RWMutex
	items map[string]domain.Subscription
	keys  map[string]idempotencyRecord
}

// NewSubscriptionRepository creates an empty transient subscription repository.
func NewSubscriptionRepository() *SubscriptionRepository {
	return &SubscriptionRepository{items: make(map[string]domain.Subscription), keys: make(map[string]idempotencyRecord)}
}

// Create atomically creates or replays a subscription keyed by an exact request fingerprint.
func (r *SubscriptionRepository) Create(_ context.Context, request ports.CreateSubscription) (domain.Subscription, bool, error) {
	fingerprint, err := requestFingerprint(request)
	if err != nil {
		return domain.Subscription{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if record, ok := r.keys[request.IdempotencyKey]; ok {
		if record.fingerprint != fingerprint {
			return domain.Subscription{}, false, ports.ErrIdempotencyConflict
		}
		return cloneSubscription(r.items[record.id]), false, nil
	}
	id, err := newID()
	if err != nil {
		return domain.Subscription{}, false, fmt.Errorf("generate subscription id: %w", err)
	}
	now := time.Now().UTC()
	item := domain.Subscription{ID: id, MediaID: request.MediaID, Status: domain.SubscriptionStatusActive, Mode: request.Mode, Filter: cloneFilter(request.Filter), CreatedAt: now, UpdatedAt: now, Version: 1}
	r.items[id] = item
	r.keys[request.IdempotencyKey] = idempotencyRecord{fingerprint: fingerprint, id: id}
	return cloneSubscription(item), true, nil
}

// Cancel transitions an existing subscription to canceled and is idempotent on repeats.
func (r *SubscriptionRepository) Cancel(_ context.Context, id string) (domain.Subscription, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return domain.Subscription{}, false, ports.ErrSubscriptionNotFound
	}
	if item.Status == domain.SubscriptionStatusCanceled {
		return cloneSubscription(item), false, nil
	}
	item.Status = domain.SubscriptionStatusCanceled
	item.Version++
	item.UpdatedAt = time.Now().UTC()
	r.items[id] = item
	return cloneSubscription(item), true, nil
}

// List returns a stable, filtered page ordered by creation time and ID.
func (r *SubscriptionRepository) List(_ context.Context, query ports.SubscriptionListQuery) (domain.SubscriptionPage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Subscription, 0, len(r.items))
	for _, item := range r.items {
		if query.Status != "" && item.Status != query.Status {
			continue
		}
		items = append(items, cloneSubscription(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	total := len(items)
	start := min(query.Offset, total)
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	end := min(start+limit, total)
	return domain.SubscriptionPage{Items: items[start:end], Total: total}, nil
}

func requestFingerprint(request ports.CreateSubscription) (string, error) {
	encoded, err := json.Marshal([]any{request.MediaID, request.Mode, request.Filter})
	if err != nil {
		return "", fmt.Errorf("encode subscription fingerprint: %w", err)
	}
	return string(encoded), nil
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func cloneSubscription(item domain.Subscription) domain.Subscription {
	item.Filter = cloneFilter(item.Filter)
	return item
}

func cloneFilter(filter map[string]any) map[string]any {
	if filter == nil {
		return map[string]any{}
	}
	copy := make(map[string]any, len(filter))
	for key, value := range filter {
		copy[key] = value
	}
	return copy
}
