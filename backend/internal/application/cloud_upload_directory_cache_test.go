package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func TestUploadOverwriteWithoutOldFilesUsesOneVerification(t *testing.T) {
	service, mapping, file, remote, store := recoveryFixture(t)
	remote.files = map[string][]byte{}
	counted := &uploadDirectoryCountingRemote{uploadRemote: remote}
	service.provider = func(context.Context, string) (uploadRemote, error) { return counted, nil }
	state, err := service.process(context.Background(), mapping, file, "overwrite")
	if err != nil || state != "completed" || counted.lists != 2 || remote.uploads != 1 {
		t.Fatalf("state=%s lists=%d uploads=%d err=%v", state, counted.lists, remote.uploads, err)
	}
	if snapshot := store[uploadKey(mapping, file, "overwrite")].Overwrite; snapshot == nil || len(snapshot.Files) != 0 {
		t.Fatal("empty frozen replacement set must remain durable")
	}
}

func TestUploadFiveNewFilesSharePreparation(t *testing.T) {
	service, mapping, _, remote, _ := recoveryFixture(t)
	remote.files = map[string][]byte{}
	counted := &uploadDirectoryCountingRemote{uploadRemote: remote}
	service.provider = func(context.Context, string) (uploadRemote, error) { return counted, nil }
	for index := 0; index < 5; index++ {
		name := fmt.Sprintf("%d.txt", index)
		absolute := filepath.Join(mapping.LocalPath, name)
		if err := os.WriteFile(absolute, []byte("new file"), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			t.Fatal(err)
		}
		file := uploadLocalFile{Absolute: absolute, Relative: name, Size: info.Size(), Modified: info.ModTime().UnixNano()}
		if state, err := service.process(context.Background(), mapping, file, "overwrite"); err != nil || state != "completed" {
			t.Fatalf("state=%s err=%v", state, err)
		}
	}
	if counted.lists != 6 || remote.uploads != 5 || len(remote.files) != 5 {
		t.Fatalf("want shared preparation + five verifications: lists=%d uploads=%d files=%d", counted.lists, remote.uploads, len(remote.files))
	}
}

