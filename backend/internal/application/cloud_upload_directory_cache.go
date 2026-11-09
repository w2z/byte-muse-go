package application

import (
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"errors"
	"sync"
	"time"
)

const uploadDirectoryTTL = 40 * time.Second
const uploadDirectoryCacheLimit = 512
const uploadDirectoryEntryLimit = 100000

type uploadDirectoryCacheKey struct {
	scope, parent string
}

type uploadDirectorySnapshot struct {
	entries []uploadEntry
	started time.Time
}

type uploadDirectoryFlight struct {
	done        chan struct{}
	started     time.Time
	entries     []uploadEntry
	err         error
	valid       bool
	invalidated bool
	superseded  bool
	epoch       uint64
}

// uploadDirectoryCache shares complete listings only; freshness barriers exclude reads begun before a write.
// It is memory-only and bounded, so restart never revives an assumed-empty directory.
type uploadDirectoryCache struct {
	mu           sync.Mutex
	snapshots    map[uploadDirectoryCacheKey]uploadDirectorySnapshot
	flights      map[uploadDirectoryCacheKey]*uploadDirectoryFlight
	entries      int
	epoch        uint64
	reservations map[string]*uploadNameReservation
	paths        map[uploadDirectoryCacheKey]uploadDirectoryPath
}

type uploadDirectoryPath struct {
	path     []pan115.Directory
	observed time.Time
}

// rememberPath retains bounded account-scoped ancestry independently of mutable directory contents.
func (cache *uploadDirectoryCache) rememberPath(scope, parent string, ancestors []pan115.Directory) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.rememberPathLocked(scope, parent, ancestors)
}
func (cache *uploadDirectoryCache) rememberPathLocked(scope, parent string, ancestors []pan115.Directory) {
	if len(ancestors) == 0 || ancestors[len(ancestors)-1].ID != parent {
		return
	}
	if cache.paths == nil {
		cache.paths = map[uploadDirectoryCacheKey]uploadDirectoryPath{}
	}
	if len(cache.paths) >= uploadDirectoryCacheLimit {
		for key := range cache.paths {
			delete(cache.paths, key)
			break
		}
	}
	cache.paths[uploadDirectoryCacheKey{scope, parent}] = uploadDirectoryPath{append([]pan115.Directory(nil), ancestors...), time.Now()}
}

func (cache *uploadDirectoryCache) directoryPath(scope, parent string) []pan115.Directory {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	p := cache.paths[uploadDirectoryCacheKey{scope, parent}]
	if time.Since(p.observed) >= uploadDirectoryTTL {
		return nil
	}
	return append([]pan115.Directory(nil), p.path...)
}

// mergeUploadEntry preserves distinct same-name identities; only the exact uploaded ID is replaced.
func mergeUploadEntry(entries []uploadEntry, entry uploadEntry) []uploadEntry {
	result := append([]uploadEntry(nil), entries...)
	for i, e := range result {
		if e.ID == entry.ID {
			result[i] = entry
			return result
		}
	}
	return append(result, entry)
}

// uploaded merges a confirmed object without extending the original full-list freshness.
// In-flight older lists are invalidated so they cannot erase a newly confirmed upload.
func (cache *uploadDirectoryCache) uploaded(scope, parent string, entry uploadEntry) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key := uploadDirectoryCacheKey{scope, parent}
	if flight := cache.flights[key]; flight != nil {
		flight.invalidated = true
	}
	if snapshot, ok := cache.snapshots[key]; ok {
		snapshot.entries = mergeUploadEntry(snapshot.entries, entry)
		cache.putLocked(key, snapshot)
	}
}

type uploadNameReservation struct {
	slot  chan struct{}
	users int
}

// reserve serializes only the exact destination name, with cancellation and bounded lifetime.
func (cache *uploadDirectoryCache) reserve(ctx context.Context, key string) (func(), error) {
	cache.mu.Lock()
	if cache.reservations == nil {
		cache.reservations = make(map[string]*uploadNameReservation)
	}
	reservation := cache.reservations[key]
	if reservation == nil {
		reservation = &uploadNameReservation{slot: make(chan struct{}, 1)}
		cache.reservations[key] = reservation
	}
	reservation.users++
	cache.mu.Unlock()
	releaseRef := func() {
		cache.mu.Lock()
		reservation.users--
		if reservation.users == 0 {
			delete(cache.reservations, key)
		}
		cache.mu.Unlock()
	}
	select {
	case reservation.slot <- struct{}{}:
		return func() { <-reservation.slot; releaseRef() }, nil
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
}

// list coalesces overlapping reads; canceled waiters do not cancel another worker's request.
func (cache *uploadDirectoryCache) list(ctx context.Context, key uploadDirectoryCacheKey, after time.Time, load func(context.Context, string) ([]uploadEntry, error)) ([]uploadEntry, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cache.mu.Lock()
		if snapshot, ok := cache.snapshots[key]; ok && time.Since(snapshot.started) < uploadDirectoryTTL && snapshot.started.After(after) {
			entries := append([]uploadEntry(nil), snapshot.entries...)
			cache.mu.Unlock()
			pan115.TraceUploadCache(ctx, "hit")
			return entries, nil
		}
		if flight := cache.flights[key]; flight != nil {
			eligible := !flight.superseded
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight.done:
				cache.mu.Lock()
				valid := flight.valid && !flight.invalidated && flight.epoch == cache.epoch
				cache.mu.Unlock()
				if eligible && valid && flight.started.After(after) && flight.err == nil {
					pan115.TraceUploadCache(ctx, "shared")
					return append([]uploadEntry(nil), flight.entries...), nil
				}
				if flight.err != nil && !errors.Is(flight.err, context.Canceled) && !errors.Is(flight.err, context.DeadlineExceeded) {
					return nil, flight.err
				}
				continue
			}
		}
		if cache.flights == nil {
			cache.flights = make(map[uploadDirectoryCacheKey]*uploadDirectoryFlight)
		}
		flight := &uploadDirectoryFlight{done: make(chan struct{}), started: time.Now(), epoch: cache.epoch}
		cache.flights[key] = flight
		cache.mu.Unlock()
		pan115.TraceUploadCache(ctx, "load")
		entries, err := load(ctx, key.parent)
		cache.mu.Lock()
		flight.entries, flight.err, flight.valid = entries, err, !flight.invalidated
		if err == nil && flight.valid && !flight.superseded {
			cache.putLocked(key, uploadDirectorySnapshot{entries: append([]uploadEntry(nil), entries...), started: flight.started})
		} else if err != nil && !uploadDirectoryCooldown(err) {
			cache.removeLocked(key)
		}
		delete(cache.flights, key)
		close(flight.done)
		valid := flight.valid
		cache.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if valid {
			return append([]uploadEntry(nil), entries...), nil
		}
	}
}

