package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"bytemuse/backend/internal/ports"
)

// taskJournal stores immutable inputs and successful units independently of UI progress.
// A unit is recorded only after its side effect completes; unconfirmed units may be retried.
type taskJournal struct {
	repo   ports.TaskCheckpointRepository
	id     string
	mu     sync.Mutex
	memory map[string][]byte
}
type taskJournalKey struct{}

func journalContext(ctx context.Context, journal *taskJournal) context.Context {
	return context.WithValue(ctx, taskJournalKey{}, journal)
}

func journalKey(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (journal *taskJournal) load(ctx context.Context, key string, target any) (bool, error) {
	var raw []byte
	var err error
	if journal.repo != nil {
		raw, err = journal.repo.LoadCheckpoint(ctx, journal.id, key)
	} else {
		journal.mu.Lock()
		raw = journal.memory[key]
		journal.mu.Unlock()
	}
	if err != nil || len(raw) == 0 {
		return false, err
	}
	return true, json.Unmarshal(raw, target)
}

func (journal *taskJournal) save(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if journal.repo != nil {
		return journal.repo.SaveCheckpoint(ctx, journal.id, key, raw)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.memory == nil {
		journal.memory = map[string][]byte{}
	}
	journal.memory[key] = raw
	return nil
}

func loadTaskCheckpoint(ctx context.Context, key string, target any) (bool, error) {
	journal, _ := ctx.Value(taskJournalKey{}).(*taskJournal)
	if journal == nil {
		return false, nil
	}
	return journal.load(ctx, key, target)
}

func saveTaskCheckpoint(ctx context.Context, key string, value any) error {
	journal, _ := ctx.Value(taskJournalKey{}).(*taskJournal)
	if journal == nil {
		return nil
	}
	return journal.save(ctx, key, value)
}

// taskInput freezes only caller-selected non-secret settings; credentials remain live.
func taskInput(ctx context.Context, key string, value any) error {
	found, err := loadTaskCheckpoint(ctx, journalKey("input", key), value)
	if err != nil || found {
		return err
	}
	return saveTaskCheckpoint(ctx, journalKey("input", key), value)
}

func taskUnitDone(ctx context.Context, parts ...string) (bool, error) {
	var done bool
	_, err := loadTaskCheckpoint(ctx, journalKey(parts...), &done)
	return done, err
}

func completeTaskUnit(ctx context.Context, parts ...string) error {
	return saveTaskCheckpoint(ctx, journalKey(parts...), true)
}