func TestUploadDirectRetryResetsDeletedParent(t *testing.T) {
	service, mapping, file, _, store := recoveryFixture(t)
	key := uploadKey(mapping, file, "overwrite")
	store[key] = domain.UploadRecord{Key: key, Kind: "115", Root: mapping.ID, LocalRoot: mapping.LocalPath, Source: file.Absolute, Relative: file.Relative, Size: file.Size, Modified: file.Modified, Policy: "overwrite", State: "pending", Parent: "deleted", Stage: file.Relative, Target: file.Relative, Error: "previous failure"}
	remote := &uploadRecreateRemote{entries: map[string][]uploadEntry{"0": nil}}
	service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
	if _, err := service.process(context.Background(), mapping, file, "overwrite"); err == nil || store[key].Parent != "" {
		t.Fatalf("missing parent not reset: err=%v record=%+v", err, store[key])
	}
	if state, err := service.process(context.Background(), mapping, file, "overwrite"); err != nil || state != "completed" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

type uploadRetryCountingRemote struct {
	uploadRemote
	lists, uploads       int
	failUpload, failList error
	failAfterUpload      bool
}

func (remote *uploadRetryCountingRemote) List(ctx context.Context, parent string) ([]uploadEntry, error) {
	remote.lists++
	if remote.failList != nil && (!remote.failAfterUpload || remote.uploads > 0) {
		return nil, remote.failList
	}
	return remote.uploadRemote.List(ctx, parent)
}

func (remote *uploadRetryCountingRemote) Upload(ctx context.Context, parent, name string, file *os.File, size int64, hash string) error {
	remote.uploads++
	if remote.failUpload != nil {
		return remote.failUpload
	}
	return remote.uploadRemote.Upload(ctx, parent, name, file, size, hash)
}

func TestUploadCooldownPreservesPreparationAndRetryReadsOnce(t *testing.T) {
	service, mapping, file, remote, _ := recoveryFixture(t)
	remote.files = map[string][]byte{}
	counted := &uploadRetryCountingRemote{uploadRemote: remote, failUpload: &pan115.UploadRetryError{Until: time.Now().Add(time.Minute), Cause: &pan115.APIError{Code: 770004}}}
	service.provider = func(context.Context, string) (uploadRemote, error) { return counted, nil }
	if _, err := service.process(context.Background(), mapping, file, "overwrite"); err == nil {
		t.Fatal("expected cooldown")
	}
	prepared := uploadDirectoryRemote{uploadRemote: counted, service: service, kind: "115"}
	if _, err := prepared.List(context.Background(), "0"); err != nil || counted.lists != 1 {
		t.Fatalf("cooldown discarded valid preparation: lists=%d err=%v", counted.lists, err)
	}
	counted.failUpload = nil
	service.directoryCache.invalidate("115")
	before := counted.lists
	state, err := service.process(context.Background(), mapping, file, "overwrite")
	if err != nil || state != "completed" || counted.lists-before != 2 || remote.uploads != 1 {
		t.Fatalf("retry should read once before upload and once after: state=%s lists=%d uploads=%d err=%v", state, counted.lists-before, remote.uploads, err)
	}
}

func TestUploadVerificationFailureResumesWithoutReupload(t *testing.T) {
	service, mapping, file, remote, store := recoveryFixture(t)
	remote.files = map[string][]byte{}
	counted := &uploadRetryCountingRemote{uploadRemote: remote, failAfterUpload: true, failList: errors.New("read failed")}
	service.provider = func(context.Context, string) (uploadRemote, error) { return counted, nil }
	if _, err := service.process(context.Background(), mapping, file, "overwrite"); err == nil {
		t.Fatal("expected failed verification")
	}
	counted.failList = nil
	restarted := &UploadService{store: store, provider: service.provider}
	before := counted.lists
	state, err := restarted.process(context.Background(), mapping, file, "overwrite")
	if err != nil || state != "completed" || counted.lists-before != 1 || counted.uploads != 1 {
		t.Fatalf("restart should only reconcile: state=%s lists=%d uploads=%d err=%v", state, counted.lists-before, counted.uploads, err)
	}
}

func TestUploadCleanupRetryOnlyReconcilesRecordedOldFiles(t *testing.T) {
	service, mapping, file, _, store := recoveryFixture(t)
	remote := &directUploadRemote{entries: []uploadEntry{{ID: "old", Name: file.Relative, Size: 3, SHA1: "old-hash"}}, deleteFailure: true}
	counted := &uploadDirectoryCountingRemote{uploadRemote: remote}
	service.provider = func(context.Context, string) (uploadRemote, error) { return counted, nil }
	if _, err := service.process(context.Background(), mapping, file, "overwrite"); err == nil || store[uploadKey(mapping, file, "overwrite")].State != "cleanup" {
		t.Fatal("cleanup failure not persisted")
	}
	remote.deleteFailure = false
	restarted := &UploadService{store: store, provider: service.provider}
	if state, err := restarted.process(context.Background(), mapping, file, "overwrite"); err != nil || state != "completed" || len(remote.names) != 1 || len(remote.entries) != 1 || remote.entries[0].ID != "new-1" {
		t.Fatalf("cleanup reuploaded or lost identity: state=%s err=%v names=%v entries=%v", state, err, remote.names, remote.entries)
	}
}

func TestUploadFailedFreshReadPreservesOnlySafeCachedPreparation(t *testing.T) {
	for _, cooldown := range []bool{true, false} {
		t.Run(fmt.Sprint(cooldown), func(t *testing.T) {
			cache := &uploadDirectoryCache{}
			key := uploadDirectoryCacheKey{"account", "parent"}
			calls := 0
			load := func(context.Context, string) ([]uploadEntry, error) {
				calls++
				return []uploadEntry{{Name: "original"}}, nil
			}
			_, _ = cache.list(context.Background(), key, time.Time{}, load)
			failure := error(errors.New("read failed"))
			if cooldown {
				failure = &pan115.UploadRetryError{Until: time.Now().Add(time.Minute)}
			}
			if _, err := cache.list(context.Background(), key, time.Now(), func(context.Context, string) ([]uploadEntry, error) {
				return nil, failure
			}); err == nil {
				t.Fatal("failed verification accepted cached data")
			}
			_, _ = cache.list(context.Background(), key, time.Time{}, load)
			want := 2
			if cooldown {
				want = 1
			}
			if calls != want {
				t.Fatalf("loads=%d want=%d", calls, want)
			}
		})
	}
}

func TestUploadSuccessfulWriteInvalidatesPreparationBeforeRateLimitedVerification(t *testing.T) {
	service, mapping, file, remote, _ := recoveryFixture(t)
	remote.files = map[string][]byte{}
	counted := &uploadRetryCountingRemote{uploadRemote: remote, failAfterUpload: true, failList: &pan115.UploadRetryError{Until: time.Now().Add(time.Minute)}}
	service.provider = func(context.Context, string) (uploadRemote, error) { return counted, nil }
	if _, err := service.process(context.Background(), mapping, file, "overwrite"); err == nil {
		t.Fatal("expected verification cooldown")
	}
	counted.failList = nil
	prepared := uploadDirectoryRemote{uploadRemote: counted, service: service, kind: "115"}
	entries, err := prepared.List(context.Background(), "0")
	if err != nil || len(entries) != 1 || entries[0].Name != file.Relative {
		t.Fatalf("pre-upload absence survived successful write: %v %v", entries, err)
	}
}

func TestUploadUncertainWriteKeepsUnrelatedDirectoryCached(t *testing.T) {
	service, mapping, file, remote, _ := recoveryFixture(t)
	remote.files = map[string][]byte{}
	counted := &uploadRetryCountingRemote{uploadRemote: remote, failUpload: errors.New("response lost")}
	service.provider = func(context.Context, string) (uploadRemote, error) { return counted, nil }
	prepared := uploadDirectoryRemote{uploadRemote: counted, service: service, kind: "115"}
	_, _ = prepared.List(context.Background(), "unrelated")
	if _, err := service.process(context.Background(), mapping, file, "overwrite"); err == nil {
		t.Fatal("expected failure")
	}
	before := counted.lists
	_, _ = prepared.List(context.Background(), "unrelated")
	if counted.lists != before {
		t.Fatal("unrelated snapshot evicted")
	}
	_, _ = prepared.List(context.Background(), "0")
	if counted.lists != before+1 {
		t.Fatal("uncertain upload reused stale snapshot")
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
