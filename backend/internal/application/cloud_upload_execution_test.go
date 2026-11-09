package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUploadMovePrunesOnlyEmptyParents(t *testing.T) {
	for _, keepSibling := range []bool{false, true} {
		t.Run(fmt.Sprint(keepSibling), func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "album", "disc")
			if err := os.MkdirAll(parent, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "album", "empty", "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			content := []byte("content")
			source := filepath.Join(parent, "file.txt")
			if err := os.WriteFile(source, content, 0600); err != nil {
				t.Fatal(err)
			}
			if keepSibling {
				if err := os.WriteFile(filepath.Join(root, "album", "keep.txt"), content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			info, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			record := &domain.UploadRecord{Mode: "move", LocalRoot: root, Relative: "album/disc/file.txt", Source: source, Size: info.Size(), Modified: info.ModTime().UnixNano(), SHA1: fmt.Sprintf("%x", sha1.Sum(content)), StageID: "verified"}
			for attempt := 0; attempt < 2; attempt++ {
				if err := removeUploadedSource(context.Background(), record); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(parent); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("empty directory remains: %v", err)
			}
			_, err = os.Stat(filepath.Join(root, "album"))
			if keepSibling && err != nil || !keepSibling && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("parent protection: %v", err)
			}
			if _, err := os.Stat(root); err != nil {
				t.Fatal("mapping root removed", err)
			}
		})
	}
}

func TestUploadMoveRecoversEmptyDirectoryCleanup(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "album")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	record := &domain.UploadRecord{Mode: "move", LocalRoot: root, Relative: "album/file.txt", Source: filepath.Join(directory, "file.txt"), SHA1: "verified", StageID: "verified"}
	if err := removeUploadedSource(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing source did not clean empty parent", err)
	}
	if err := os.WriteFile(directory, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := pruneUploadedParents(context.Background(), opened, "album/file.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatal("replacement file removed", err)
	}
}

// TestUploadMoveOnlyDeletesVerifiedSource covers copy compatibility, skip protection and idempotent move receipts.
func TestUploadMoveOnlyDeletesVerifiedSource(t *testing.T) {
	for _, mode := range []string{"", "copy", "move"} {
		for _, policy := range []string{"overwrite", "skip"} {
			t.Run(mode+policy, func(t *testing.T) {
				mapping := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: t.TempDir()}
				name := filepath.Join(mapping.LocalPath, "file.txt")
				if err := os.WriteFile(name, []byte("content"), 0600); err != nil {
					t.Fatal(err)
				}
				files, err := uploadSnapshot(context.Background(), mapping)
				if err != nil {
					t.Fatal(err)
				}
				file := files[0]
				key := uploadKey(mapping, file, policy)
				store := uploadMemoryStore{key: {Key: key, Kind: "115", Root: "0", LocalRoot: mapping.LocalPath, Source: name, Relative: file.Relative, Size: file.Size, Modified: file.Modified, Policy: policy, Mode: mode, State: "pending"}}
				remote := &uploadMemoryRemote{files: map[string][]byte{}}
				if policy == "skip" {
					remote.files["file.txt"] = []byte("other")
				}
				service := &UploadService{store: store, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
				for attempt := 0; attempt < 2; attempt++ {
					if _, err = service.process(context.Background(), mapping, file, policy); err != nil {
						t.Fatal(err)
					}
				}
				_, err = os.Stat(name)
				removed := errors.Is(err, os.ErrNotExist)
				if removed != (mode == "move" && policy != "skip") {
					t.Fatalf("mode=%s policy=%s removed=%v", mode, policy, removed)
				}
				if remote.uploads > 1 {
					t.Fatal("duplicate upload")
				}
			})
		}
	}
}

