package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"testing"
)

type uploadCountedStore struct {
	uploadMemoryStore
	lists int
	fail  bool
}

func (store *uploadCountedStore) List(ctx context.Context) ([]domain.UploadRecord, error) {
	store.lists++
	return store.uploadMemoryStore.List(ctx)
}

func (store *uploadCountedStore) Save(ctx context.Context, record domain.UploadRecord) error {
	if store.fail {
		return errors.New("save failed")
	}
	return store.uploadMemoryStore.Save(ctx, record)
}

func TestUploadQueueCacheUsesCommittedRecords(t *testing.T) {
	ctx := context.Background()
	durable := &uploadCountedStore{uploadMemoryStore: uploadMemoryStore{
		"first":  {Key: "first", State: "pending", QueuedAt: "2026-11-08T00:00:00Z"},
		"second": {Key: "second", State: "pending", QueuedAt: "2026-11-08T00:00:01Z"},
	}}
	cache, err := newUploadQueueStore(ctx, durable)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		records, err := cache.PendingCommits(ctx)
		if err != nil || len(records) != 2 || records[0].Key != "first" {
			t.Fatalf("queue=%v err=%v", records, err)
		}
		_, _ = cache.List(ctx)
	}
	if durable.lists != 1 {
		t.Fatalf("full database reads=%d", durable.lists)
	}
	record, _ := cache.Get(ctx, "first")
	record.State = "completed"
	durable.fail = true
	if cache.Save(ctx, *record) == nil {
		t.Fatal("save failure swallowed")
	}
	unchanged, _ := cache.Get(ctx, "first")
	if unchanged.State != "pending" || len(cache.takeChanges()) != 0 {
		t.Fatal("failed commit changed cache")
	}
	durable.fail = false
	if err = cache.Save(ctx, *record); err != nil {
		t.Fatal(err)
	}
	changes := cache.takeChanges()
	if len(changes) != 1 || changes[0].State != "completed" {
		t.Fatal(changes)
	}
	pending, _ := cache.PendingCommits(ctx)
	if len(pending) != 1 || pending[0].Key != "second" {
		t.Fatal(pending)
	}
	if err = cache.HideCompleted(ctx, []string{"first", "second"}); err != nil {
		t.Fatal(err)
	}
	first, _ := cache.Get(ctx, "first")
	second, _ := cache.Get(ctx, "second")
	if !first.Hidden || second.Hidden {
		t.Fatal("clear changed pending task")
	}
}
