package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

func TestCatalogListsRequestedPage(t *testing.T) {
	repo := &movieRepositoryStub{page: domain.MediaPage{
		Items: []domain.Media{{ID: "media-1", Code: "BM-001", Title: "影片一"}},
		Total: 41,
	}}
	service := application.NewCatalogService(repo)

	got, err := service.List(context.Background(), 3, 20)
	if err != nil {
		t.Fatalf("list media: %v", err)
	}
	if repo.query.Limit != 20 || repo.query.Offset != 40 {
		t.Fatalf("repository query = %+v, want limit=20 offset=40", repo.query)
	}
	if got.Page != 3 || got.PageSize != 20 || got.Total != 41 || len(got.Items) != 1 {
		t.Fatalf("page = %+v, want page metadata and one item", got)
	}
}

func TestCatalogRejectsInvalidPagination(t *testing.T) {
	service := application.NewCatalogService(&movieRepositoryStub{})
	for _, tc := range []struct {
		page, pageSize int
	}{
		{page: 0, pageSize: 20},
		{page: 1, pageSize: 0},
		{page: 1, pageSize: 101},
	} {
		if _, err := service.List(context.Background(), tc.page, tc.pageSize); !errors.Is(err, application.ErrInvalidPagination) {
			t.Fatalf("List(%d, %d) error = %v, want ErrInvalidPagination", tc.page, tc.pageSize, err)
		}
	}
}

func TestSubscriptionCreateIsIdempotent(t *testing.T) {
	repo := newSubscriptionRepositoryStub()
	service := application.NewSubscriptionService(repo)
	command := application.CreateSubscriptionCommand{
		IdempotencyKey: "request-0001",
		MediaID:       "media-1",
		Mode:          domain.SubscriptionModeStrict,
		Filter:        map[string]any{"minimum_seeders": float64(2)},
	}

	first, created, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, replayCreated, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if !created || replayCreated {
		t.Fatalf("created flags first/replay = %t/%t, want true/false", created, replayCreated)
	}
	if first.ID != second.ID || first.Version != second.Version || repo.createCount != 1 {
		t.Fatalf("idempotent result mismatch: first=%+v second=%+v writes=%d", first, second, repo.createCount)
	}
}

func TestSubscriptionRejectsReusedKeyWithDifferentPayload(t *testing.T) {
	repo := newSubscriptionRepositoryStub()
	service := application.NewSubscriptionService(repo)
	first := application.CreateSubscriptionCommand{IdempotencyKey: "request-0002", MediaID: "media-1", Mode: domain.SubscriptionModeStrict}
	if _, _, err := service.Create(context.Background(), first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	first.MediaID = "media-2"
	if _, _, err := service.Create(context.Background(), first); !errors.Is(err, ports.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestSubscriptionCancelIsIdempotent(t *testing.T) {
	repo := newSubscriptionRepositoryStub()
	service := application.NewSubscriptionService(repo)
	created, _, err := service.Create(context.Background(), application.CreateSubscriptionCommand{
		IdempotencyKey: "request-0003",
		MediaID:       "media-1",
		Mode:          domain.SubscriptionModePreload,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := service.Cancel(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	second, err := service.Cancel(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("second cancel: %v", err)
	}
	if first.Status != domain.SubscriptionStatusCanceled || second.Status != domain.SubscriptionStatusCanceled {
		t.Fatalf("cancel statuses = %q/%q, want canceled", first.Status, second.Status)
	}
	if first.Version != second.Version || repo.cancelCount != 1 {
		t.Fatalf("idempotent cancel versions = %d/%d writes=%d", first.Version, second.Version, repo.cancelCount)
	}
}

type movieRepositoryStub struct {
	query ports.MediaListQuery
	page  domain.MediaPage
	err   error
}

func (r *movieRepositoryStub) List(_ context.Context, query ports.MediaListQuery) (domain.MediaPage, error) {
	r.query = query
	return r.page, r.err
}

func (r *movieRepositoryStub) Get(_ context.Context, id string) (domain.Media, error) {
	for _, item := range r.page.Items {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.Media{}, ports.ErrMediaNotFound
}

type subscriptionRepositoryStub struct {
	mu            sync.Mutex
	byID          map[string]domain.Subscription
	byKey         map[string]storedRequest
	createCount   int
	cancelCount   int
	currentTime   time.Time
}

type storedRequest struct {
	mediaID string
	mode    domain.SubscriptionMode
	item    domain.Subscription
}

func newSubscriptionRepositoryStub() *subscriptionRepositoryStub {
	return &subscriptionRepositoryStub{
		byID:        make(map[string]domain.Subscription),
		byKey:       make(map[string]storedRequest),
		currentTime: time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC),
	}
}

func (r *subscriptionRepositoryStub) Create(_ context.Context, request ports.CreateSubscription) (domain.Subscription, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if stored, ok := r.byKey[request.IdempotencyKey]; ok {
		if stored.mediaID != request.MediaID || stored.mode != request.Mode {
			return domain.Subscription{}, false, ports.ErrIdempotencyConflict
		}
		return stored.item, false, nil
	}
	r.createCount++
	item := domain.Subscription{
		ID:        "01K5Y8DB7W3YB6AJ6F8W9P8V4P",
		MediaID:   request.MediaID,
		Status:    domain.SubscriptionStatusActive,
		Mode:      request.Mode,
		Filter:    request.Filter,
		CreatedAt: r.currentTime,
		UpdatedAt: r.currentTime,
		Version:   1,
	}
	r.byID[item.ID] = item
	r.byKey[request.IdempotencyKey] = storedRequest{mediaID: request.MediaID, mode: request.Mode, item: item}
	return item, true, nil
}

func (r *subscriptionRepositoryStub) Cancel(_ context.Context, id string) (domain.Subscription, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return domain.Subscription{}, false, ports.ErrSubscriptionNotFound
	}
	if item.Status == domain.SubscriptionStatusCanceled {
		return item, false, nil
	}
	r.cancelCount++
	item.Status = domain.SubscriptionStatusCanceled
	item.Version++
	item.UpdatedAt = item.UpdatedAt.Add(time.Second)
	r.byID[id] = item
	for key, request := range r.byKey {
		if request.item.ID == id {
			request.item = item
			r.byKey[key] = request
		}
	}
	return item, true, nil
}

func (r *subscriptionRepositoryStub) List(_ context.Context, _ ports.SubscriptionListQuery) (domain.SubscriptionPage, error) {
	items := make([]domain.Subscription, 0, len(r.byID))
	for _, item := range r.byID {
		items = append(items, item)
	}
	return domain.SubscriptionPage{Items: items, Total: len(items)}, nil
}
