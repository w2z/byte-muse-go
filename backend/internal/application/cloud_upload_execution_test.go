package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

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
	if _, err := service.process(context.Background(), mapping, files[0], "overwrite"); err == nil {
		t.Fatal("changed content deleted")
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

type uploadBlockingRemote struct {
	uploadMemoryRemote
	started chan string
	release chan struct{}
}

func (remote *uploadBlockingRemote) Upload(ctx context.Context, _ string, name string, _ *os.File, _ int64) error {
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