// TestUploadMoveRecoveryRejectsChangedSource proves equal size/time alone cannot authorize deletion.
func TestUploadMoveRecoveryRejectsChangedSource(t *testing.T) {
	mapping := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: t.TempDir()}
	name := filepath.Join(mapping.LocalPath, "file.txt")
	if err := os.WriteFile(name, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	files, _ := uploadSnapshot(context.Background(), mapping)
	store := uploadMemoryStore{}
	remote := &uploadMemoryRemote{files: map[string][]byte{}}
	service := &UploadService{store: store, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	if _, err := service.process(context.Background(), mapping, files[0], "overwrite"); err != nil {
		t.Fatal(err)
	}
	key := uploadKey(mapping, files[0], "overwrite")
	record := store[key]
	record.Mode = "move"
	record.State = "cleanup"
	record.CompletedAt = ""
	if err := os.WriteFile(name, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(0, record.Modified)
	if err := os.Chtimes(name, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	store[key] = record
	if _, err := service.process(context.Background(), mapping, files[0], "overwrite"); !errors.Is(err, errLocalUploadConflict) {
		t.Fatalf("changed content was not classified as a local conflict: %v", err)
	}
	if uploadRetryDue(store[key], time.Now()) {
		t.Fatal("changed source scheduled for automatic retry")
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatal(err)
	}
	if remote.uploads != 1 {
		t.Fatal("cleanup reuploaded")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if state, err := service.process(context.Background(), mapping, files[0], "overwrite"); err != nil || state != "completed" {
		t.Fatalf("missing source recovery: %s %v", state, err)
	}
}

// A producer that ignores advisory locks must not cause deletion of newly written content.
func TestUploadMoveConcurrentWriter(t *testing.T) {
	s, m, f, _, store := recoveryFixture(t)
	key := uploadKey(m, f, "overwrite")
	store[key] = domain.UploadRecord{Key: key, Kind: "115", Root: m.ID, LocalRoot: m.LocalPath, Source: f.Absolute, Relative: f.Relative, Size: f.Size, Modified: f.Modified, Mode: "move", Policy: "overwrite", State: "pending"}
	remote := &receiptUploadRemote{}
	remote.afterDirectUpload = func() {
		writer, err := os.OpenFile(f.Absolute, os.O_WRONLY, 0)
		if runtime.GOOS == "windows" {
			if err == nil {
				writer.Close()
				t.Fatal("write protection released before cleanup")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		_, err = writer.WriteAt([]byte("X"), 0)
		writer.Close()
		if err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(0, f.Modified)
		if err := os.Chtimes(f.Absolute, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	s.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
	state, err := s.process(context.Background(), m, f, "overwrite")
	if runtime.GOOS == "windows" {
		if err != nil || state != "completed" {
			t.Fatalf("protected move: %s %v", state, err)
		}
		if _, err := os.Stat(f.Absolute); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("source remains", err)
		}
	} else {
		if !errors.Is(err, errLocalUploadConflict) || uploadRetryDue(store[key], time.Now()) {
			t.Fatalf("changed content accepted: %s %v", state, err)
		}
		body, err := os.ReadFile(f.Absolute)
		if err != nil || body[0] != 'X' {
			t.Fatal("changed source lost", err)
		}
	}
}

// Continuous Windows write protection permits one hash; advisory locks still require cleanup verification.
func TestUploadMoveHashPhases(t *testing.T) {
	for _, persistedHash := range []bool{false, true} {
		t.Run(fmt.Sprint(persistedHash), func(t *testing.T) {
			s, m, f, _, store := recoveryFixture(t)
			key := uploadKey(m, f, "overwrite")
			r := domain.UploadRecord{Key: key, Kind: "115", Root: m.ID, LocalRoot: m.LocalPath, Source: f.Absolute, Relative: f.Relative, Size: f.Size, Modified: f.Modified, Mode: "move", Policy: "overwrite", State: "pending"}
			if persistedHash {
				body, err := os.ReadFile(f.Absolute)
				if err != nil {
					t.Fatal(err)
				}
				r.SHA1 = fmt.Sprintf("%x", sha1.Sum(body))
			}
			store[key] = r
			remote := &receiptUploadRemote{}
			s.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
			verifications, cleanups := 0, 0
			ctx := domain.WithUploadProgress(context.Background(), func(done, total int64, phase string) {
				if done == 0 && phase == "verifying" {
					verifications++
				}
				if done == 0 && phase == "cleanup" {
					cleanups++
				}
			})
			state, err := s.process(ctx, m, f, "overwrite")
			if err != nil || state != "completed" {
				t.Fatalf("move=%s %v", state, err)
			}
			wantHash := 1
			if persistedHash {
				wantHash = 0
			}
			wantCleanup := 1
			if runtime.GOOS == "windows" && !persistedHash {
				wantCleanup = 0
			}
			if verifications != wantHash || cleanups != wantCleanup {
				t.Fatalf("hash phases=%d cleanup phases=%d", verifications, cleanups)
			}
			if _, err := os.Stat(f.Absolute); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("source remains", err)
			}
		})
	}
}

type uploadBlockingRemote struct {
	uploadMemoryRemote
	started chan string
	release chan struct{}
}

type uploadParallelPreparationRemote struct {
	uploadBlockingRemote
	listed  chan struct{}
	prepare chan struct{}
	mu      sync.Mutex
	entries []uploadEntry
}

func (remote *uploadParallelPreparationRemote) List(ctx context.Context, _ string) ([]uploadEntry, error) {
	remote.listed <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-remote.prepare:
		remote.mu.Lock()
		defer remote.mu.Unlock()
		return append([]uploadEntry(nil), remote.entries...), nil
	}
}

func (remote *uploadParallelPreparationRemote) Upload(ctx context.Context, _ string, name string, _ *os.File, size int64, hash string) error {
	remote.started <- name
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-remote.release:
		remote.mu.Lock()
		defer remote.mu.Unlock()
		remote.entries = append(remote.entries, uploadEntry{ID: name, Name: name, Size: size, SHA1: hash})
		return nil
	}
}

func TestUploadPreparationParallelAndCompletionRefills(t *testing.T) {
	mapping := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: t.TempDir()}
	store := &uploadCountedStore{uploadMemoryStore: uploadMemoryStore{}}
	for index := 0; index < 6; index++ {
		name := filepath.Join(mapping.LocalPath, fmt.Sprintf("%02d.txt", index))
		if err := os.WriteFile(name, []byte("a"), 0600); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(name)
		file := uploadLocalFile{Absolute: name, Relative: filepath.Base(name), Size: 1, Modified: info.ModTime().UnixNano()}
		key := uploadKey(mapping, file, "overwrite")
		store.uploadMemoryStore[key] = domain.UploadRecord{Key: key, Kind: "115", Root: "0", LocalRoot: mapping.LocalPath, RemotePath: "/", Relative: file.Relative, Source: name, Size: 1, Modified: file.Modified, Policy: "overwrite", State: "pending", Intent: "run", QueuedAt: time.Unix(int64(index), 0).UTC().Format(time.RFC3339Nano)}
	}
	raw, _ := json.Marshal([]UploadMapping{mapping})
	remote := &uploadParallelPreparationRemote{uploadBlockingRemote: uploadBlockingRemote{started: make(chan string, 16), release: make(chan struct{}, 16)}, listed: make(chan struct{}, 64), prepare: make(chan struct{})}
	service := &UploadService{store: store, settings: func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_PATHS": string(raw)}, nil
	}, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); service.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-remote.listed:
	case <-time.After(6 * time.Second):
		t.Fatal("shared directory preparation did not start")
	}
	close(remote.prepare)
	for index := 0; index < 5; index++ {
		select {
		case <-remote.started:
		case <-time.After(3 * time.Second):
			t.Fatal("five transfers did not start")
		}
	}
	time.Sleep(2200 * time.Millisecond)
	remote.release <- struct{}{}
	select {
	case name := <-remote.started:
		if name != "05.txt" {
			t.Fatalf("expected next queued file, got %s", name)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("completion did not immediately refill slot")
	}
	cancel()
	<-done
	if store.lists != 1 {
		t.Fatalf("scheduler reloaded full database %d times", store.lists)
	}
}

func (remote *uploadBlockingRemote) Upload(ctx context.Context, _ string, name string, _ *os.File, _ int64, _ string) error {
	remote.started <- name
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-remote.release:
		return errors.New("test transfer failure")
	}
}

// TestUploadConcurrencyChangesWithoutCancel verifies default five, live growth and non-destructive shrinking.
func TestUploadConcurrencyChangesWithoutCancel(t *testing.T) {
	mapping := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: t.TempDir()}
	store := uploadMemoryStore{}
	for index := 0; index < 9; index++ {
		name := filepath.Join(mapping.LocalPath, fmt.Sprintf("%02d.txt", index))
		if err := os.WriteFile(name, []byte("a"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := uploadSnapshot(context.Background(), mapping)
	for index, file := range files {
		key := uploadKey(mapping, file, "overwrite")
		store[key] = domain.UploadRecord{Key: key, Kind: "115", Root: "0", LocalRoot: mapping.LocalPath, RemotePath: "/", Source: file.Absolute, Relative: file.Relative, Size: file.Size, Modified: file.Modified, Policy: "overwrite", State: "pending", Intent: "run", QueuedAt: time.Unix(int64(index), 0).UTC().Format(time.RFC3339Nano)}
	}
	raw, _ := json.Marshal([]UploadMapping{mapping})
	var limit atomic.Value
	limit.Store("")
	remote := &uploadBlockingRemote{uploadMemoryRemote: uploadMemoryRemote{files: map[string][]byte{}}, started: make(chan string, 32), release: make(chan struct{}, 32)}
	service := &UploadService{store: store, settings: func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_PATHS": string(raw), "CLOUD_UPLOAD_CONCURRENCY": limit.Load().(string)}, nil
	}, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); service.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitStarted := func(count int) {
		t.Helper()
		for index := 0; index < count; index++ {
			select {
			case <-remote.started:
			case <-time.After(8 * time.Second):
				t.Fatal("worker not started")
			}
		}
	}
	waitStarted(5)
	select {
	case name := <-remote.started:
		t.Fatalf("exceeded default limit: %s", name)
	case <-time.After(1200 * time.Millisecond):
	}
	limit.Store("7")
	waitStarted(2)
	limit.Store("2")
	time.Sleep(1200 * time.Millisecond)
	service.mu.RLock()
	active := len(service.active)
	service.mu.RUnlock()
	if active != 7 {
		t.Fatalf("shrinking canceled active transfers: %d", active)
	}
	for index := 0; index < 5; index++ {
		remote.release <- struct{}{}
	}
	time.Sleep(1200 * time.Millisecond)
	select {
	case name := <-remote.started:
		t.Fatalf("started before draining: %s", name)
	default:
	}
}
