package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUploadDirectoryCacheCoalescesAndSeparatesAccounts(t *testing.T) {
	cache := &uploadDirectoryCache{}
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context, parent string) ([]uploadEntry, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []uploadEntry{{ID: parent, Name: "first"}}, nil
	}
	var workers sync.WaitGroup
	for index := 0; index < 5; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			entries, err := cache.list(context.Background(), uploadDirectoryCacheKey{"account-a", "0"}, time.Time{}, load)
			if err != nil || len(entries) != 1 {
				t.Errorf("entries=%v err=%v", entries, err)
			}
		}()
	}
	<-entered
	close(release)
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("five workers issued %d queries", calls.Load())
	}
	if _, err := cache.list(context.Background(), uploadDirectoryCacheKey{"account-b", "0"}, time.Time{}, load); err != nil || calls.Load() != 2 {
		t.Fatalf("account caches mixed: %d %v", calls.Load(), err)
	}
}

func TestUploadDirectoryCacheFreshBarrierAndExpiry(t *testing.T) {
	cache := &uploadDirectoryCache{}
	key := uploadDirectoryCacheKey{"account", "0"}
	calls := 0
	load := func(context.Context, string) ([]uploadEntry, error) {
		calls++
		return []uploadEntry{{Name: fmt.Sprint(calls)}}, nil
	}
	_, _ = cache.list(context.Background(), key, time.Time{}, load)
	barrier := time.Now()
	entries, err := cache.list(context.Background(), key, barrier, load)
	if err != nil || calls != 2 || entries[0].Name != "2" {
		t.Fatal("pre-write snapshot reused", entries, err)
	}
	entries[0].Name = "mutated"
	entries, _ = cache.list(context.Background(), key, time.Time{}, load)
	if entries[0].Name != "2" {
		t.Fatal("caller mutated shared snapshot")
	}
	snapshot := cache.snapshots[key]
	snapshot.started = time.Now().Add(-uploadDirectoryTTL)
	cache.snapshots[key] = snapshot
	_, _ = cache.list(context.Background(), key, time.Time{}, load)
	if calls != 3 {
		t.Fatal("expired snapshot reused")
	}
	cache.invalidate(key.scope)
	_, _ = cache.list(context.Background(), key, time.Time{}, load)
	if calls != 4 {
		t.Fatal("invalidated snapshot reused")
	}
}

func TestUploadDirectoryCacheDoesNotCachePartialFailure(t *testing.T) {
	cache := &uploadDirectoryCache{}
	key := uploadDirectoryCacheKey{"account", "0"}
	calls := 0
	load := func(context.Context, string) ([]uploadEntry, error) {
		calls++
		if calls == 1 {
			return []uploadEntry{{Name: "partial"}}, errors.New("second page failed")
		}
		return []uploadEntry{{Name: "complete"}}, nil
	}
	if _, err := cache.list(context.Background(), key, time.Time{}, load); err == nil {
		t.Fatal("partial response accepted")
	}
	entries, err := cache.list(context.Background(), key, time.Time{}, load)
	if err != nil || calls != 2 || entries[0].Name != "complete" {
		t.Fatal(entries, err)
	}
}

func TestUploadDirectoryCacheReadBeforeUploadCannotVerifyIt(t *testing.T) {
	cache := &uploadDirectoryCache{}
	key := uploadDirectoryCacheKey{"account", "0"}
	var calls atomic.Int32
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	load := func(context.Context, string) ([]uploadEntry, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return nil, nil
		}
		return []uploadEntry{{Name: "uploaded"}}, nil
	}
	go func() { defer close(done); _, _ = cache.list(context.Background(), key, time.Time{}, load) }()
	<-entered
	barrier := time.Now()
	close(release)
	entries, err := cache.list(context.Background(), key, barrier, load)
	<-done
	if err != nil || len(entries) != 1 || calls.Load() != 2 {
		t.Fatalf("stale verification: entries=%v calls=%d err=%v", entries, calls.Load(), err)
	}
}