// written prevents new readers from reusing pre-write contents without restarting earlier verifiers.
// Earlier readers still enforce their own freshness barrier and verify the exact target identity.
func (cache *uploadDirectoryCache) written(scope, parent string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key := uploadDirectoryCacheKey{scope, parent}
	cache.removeLocked(key)
	if flight := cache.flights[key]; flight != nil {
		flight.superseded = true
	}
}

// uploadDirectoryCooldown identifies a rejected request, not an uncertain remote write.
func uploadDirectoryCooldown(err error) bool {
	var cooldown *pan115.UploadRetryError
	return errors.As(err, &cooldown)
}

// invalidateParent discards uncertain contents without evicting unrelated ancestor listings.
func (cache *uploadDirectoryCache) invalidateParent(scope, parent string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key := uploadDirectoryCacheKey{scope, parent}
	delete(cache.paths, key)
	cache.removeLocked(key)
	if flight := cache.flights[key]; flight != nil {
		flight.invalidated = true
	}
}

func (cache *uploadDirectoryCache) removeLocked(key uploadDirectoryCacheKey) {
	cache.entries -= len(cache.snapshots[key].entries)
	delete(cache.snapshots, key)
}

func (cache *uploadDirectoryCache) putLocked(key uploadDirectoryCacheKey, snapshot uploadDirectorySnapshot) {
	cache.removeLocked(key)
	if len(snapshot.entries) > uploadDirectoryEntryLimit {
		return
	}
	if cache.snapshots == nil {
		cache.snapshots = make(map[uploadDirectoryCacheKey]uploadDirectorySnapshot)
	}
	for len(cache.snapshots) >= uploadDirectoryCacheLimit || cache.entries+len(snapshot.entries) > uploadDirectoryEntryLimit {
		var oldest uploadDirectoryCacheKey
		var stamp time.Time
		for candidate, value := range cache.snapshots {
			if stamp.IsZero() || value.started.Before(stamp) {
				oldest, stamp = candidate, value.started
			}
		}
		cache.removeLocked(oldest)
	}
	cache.snapshots[key] = snapshot
	cache.entries += len(snapshot.entries)
}

// invalidate drops ancestor identities too after an external change or uncertain write.
func (cache *uploadDirectoryCache) invalidate(scope string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.epoch++
	for key := range cache.paths {
		if key.scope == scope {
			delete(cache.paths, key)
		}
	}
	for key, flight := range cache.flights {
		if key.scope == scope {
			flight.invalidated = true
		}
	}
	for key := range cache.snapshots {
		if key.scope == scope {
			cache.removeLocked(key)
		}
	}
}

// created publishes a new directory and empty contents atomically; reused directories are never seeded empty.
func (cache *uploadDirectoryCache) created(scope, parent, name, id string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	ancestors := cache.paths[uploadDirectoryCacheKey{scope, parent}]
	if parent == "0" {
		cache.rememberPathLocked(scope, id, []pan115.Directory{{ID: "0"}, {ID: id, Name: name}})
	} else if len(ancestors.path) > 0 && time.Since(ancestors.observed) < uploadDirectoryTTL {
		cache.rememberPathLocked(scope, id, append(append([]pan115.Directory(nil), ancestors.path...), pan115.Directory{ID: id, Name: name}))
	}
	for key, flight := range cache.flights {
		if key.scope == scope && (key.parent == parent || key.parent == id) {
			flight.invalidated = true
		}
	}
	key := uploadDirectoryCacheKey{scope, parent}
	if snapshot, ok := cache.snapshots[key]; ok {
		snapshot.entries = append(append([]uploadEntry(nil), snapshot.entries...), uploadEntry{ID: id, Name: name, Directory: true})
		cache.putLocked(key, snapshot)
	}
	cache.putLocked(uploadDirectoryCacheKey{scope, id}, uploadDirectorySnapshot{started: time.Now()})
}

// uploadFreshList requires a read begun after the barrier, never a pre-upload cache hit.
func uploadFreshList(ctx context.Context, remote uploadRemote, parent string, after time.Time) ([]uploadEntry, error) {
	if cached, ok := remote.(uploadDirectoryRemote); ok {
		return cached.service.directoryCache.list(ctx, uploadDirectoryCacheKey{cached.kind, parent}, after, cached.uploadRemote.List)
	}
	return remote.List(ctx, parent)
}
