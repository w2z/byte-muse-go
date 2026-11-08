package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"sort"
	"sync"
)

// uploadQueueStore is a single-process write-through queue; failed durable writes never publish changes.
// Run loads it once per startup. Every upload control and worker must use this same store.
type uploadQueueStore struct {
	UploadStore
	mu      sync.RWMutex
	writes  [64]sync.Mutex
	records map[string]domain.UploadRecord
	pending []string
	changes map[string]domain.UploadRecord
}

func newUploadQueueStore(ctx context.Context, durable UploadStore) (*uploadQueueStore, error) {
	records, err := durable.List(ctx)
	if err != nil {
		return nil, err
	}
	store := &uploadQueueStore{UploadStore: durable, records: map[string]domain.UploadRecord{}, changes: map[string]domain.UploadRecord{}}
	for _, record := range records {
		store.records[record.Key] = record
		if uploadQueued(record) {
			store.pending = append(store.pending, record.Key)
		}
	}
	sort.Slice(store.pending, func(left, right int) bool {
		return uploadQueueBefore(store.records[store.pending[left]], store.records[store.pending[right]])
	})
	return store, nil
}

func uploadQueued(record domain.UploadRecord) bool {
	return record.State != "control" && (!uploadTerminal(record.State) || (record.State == "stopped" && !record.Hidden))
}

func (store *uploadQueueStore) Get(ctx context.Context, key string) (*domain.UploadRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	record, exists := store.records[key]
	if !exists {
		return nil, nil
	}
	return &record, nil
}

func (store *uploadQueueStore) Save(ctx context.Context, record domain.UploadRecord) error {
	var stripe uint32
	for _, value := range []byte(record.Key) {
		stripe = stripe*31 + uint32(value)
	}
	write := &store.writes[stripe%uint32(len(store.writes))]
	write.Lock()
	defer write.Unlock()
	if err := store.UploadStore.Save(ctx, record); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	previous, exists := store.records[record.Key]
	store.records[record.Key] = record
	store.changes[record.Key] = record
	wasQueued := exists && uploadQueued(previous)
	isQueued := uploadQueued(record)
	orderChanged := uploadQueueTime(previous) != uploadQueueTime(record) || previous.Source != record.Source
	if wasQueued && (!isQueued || orderChanged) {
		for index, key := range store.pending {
			if key == record.Key {
				store.pending = append(store.pending[:index], store.pending[index+1:]...)
				break
			}
		}
	}
	if isQueued && (!wasQueued || orderChanged) {
		index := sort.Search(len(store.pending), func(index int) bool { return !uploadQueueBefore(store.records[store.pending[index]], record) })
		store.pending = append(store.pending, "")
		copy(store.pending[index+1:], store.pending[index:])
		store.pending[index] = record.Key
	}
	return nil
}

func (store *uploadQueueStore) PendingCommits(ctx context.Context) ([]domain.UploadRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]domain.UploadRecord, 0, len(store.pending))
	for _, key := range store.pending {
		records = append(records, store.records[key])
	}
	return records, nil
}

func (store *uploadQueueStore) List(ctx context.Context) ([]domain.UploadRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]domain.UploadRecord, 0, len(store.records))
	for _, record := range store.records {
		records = append(records, record)
	}
	return records, nil
}

func (store *uploadQueueStore) takeChanges() []domain.UploadRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	records := make([]domain.UploadRecord, 0, len(store.changes))
	for _, record := range store.changes {
		records = append(records, record)
	}
	clear(store.changes)
	return records
}

// HideCompleted retains the repository's bulk transaction and updates only successful receipts after commit.
func (store *uploadQueueStore) HideCompleted(ctx context.Context, keys []string) error {
	for index := range store.writes {
		store.writes[index].Lock()
	}
	defer func() {
		for index := range store.writes {
			store.writes[index].Unlock()
		}
	}()
	store.mu.Lock()
	defer store.mu.Unlock()
	if bulk, ok := store.UploadStore.(interface {
		HideCompleted(context.Context, []string) error
	}); ok {
		if err := bulk.HideCompleted(ctx, keys); err != nil {
			return err
		}
		for _, key := range keys {
			record, exists := store.records[key]
			if exists && (record.State == "completed" || record.State == "skipped") {
				record.Hidden = true
				store.records[key], store.changes[key] = record, record
			}
		}
		return nil
	}
	for _, key := range keys {
		record, exists := store.records[key]
		if !exists || (record.State != "completed" && record.State != "skipped") {
			continue
		}
		record.Hidden = true
		if err := store.UploadStore.Save(ctx, record); err != nil {
			return err
		}
		store.records[key], store.changes[key] = record, record
	}
	return nil
}
