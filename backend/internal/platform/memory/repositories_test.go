package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/memory"
	"bytemuse/backend/internal/ports"
)

func TestSubscriptionRepositorySerializesConcurrentIdempotentCreates(t *testing.T) {
	repository := memory.NewSubscriptionRepository()
	request := ports.CreateSubscription{IdempotencyKey: "concurrent-request", MediaID: "media-1", Mode: domain.SubscriptionModeStrict, Filter: map[string]any{}}
	const workers = 16
	results := make(chan string, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			item, _, err := repository.Create(context.Background(), request)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- item.ID
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("create: %v", err)
	}
	first := ""
	for id := range results {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("idempotent create returned IDs %q and %q", first, id)
		}
	}
	page, err := repository.List(context.Background(), ports.SubscriptionListQuery{Limit: 20})
	if err != nil || page.Total != 1 {
		t.Fatalf("list = %+v, %v; want one subscription", page, err)
	}
}

func TestSubscriptionRepositoryRejectsChangedFilterForSameKey(t *testing.T) {
	repository := memory.NewSubscriptionRepository()
	request := ports.CreateSubscription{IdempotencyKey: "changed-filter", MediaID: "media-1", Mode: domain.SubscriptionModeStrict, Filter: map[string]any{"seeders": 1}}
	if _, _, err := repository.Create(context.Background(), request); err != nil {
		t.Fatalf("first create: %v", err)
	}
	request.Filter = map[string]any{"seeders": 2}
	if _, _, err := repository.Create(context.Background(), request); !errors.Is(err, ports.ErrIdempotencyConflict) {
		t.Fatalf("changed filter error = %v, want ErrIdempotencyConflict", err)
	}
}