func TestUploadDirectoryCacheBoundsAndReservationCancellation(t *testing.T) {
	cache := &uploadDirectoryCache{}
	for index := 0; index < uploadDirectoryCacheLimit+10; index++ {
		cache.created("account", "0", fmt.Sprint(index), fmt.Sprint(index+1))
	}
	if len(cache.snapshots) > uploadDirectoryCacheLimit || cache.entries > uploadDirectoryEntryLimit {
		t.Fatal("cache exceeded limits")
	}
	release, err := cache.reserve(context.Background(), "same-name")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = cache.reserve(ctx, "same-name"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	other, err := cache.reserve(context.Background(), "other-name")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	if len(cache.reservations) != 0 {
		t.Fatal("reservations leaked")
	}
}

func TestUploadDirectoryCacheMutationDiscardsInflightSnapshot(t *testing.T) {
	cache := &uploadDirectoryCache{}
	key := uploadDirectoryCacheKey{"account", "0"}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context, string) ([]uploadEntry, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return []uploadEntry{{Name: "old"}}, nil
		}
		return []uploadEntry{{Name: "new"}}, nil
	}
	go func() {
		defer close(done)
		entries, err := cache.list(context.Background(), key, time.Time{}, load)
		if err != nil || len(entries) != 1 || entries[0].Name != "new" {
			t.Errorf("stale response after invalidation: %v %v", entries, err)
		}
	}()
	<-entered
	cache.invalidate(key.scope)
	close(release)
	<-done
}

type uploadDirectoryCountingRemote struct {
	uploadRemote
	lists int
}

func (remote *uploadDirectoryCountingRemote) List(ctx context.Context, parent string) ([]uploadEntry, error) {
	remote.lists++
	return remote.uploadRemote.List(ctx, parent)
}

func TestUploadSharedPreparationStillVerifiesEachUploadedFile(t *testing.T) {
	service, mapping, file, remote, _ := recoveryFixture(t)
	remote.files = map[string][]byte{}
	counted := &uploadDirectoryCountingRemote{uploadRemote: remote}
	service.provider = func(context.Context, string) (uploadRemote, error) { return counted, nil }
	state, err := service.process(context.Background(), mapping, file, "skip")
	if err != nil || state != "completed" || counted.lists != 2 {
		t.Fatalf("want one preparation + one post-upload read: state=%s lists=%d err=%v", state, counted.lists, err)
	}
	if remote.uploads != 1 {
		t.Fatal("upload not performed")
	}
}

func TestUploadDirectoryCacheRestartCannotReuseEmptyDirectory(t *testing.T) {
	cache := &uploadDirectoryCache{}
	cache.created("account", "0", "child", "1")
	calls := 0
	load := func(context.Context, string) ([]uploadEntry, error) {
		calls++
		return []uploadEntry{{Name: "external"}}, nil
	}
	_, _ = cache.list(context.Background(), uploadDirectoryCacheKey{"account", "1"}, time.Time{}, load)
	if calls != 0 {
		t.Fatal("new directory not seeded")
	}
	restarted := &uploadDirectoryCache{}
	entries, err := restarted.list(context.Background(), uploadDirectoryCacheKey{"account", "1"}, time.Time{}, load)
	if err != nil || calls != 1 || len(entries) != 1 {
		t.Fatal("restart used stale empty state", entries, err)
	}
}

func TestUploadDirectoryCreationSharedByFiveWorkers(t *testing.T) {
	service := &UploadService{}
	probe := &uploadPreparationProbe{}
	remote := uploadDirectoryRemote{uploadRemote: probe, service: service, kind: "account"}
	var workers sync.WaitGroup
	for index := 0; index < 5; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			id, err := remote.Mkdir(context.Background(), "0", "child")
			if err != nil || id != "0/child" {
				t.Errorf("id=%s err=%v", id, err)
			}
		}()
	}
	workers.Wait()
	if len(probe.lists) != 1 || len(probe.created) != 1 {
		t.Fatalf("duplicate creation or reads: lists=%v created=%v", probe.lists, probe.created)
	}
}

func TestUploadDirectoryFreshVerificationSharedAfterBarrier(t *testing.T) {
	cache := &uploadDirectoryCache{}
	key := uploadDirectoryCacheKey{"account", "0"}
	barrier := time.Now().Add(-time.Second)
	var calls atomic.Int32
	load := func(context.Context, string) ([]uploadEntry, error) {
		calls.Add(1)
		return []uploadEntry{{Name: "completed"}}, nil
	}
	var workers sync.WaitGroup
	for index := 0; index < 5; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			entries, err := cache.list(context.Background(), key, barrier, load)
			if err != nil || len(entries) != 1 {
				t.Errorf("entries=%v err=%v", entries, err)
			}
		}()
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("verification not coalesced: %d", calls.Load())
	}
}
