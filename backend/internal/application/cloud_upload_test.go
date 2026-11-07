package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type uploadMemoryStore map[string]domain.UploadRecord

type uploadPreparationProbe struct {
	uploadRemote
	lists   []string
	created []string
}

func (remote *uploadPreparationProbe) List(_ context.Context, parent string) ([]uploadEntry, error) {
	remote.lists = append(remote.lists, parent)
	return []uploadEntry{}, nil
}

func (remote *uploadPreparationProbe) Mkdir(_ context.Context, parent, name string) (string, error) {
	id := parent + "/" + name
	remote.created = append(remote.created, id)
	return id, nil
}

func TestUploadNewParentSkipsDescendantExistenceQueries(t *testing.T) {
	remote := &uploadPreparationProbe{}
	prepared := &uploadPreparationRemote{uploadRemote: remote, entries: map[string][]uploadEntry{}}
	ctx := context.Background()
	root, err := uploadMappingRoot(ctx, prepared, UploadMapping{Kind: "115", ID: "0"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := uploadParent(ctx, prepared, root, "new/child/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := prepared.List(ctx, parent)
	if err != nil || len(entries) != 0 || len(remote.lists) != 1 || len(remote.created) != 2 {
		t.Fatalf("unexpected lookups: lists=%v created=%v err=%v", remote.lists, remote.created, err)
	}
	fresh := &uploadPreparationRemote{uploadRemote: remote, entries: map[string][]uploadEntry{}}
	if _, err = fresh.List(ctx, root); err != nil || len(remote.lists) != 2 {
		t.Fatal("preparation snapshot leaked into next attempt")
	}
}

type uploadDelayedAdmissionStore struct {
	uploadMemoryStore
	blocked chan struct{}
	release chan struct{}
	saves   atomic.Int32
}

func (store *uploadDelayedAdmissionStore) Save(ctx context.Context, record domain.UploadRecord) error {
	if record.State == "pending" && store.saves.Add(1) == 2 {
		close(store.blocked)
		select {
		case <-store.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return store.uploadMemoryStore.Save(ctx, record)
}

func TestUploadDiscoveryPublishesBeforeScanCompletes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("a"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal([]UploadMapping{{Kind: "115", ID: "0", Path: "/", LocalPath: root}})
	store := &uploadDelayedAdmissionStore{uploadMemoryStore: uploadMemoryStore{}, blocked: make(chan struct{}), release: make(chan struct{})}
	started := make(chan struct{}, 8)
	service := &UploadService{store: store, settings: func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_ENABLE": "true", "CLOUD_UPLOAD_PATHS": string(raw)}, nil
	}, provider: func(context.Context, string) (uploadRemote, error) {
		started <- struct{}{}
		return nil, errors.New("test offline")
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); service.Run(ctx) }()
	defer func() { cancel(); close(store.release); <-done }()
	select {
	case <-store.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach second file")
	}
	if service.Files(1, 20, "").Total != 1 || service.DirectoryPage("").Total != 1 || service.Status().Total != 1 {
		t.Fatal("first persisted file hidden until entire scan finishes")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upload waited for remaining scan admission")
	}
	if service.Status().Actions["scan"] {
		t.Fatal("duplicate scan enabled during discovery")
	}
}

var uploadMemoryStoreMu sync.Mutex

func TestManualUploadIntentSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]UploadMapping{{Kind: "115", ID: "0", Path: "/", LocalPath: root}})
	store := uploadMemoryStore{}
	settings := func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_ENABLE": "false", "CLOUD_UPLOAD_PATHS": string(raw)}, nil
	}
	started := make(chan struct{}, 10)
	provider := func(ctx context.Context, _ string) (uploadRemote, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	run := func() (*UploadService, context.CancelFunc, chan struct{}) {
		service := &UploadService{store: store, settings: settings, provider: provider}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); service.Run(ctx) }()
		return service, cancel, done
	}
	service, cancel, done := run()
	deadline := time.Now().Add(5 * time.Second)
	for !service.Status().Actions["scan"] {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := service.Control(context.Background(), "scan"); err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("manual upload not started")
	}
	cancel()
	<-done
	_, cancel, done = run()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("manual queue lost execution intent on restart")
	}
}

func (s uploadMemoryStore) List(context.Context) ([]domain.UploadRecord, error) {
	uploadMemoryStoreMu.Lock()
	defer uploadMemoryStoreMu.Unlock()
	result := []domain.UploadRecord{}
	for _, r := range s {
		result = append(result, r)
	}
	return result, nil
}

func (s uploadMemoryStore) PendingCommits(context.Context) ([]domain.UploadRecord, error) {
	uploadMemoryStoreMu.Lock()
	defer uploadMemoryStoreMu.Unlock()
	result := []domain.UploadRecord{}
	for _, r := range s {
		if !uploadTerminal(r.State) && r.State != "control" {
			result = append(result, r)
		}
	}
	return result, nil
}

func (s uploadMemoryStore) Get(_ context.Context, key string) (*domain.UploadRecord, error) {
	uploadMemoryStoreMu.Lock()
	defer uploadMemoryStoreMu.Unlock()
	v, ok := s[key]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

// TestUploadMonitorDiscoveryAndStop checks existing/new files and cancellation without a real provider.
func TestUploadMonitorDiscoveryAndStop(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]UploadMapping{{Kind: "115", ID: "0", Path: "/", LocalPath: root}})
	var enabled atomic.Bool
	enabled.Store(true)
	s := &UploadService{store: uploadMemoryStore{}, settings: func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_ENABLE": fmt.Sprint(enabled.Load()), "CLOUD_UPLOAD_PATHS": string(raw)}, nil
	}, provider: func(context.Context, string) (uploadRemote, error) { return nil, errors.New("test provider offline") }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("monitor timed out")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	wait(func() bool { return s.Files(1, 20, "").Total == 1 })
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return s.Files(1, 20, "").Total == 2 })
	enabled.Store(false)
	wait(func() bool { return !s.Status().Enabled })
	files := s.Files(1, 20, "")
	if files.Total != 2 || (files.Items[0].State != "stopped" && files.Items[0].State != "failed") {
		t.Fatalf("lost stopped snapshot: %+v", files)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not stop")
	}
}

// TestManualUploadScanWithoutMonitoring starts one saved-directory pass without enabling continuous monitoring.
func TestManualUploadScanWithoutMonitoring(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]UploadMapping{{Kind: "115", ID: "0", Path: "/", LocalPath: root}})
	store := uploadMemoryStore{}
	s := &UploadService{store: store, settings: func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_ENABLE": "false", "CLOUD_UPLOAD_PATHS": string(raw), "CLOUD_UPLOAD_CONFLICT": "skip"}, nil
	}, provider: func(context.Context, string) (uploadRemote, error) {
		return nil, errors.New("test provider offline")
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("manual scan timed out")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	wait(func() bool { return s.Status().Actions["scan"] })
	if s.Files(1, 20, "").Total != 0 || s.Status().Enabled {
		t.Fatal("disabled monitoring discovered files")
	}
	if err := s.Control(ctx, "scan"); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return s.Files(1, 20, "").Total == 1 })
	deadline := time.Now().Add(25 * time.Second)
	for {
		files := s.Files(1, 20, "")
		if files.Total == 1 && files.Items[0].State == "failed" && files.Items[0].Retries == uploadMaxAutoRetries && strings.Contains(files.Items[0].Error, "test provider offline") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("manual scan did not exhaust retries: %+v", files.Items)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s.Status().Enabled {
		t.Fatal("manual scan enabled monitoring")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not stop")
	}
}

// TestManualUploadRestartsClearedTasks keeps automatic deduplication but admits an explicit new batch.
func TestManualUploadRestartsClearedTasks(t *testing.T) {
	for _, previousState := range []string{"completed", "skipped", "stopped", "pending"} {
		t.Run(previousState, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("abc"), 0600); err != nil {
				t.Fatal(err)
			}
			mapping := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: root}
			files, err := uploadSnapshot(context.Background(), mapping)
			if err != nil {
				t.Fatal(err)
			}
			file := files[0]
			key := uploadKey(mapping, file, "overwrite")
			stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			record := domain.UploadRecord{Key: key, Kind: "115", Root: "0", LocalRoot: root, RemotePath: "/", Source: file.Absolute, Relative: file.Relative, Size: file.Size, Modified: file.Modified, Policy: "overwrite", State: previousState, Hidden: true, Intent: "delete", Mode: "copy", QueuedAt: stamp, CompletedAt: stamp, UpdatedAt: stamp, Parent: "old-parent", StageID: "old-id", SHA1: "old-hash", Retries: 3, Error: "old error"}
			store := uploadMemoryStore{key: record, uploadControlKey: {Key: uploadControlKey, State: "control", Intent: "stopped"}}
			raw, _ := json.Marshal([]UploadMapping{mapping})
			service := &UploadService{store: store, settings: func(context.Context) (map[string]string, error) {
				return map[string]string{"CLOUD_UPLOAD_PATHS": string(raw), "CLOUD_UPLOAD_ENABLE": "true", "CLOUD_UPLOAD_CONFLICT": "overwrite", "CLOUD_UPLOAD_MODE": "move"}, nil
			}, provider: func(context.Context, string) (uploadRemote, error) { return nil, errors.New("test provider offline") }}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); service.Run(ctx) }()
			defer func() { cancel(); <-done }()
			deadline := time.Now().Add(5 * time.Second)
			for !service.Status().Actions["scan"] {
				if time.Now().After(deadline) {
					t.Fatal("not ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			if service.Files(1, 20, "").Total != 0 {
				t.Fatal("automatic discovery revived a cleared task")
			}
			if err = service.Control(ctx, "scan"); err != nil {
				t.Fatal(err)
			}
			if previousState == "pending" {
				time.Sleep(100 * time.Millisecond)
				got, _ := store.Get(ctx, key)
				if !got.Hidden || got.StageID != "old-id" {
					t.Fatal("unfinished recovery receipt reset")
				}
				return
			}
			deadline = time.Now().Add(5 * time.Second)
			for service.Files(1, 20, "").Total != 1 {
				if time.Now().After(deadline) {
					t.Fatal("manual scan did not restore task")
				}
				time.Sleep(10 * time.Millisecond)
			}
			got, _ := store.Get(ctx, key)
			if got.Mode != "move" || got.Hidden || got.CompletedAt != "" || got.StageID != "" || got.SHA1 != "" || got.Parent != "" || got.QueuedAt == stamp || got.Intent != "run" {
				t.Fatalf("stale execution fields: %+v", got)
			}
			if service.DirectoryPage("").Total != 1 {
				t.Fatal("directory task absent")
			}
			queuedAt := got.QueuedAt
			if err = service.Control(ctx, "scan"); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
			got, _ = store.Get(ctx, key)
			if got.QueuedAt != queuedAt {
				t.Fatal("repeated scan reset visible task")
			}
		})
	}
}

// TestUploadProgressWaitsForVerification keeps bytes and completed object counts distinct.
func TestUploadProgressWaitsForVerification(t *testing.T) {
	m := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: t.TempDir()}
	key := uploadDirectoryKey(m)
	s := &UploadService{mappings: []UploadMapping{m}, files: map[string]UploadFileProgress{
		"a": {Key: "a", DirectoryKey: key, Path: "a", Size: 10, UploadedBytes: 10, State: "uploading", Speed: 5, updated: time.Now()},
		"b": {Key: "b", DirectoryKey: key, Path: "b", Size: 10, State: "skipped"},
	}}
	row := s.Directories("")[0]
	if row.Progress >= 100 || row.Uploaded != 0 || row.Skipped != 1 || row.Total != 2 || row.Size != 20 {
		t.Fatalf("premature completion: %+v", row)
	}
	s.finishFile("a", "completed", nil)
	row = s.Directories("")[0]
	if row.Progress != 100 || row.Uploaded != 1 || row.Speed != 0 {
		t.Fatalf("final counts: %+v", row)
	}
	page := s.Files(2, 1, "")
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].Path != "b" {
		t.Fatalf("paging: %+v", page)
	}
	if len(s.Files(3, 1, "").Items) != 0 {
		t.Fatal("out of range page must be empty")
	}
}

// TestUploadVerificationReportsHashedBytes keeps checksum progress moving before any network transfer starts.
func TestUploadVerificationReportsHashedBytes(t *testing.T) {
	root := t.TempDir()
	content := bytes.Repeat([]byte("v"), 2*1024*1024+100)
	if err := os.WriteFile(filepath.Join(root, "film.mkv"), content, 0600); err != nil {
		t.Fatal(err)
	}
	m := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: root}
	files, err := uploadSnapshot(context.Background(), m)
	if err != nil || len(files) != 1 {
		t.Fatal(err, len(files))
	}
	var reports []int64
	var lastPhase string
	ctx := domain.WithUploadProgress(context.Background(), func(done, total int64, phase string) {
		lastPhase = phase
		if phase == "uploading" {
			return
		}
		if phase != "verifying" || total != int64(len(content)) {
			t.Errorf("progress phase=%s done=%d total=%d", phase, done, total)
		}
		reports = append(reports, done)
	})
	remote := &uploadMemoryRemote{files: map[string][]byte{}}
	s := &UploadService{store: uploadMemoryStore{}, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	if _, err = s.process(ctx, m, files[0], "overwrite"); err != nil {
		t.Fatal(err)
	}
	if lastPhase != "uploading" {
		t.Fatal("verification phase remained during remote requests")
	}
	if len(reports) < 3 || reports[0] != 0 || reports[len(reports)-1] != int64(len(content)) {
		t.Fatalf("verification reports: %v", reports)
	}
	for i := 1; i < len(reports); i++ {
		if reports[i] <= reports[i-1] {
			t.Fatalf("verification progress rewound: %v", reports)
		}
	}
}

// TestUploadDirectoriesHideSavedMappingsWithoutTasks keeps a saved mapping out of the directory table until files are queued or finished.
func TestUploadDirectoriesHideSavedMappingsWithoutTasks(t *testing.T) {
	idle := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: t.TempDir()}
	queued := UploadMapping{Kind: "cd2", ID: "/videos", Path: "/videos", LocalPath: t.TempDir()}
	done := UploadMapping{Kind: "115", ID: "9", Path: "/done", LocalPath: t.TempDir()}
	s := &UploadService{mappings: []UploadMapping{idle, queued, done}, files: map[string]UploadFileProgress{
		"queued": {DirectoryKey: uploadDirectoryKey(queued), Path: "new.mkv", Size: 8, State: "waiting"},
		"done":   {DirectoryKey: uploadDirectoryKey(done), Path: "old.mkv", Size: 8, State: "completed", Progress: 100},
	}}
	rows := s.Directories("")
	if len(rows) != 2 || rows[0].LocalPath != queued.LocalPath || rows[0].Total != 1 || rows[1].LocalPath != done.LocalPath || rows[1].Uploaded != 1 {
		t.Fatalf("%+v", rows)
	}
}

func TestUploadDirectoryTimesUseQueueAndFinalCompletion(t *testing.T) {
	mapping := UploadMapping{Kind: "115", ID: "1", Path: "/videos", LocalPath: "/media"}
	key := uploadDirectoryKey(mapping)
	s := &UploadService{mappings: []UploadMapping{mapping}, files: map[string]UploadFileProgress{
		"a": {DirectoryKey: key, State: "completed", QueuedAt: "2026-11-07T01:00:00Z", CompletedAt: "2026-11-07T03:00:00Z", Size: 1},
		"b": {DirectoryKey: key, State: "skipped", QueuedAt: "2026-11-07T02:00:00Z", CompletedAt: "2026-11-07T04:00:00Z", Size: 1},
	}}
	row := s.Directories("")[0]
	if row.QueuedAt != "2026-11-07T01:00:00Z" || row.CompletedAt != "2026-11-07T04:00:00Z" {
		t.Fatalf("%+v", row)
	}
	s.files["c"] = UploadFileProgress{DirectoryKey: key, State: "waiting", QueuedAt: "2026-11-07T00:30:00Z", Size: 1}
	row = s.Directories("")[0]
	if row.QueuedAt != "2026-11-07T00:30:00Z" || row.CompletedAt != "" {
		t.Fatalf("unfinished directory: %+v", row)
	}
}

func TestUploadCompletionTimeFallsBackAndIsRecordedOnce(t *testing.T) {
	store := uploadMemoryStore{"old": {Key: "old", State: "completed", QueuedAt: "2026-11-07T01:00:00Z", UpdatedAt: "2026-11-07T05:00:00Z", Source: "/old"}}
	s := &UploadService{store: store, files: map[string]UploadFileProgress{}}
	if err := s.refreshUploadView(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := s.Files(1, 10, "").Items[0]
	if old.QueuedAt != "2026-11-07T01:00:00Z" || old.CompletedAt != "2026-11-07T05:00:00Z" {
		t.Fatalf("fallback: %+v", old)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mkv"), []byte("new video"), 0600); err != nil {
		t.Fatal(err)
	}
	mapping := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: root}
	files, err := uploadSnapshot(context.Background(), mapping)
	if err != nil || len(files) != 1 {
		t.Fatal(err)
	}
	remote := &uploadMemoryRemote{files: map[string][]byte{"film.mkv": []byte("old video")}}
	fresh := uploadMemoryStore{}
	service := &UploadService{store: fresh, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	if _, err = service.process(context.Background(), mapping, files[0], "skip"); err != nil {
		t.Fatal(err)
	}
	key := uploadKey(mapping, files[0], "skip")
	if fresh[key].State != "skipped" || fresh[key].CompletedAt == "" {
		t.Fatalf("%+v", fresh[key])
	}
	recorded := fresh[key].CompletedAt
	if _, err = service.process(context.Background(), mapping, files[0], "skip"); err != nil {
		t.Fatal(err)
	}
	if fresh[key].CompletedAt != recorded {
		t.Fatalf("completion time changed: %s", fresh[key].CompletedAt)
	}
}

func TestUploadFileListShowsLocalAndRemotePaths(t *testing.T) {
	store := uploadMemoryStore{
		"nested":   {Key: "nested", Kind: "115", Root: "1", LocalRoot: `E:\media`, RemotePath: "/电影", Relative: "A/a.mkv", Source: `E:\media\A\a.mkv`, State: "pending"},
		"renamed":  {Key: "renamed", Kind: "115", Root: "1", LocalRoot: `E:\media`, RemotePath: "/电影", Relative: "b.mkv", Target: "b (1).mkv", Source: `E:\media\b.mkv`, State: "pending"},
		"fallback": {Key: "fallback", Kind: "cd2", Root: "/cd", LocalRoot: "/data", Relative: "c.mkv", Source: "/data/c.mkv", State: "pending"},
	}
	s := &UploadService{store: store, mode: "running", files: map[string]UploadFileProgress{}, mappings: []UploadMapping{{Kind: "cd2", ID: "/cd", Path: "/cloud", LocalPath: "/data"}}}
	s.status.Enabled = true
	if err := s.refreshUploadView(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := map[string]UploadFileProgress{}
	for _, item := range s.Files(1, 10, "").Items {
		got[item.Key] = item
	}
	if got["nested"].Path != `E:\media\A\a.mkv` || got["nested"].RemotePath != "/电影/A/a.mkv" {
		t.Fatalf("nested: %+v", got["nested"])
	}
	if got["renamed"].RemotePath != "/电影/b (1).mkv" {
		t.Fatalf("renamed: %+v", got["renamed"])
	}
	if got["fallback"].Path != "/data/c.mkv" || got["fallback"].RemotePath != "/cloud/c.mkv" {
		t.Fatalf("fallback: %+v", got["fallback"])
	}
}

func TestUploadQueueStartsInAscendingEnqueueOrder(t *testing.T) {
	older := domain.UploadRecord{Key: "older", State: "pending", QueuedAt: "2026-11-07T01:00:00Z", UpdatedAt: "2026-11-07T03:00:00Z"}
	newer := domain.UploadRecord{Key: "newer", State: "pending", QueuedAt: "2026-11-07T02:00:00Z", UpdatedAt: "2026-11-07T02:00:00Z"}
	commit := domain.UploadRecord{Key: "commit", State: "uploaded", QueuedAt: "2026-11-07T04:00:00Z"}
	if !uploadQueueBefore(older, newer) || uploadQueueBefore(newer, older) || uploadQueueBefore(commit, older) {
		t.Fatal("queue order changed")
	}
	historical := domain.UploadRecord{Key: "historical", State: "pending", UpdatedAt: "2026-11-07T00:30:00Z"}
	if !uploadQueueBefore(historical, older) {
		t.Fatal("historical record did not keep its original update time")
	}
}

func TestUploadQueueAndListShareTieBreakAndChronologicalTime(t *testing.T) {
	for _, stamps := range [][2]string{
		{"2026-11-07T01:00:00Z", "2026-11-07T01:00:00Z"},
		{"2026-11-07T01:00:00Z", "2026-11-07T01:00:00.1Z"},
		{"2026-11-07T09:00:00+08:00", "2026-11-07T01:00:01Z"},
	} {
		first := domain.UploadRecord{Key: "z", Source: "/a", QueuedAt: stamps[0]}
		second := domain.UploadRecord{Key: "a", Source: "/z", QueuedAt: stamps[1]}
		service := &UploadService{files: map[string]UploadFileProgress{
			first.Key:  {Key: first.Key, Path: first.Source, QueuedAt: first.QueuedAt},
			second.Key: {Key: second.Key, Path: second.Source, QueuedAt: second.QueuedAt},
		}}
		if !uploadQueueBefore(first, second) || uploadQueueBefore(second, first) || service.Files(1, 20, "").Items[0].Key != first.Key {
			t.Fatalf("queue/list order diverges: %v", stamps)
		}
	}
}

func TestUploadStartingStoppedLegacyTaskKeepsQueuePosition(t *testing.T) {
	original := "2026-11-06T01:02:03Z"
	record, ok := transformUploadControl(domain.UploadRecord{Key: "legacy", State: "stopped", UpdatedAt: original}, "start", "running", true)
	if !ok || record.QueuedAt != original || record.UpdatedAt == original {
		t.Fatalf("start moved historical task: %+v", record)
	}
}

func TestUploadLegacyQueueTimeSurvivesProcessingAndRestart(t *testing.T) {
	service, mapping, file, remote, store := recoveryFixture(t)
	key := uploadKey(mapping, file, "overwrite")
	original := "2026-11-06T01:02:03Z"
	store[key] = domain.UploadRecord{Key: key, Kind: "115", State: "pending", Source: file.Absolute, Size: file.Size, Modified: file.Modified, UpdatedAt: original}
	remote.failUpload = true
	_, _ = service.process(context.Background(), mapping, file, "overwrite")
	if store[key].QueuedAt != original {
		t.Fatalf("first processing moved task: %+v", store[key])
	}
	remote.failUpload = false
	restarted := &UploadService{store: store, provider: service.provider}
	if _, err := restarted.process(context.Background(), mapping, file, "overwrite"); err != nil {
		t.Fatal(err)
	}
	if store[key].QueuedAt != original {
		t.Fatal("restart moved task")
	}
}

func TestUploadQueueRetriesLockedFileBeforeQueueDrain(t *testing.T) {
	first := domain.UploadRecord{Key: "first", State: "pending", QueuedAt: "2026-11-07T01:00:00Z"}
	second := domain.UploadRecord{Key: "second", State: "pending", QueuedAt: "2026-11-07T02:00:00Z"}
	if !uploadQueueBefore(first, second) {
		t.Fatal("older task must remain first in the queue")
	}
	if !uploadRetryDue(domain.UploadRecord{Error: "该文件正在被占用", ErrorKind: "locked", NextRetry: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)}, time.Now()) {
		t.Fatal("locked task must be probed after every subsequent completion")
	}
}

func TestUploadSourceGuardDetectsWriter(t *testing.T) {
	name := filepath.Join(t.TempDir(), "copying.bin")
	writer, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if !uploadFileLocked(name) {
		t.Fatal("open writable file was not detected")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if uploadFileLocked(name) {
		t.Fatal("closed writer still blocks upload")
	}
}

func TestUploadQueueRechecksLockedFileAfterNextCompletion(t *testing.T) {
	root := t.TempDir()
	mapping := UploadMapping{Kind: "115", ID: "0", LocalPath: root, Path: "/"}
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := os.OpenFile(filepath.Join(root, "a.bin"), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	files, err := uploadSnapshot(context.Background(), mapping)
	if err != nil {
		t.Fatal(err)
	}
	store := uploadMemoryStore{}
	stamp := time.Now().Add(-20 * time.Second).UTC().Format(time.RFC3339Nano)
	for _, file := range files {
		key := uploadKey(mapping, file, "overwrite")
		store[key] = domain.UploadRecord{Key: key, Kind: "115", Root: "0", LocalRoot: root, RemotePath: "/", Source: file.Absolute, Relative: file.Relative, Size: file.Size, Modified: file.Modified, State: "pending", Policy: "overwrite", Intent: "run", QueuedAt: stamp, UpdatedAt: stamp}
	}
	remote := &uploadOrderedRemote{uploadMemoryRemote: uploadMemoryRemote{files: map[string][]byte{}}, writer: writer}
	raw, _ := json.Marshal([]UploadMapping{mapping})
	service := &UploadService{store: store, settings: func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_ENABLE": "false", "CLOUD_UPLOAD_PATHS": string(raw), "CLOUD_UPLOAD_CONFLICT": "overwrite", "CLOUD_UPLOAD_CONCURRENCY": "1"}, nil
	}, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); service.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(8 * time.Second)
	for service.Files(1, 20, "").Completed != 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if service.Files(1, 20, "").Completed != 3 {
		t.Fatalf("queue did not complete: %+v", service.Files(1, 20, ""))
	}
	if strings.Join(remote.order, ",") != "b.bin,a.bin,c.bin" {
		t.Fatalf("locked task waited for queue drain: %v", remote.order)
	}
}

type uploadOrderedRemote struct {
	uploadMemoryRemote
	writer *os.File
	order  []string
}

func (remote *uploadOrderedRemote) Upload(ctx context.Context, parent, name string, file *os.File, size int64) error {
	remote.order = append(remote.order, name)
	if strings.HasPrefix(name, "b.") {
		if err := remote.writer.Close(); err != nil {
			return err
		}
	}
	return remote.uploadMemoryRemote.Upload(ctx, parent, name, file, size)
}

func TestUploadRemoteMissingClassificationDoesNotTreatRateLimitAsDeleted(t *testing.T) {
	for _, message := range []string{"请求过于频繁", "文件不存在或已删除", "目录不存在"} {
		got := isUploadRemoteMissingError(&pan115.APIError{Code: 20018, Message: message})
		want := message != "请求过于频繁"
		if got != want {
			t.Fatalf("message=%q got=%v want=%v", message, got, want)
		}
	}
}

func TestUploadStatusFilterKeepsDirectoryTotals(t *testing.T) {
	directory := UploadMapping{Kind: "115", ID: "1", Path: "/videos", LocalPath: "/videos"}
	other := UploadMapping{Kind: "115", ID: "2", Path: "/other", LocalPath: "/other"}
	key := uploadDirectoryKey(directory)
	s := &UploadService{mappings: []UploadMapping{directory, other}, files: map[string]UploadFileProgress{
		"run":  {Key: "run", DirectoryKey: key, Path: "/videos/a.mkv", State: "uploading", Size: 10, QueuedAt: "2"},
		"wait": {Key: "wait", DirectoryKey: key, Path: "/videos/b.mkv", State: "waiting", Size: 10, QueuedAt: "1"},
		"done": {Key: "done", DirectoryKey: uploadDirectoryKey(other), Path: "/other/c.mkv", State: "completed", Size: 10, QueuedAt: "3"},
	}}
	if s.Files(1, 10, "waiting").Total != 1 || s.Files(1, 10, "waiting").Items[0].Key != "wait" {
		t.Fatalf("files: %+v", s.Files(1, 10, "waiting"))
	}
	if len(s.Files(1, 10, "running").Items) != 1 || s.Files(1, 10, "running").Items[0].Actions["start"] {
		t.Fatalf("running exposed start: %+v", s.Files(1, 10, "running").Items)
	}
	rows := s.Directories("waiting")
	if len(rows) != 1 || rows[0].Key != key || rows[0].Total != 2 || !rows[0].Actions["pause"] || rows[0].Actions["start"] {
		t.Fatalf("directory filter: %+v", rows)
	}
	directoryPage := s.DirectoryPage("waiting")
	if directoryPage.Total != 2 || directoryPage.Completed != 1 || len(directoryPage.Items) != 1 {
		t.Fatalf("directory counts must ignore filters and count directories: %+v", directoryPage)
	}
	filePage := s.Files(2, 1, "completed")
	if filePage.Total != 1 || filePage.TaskTotal != 3 || filePage.Completed != 1 || len(filePage.Items) != 0 {
		t.Fatalf("file counts must ignore filters and pagination: %+v", filePage)
	}
}

func TestUploadOutcomeFiltersCombineSkippedAndChanged(t *testing.T) {
	mapping := UploadMapping{Kind: "115", ID: "0", LocalPath: "/videos"}
	key := uploadDirectoryKey(mapping)
	service := &UploadService{mappings: []UploadMapping{mapping}, files: map[string]UploadFileProgress{
		"done":    {Key: "done", DirectoryKey: key, State: "completed"},
		"skip":    {Key: "skip", DirectoryKey: key, State: "skipped"},
		"changed": {Key: "changed", DirectoryKey: key, State: "discarded"},
	}}
	if page := service.Files(1, 20, "completed"); page.Total != 2 || page.Completed != 2 {
		t.Fatalf("success excludes skipped: %+v", page)
	}
	if page := service.Files(1, 20, "failed"); page.Total != 1 || page.Items[0].Key != "changed" {
		t.Fatalf("failure excludes changed: %+v", page)
	}
	if rows := service.Directories("failed"); len(rows) != 1 || rows[0].Failed != 1 {
		t.Fatalf("directory failure count: %+v", rows)
	}
	if !uploadClearMatches(domain.UploadRecord{State: "discarded"}, "clear_failed") || !uploadListClearActions(service.files)["clear_failed_files"] {
		t.Fatal("changed failure cannot be cleared")
	}
	for _, status := range []string{"skipped", "changed"} {
		if UploadListStatusAllowed(status) {
			t.Fatalf("obsolete filter accepted: %s", status)
		}
	}
}

func TestUploadDirectoryProgressExcludesVerification(t *testing.T) {
	mapping := UploadMapping{Kind: "115", ID: "0", LocalPath: "/videos"}
	key := uploadDirectoryKey(mapping)
	service := &UploadService{mappings: []UploadMapping{mapping}, files: map[string]UploadFileProgress{
		"verifying": {DirectoryKey: key, State: "verifying", Size: 100, UploadedBytes: 80, Progress: 80},
		"done":      {DirectoryKey: key, State: "completed", Size: 100, UploadedBytes: 100, Progress: 100},
	}}
	if rows := service.Directories(""); len(rows) != 1 || rows[0].Progress != 50 {
		t.Fatalf("verification counted as uploaded: %+v", rows)
	}
}

// TestUploadRetriesPartialStage verifies interrupted transfers can recover without changing the old target.
func TestUploadRetriesPartialStage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mkv"), []byte("new video"), 0600); err != nil {
		t.Fatal(err)
	}
	m := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: root}
	files, err := uploadSnapshot(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	remote := &uploadMemoryRemote{files: map[string][]byte{"film.mkv": []byte("old video")}, failUpload: true}
	store := uploadMemoryStore{}
	s := &UploadService{store: store, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	if _, err = s.process(context.Background(), m, files[0], "overwrite"); err == nil {
		t.Fatal("expected failure")
	}
	record := store[uploadKey(m, files[0], "overwrite")]
	remote.files["unrelated-partial"] = []byte("partial")
	remote.failUpload = false
	if _, err = s.process(context.Background(), m, files[0], "overwrite"); err != nil {
		t.Fatal(err)
	}
	if len(remote.files) != 2 || string(remote.files["film.mkv"]) != "new video" || string(remote.files["unrelated-partial"]) != "partial" || record.Stage != "film.mkv" {
		t.Fatal(remote.files)
	}
}

func TestUploadInitializationPreservesRetryBudget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	mapping := UploadMapping{Kind: "115", ID: "0", LocalPath: root}
	files, err := uploadSnapshot(context.Background(), mapping)
	if err != nil {
		t.Fatal(err)
	}
	key := uploadKey(mapping, files[0], "overwrite")
	store := uploadMemoryStore{key: {Key: key, State: "pending", Retries: 2, Intent: "run", QueuedAt: "original", Source: files[0].Absolute, Size: files[0].Size, Modified: files[0].Modified}}
	remote := &uploadMemoryRemote{files: map[string][]byte{}, failUpload: true}
	service := &UploadService{store: store, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	if _, err = service.process(context.Background(), mapping, files[0], "overwrite"); err == nil {
		t.Fatal("expected transfer failure")
	}
	if record := store[key]; record.Retries != 3 || record.Intent != "run" || record.QueuedAt != "original" || uploadRetryDue(record, time.Now()) {
		t.Fatalf("initialization lost queue metadata: %+v", record)
	}
}
func (s uploadMemoryStore) Save(_ context.Context, r domain.UploadRecord) error {
	uploadMemoryStoreMu.Lock()
	defer uploadMemoryStoreMu.Unlock()
	s[r.Key] = r
	return nil
}

type uploadMemoryRemote struct {
	files              map[string][]byte
	extra              []uploadEntry
	failUpload         bool
	loseRenameResponse bool
	preserveExtension  bool
	failDelete         bool
	uploads            int
	deleted            int
	afterUpload        func()
	loseDeleteResponse bool
}

type uploadRecreateRemote struct {
	entries     map[string][]uploadEntry
	createdRoot string
	uploaded    string
}

func (r *uploadRecreateRemote) List(_ context.Context, parent string) ([]uploadEntry, error) {
	if parent == "deleted" {
		return nil, &pan115.APIError{Code: 20018, Message: "文件不存在或已删除"}
	}
	return r.entries[parent], nil
}
func (r *uploadRecreateRemote) Mkdir(_ context.Context, parent, name string) (string, error) {
	id := parent + "/" + name
	r.entries[parent] = append(r.entries[parent], uploadEntry{ID: id, Name: name, Directory: true})
	r.entries[id] = nil
	if name == "restored" {
		r.createdRoot = id
	}
	return id, nil
}
func (r *uploadRecreateRemote) Upload(_ context.Context, parent, name string, file *os.File, size int64) error {
	r.uploaded = parent + ":" + name
	body, err := io.ReadAll(io.NewSectionReader(file, 0, size))
	if err != nil {
		return err
	}
	r.entries[parent] = append(r.entries[parent], uploadEntry{ID: name, Name: name, Size: size, SHA1: fmt.Sprintf("%x", sha1.Sum(body))})
	return nil
}
func (r *uploadRecreateRemote) Rename(_ context.Context, parent string, entry uploadEntry, name string) error {
	for i := range r.entries[parent] {
		if r.entries[parent][i].Name == entry.Name {
			r.entries[parent][i].Name = name
			return nil
		}
	}
	return errors.New("rename entry not found")
}
func (r *uploadRecreateRemote) Delete(context.Context, string, uploadEntry) error { return nil }

func (r *uploadMemoryRemote) List(context.Context, string) ([]uploadEntry, error) {
	var out []uploadEntry
	for name, body := range r.files {
		out = append(out, uploadEntry{ID: fmt.Sprintf("%x", sha1.Sum(body)), Name: name, Size: int64(len(body)), SHA1: fmt.Sprintf("%x", sha1.Sum(body))})
	}
	out = append(out, r.extra...)
	return out, nil
}
func (r *uploadMemoryRemote) Mkdir(context.Context, string, string) (string, error) {
	return "", errors.New("unexpected mkdir")
}
func (r *uploadMemoryRemote) Upload(_ context.Context, _, name string, f *os.File, size int64) error {
	r.uploads++
	if r.failUpload {
		return errors.New("transfer failed")
	}
	body, err := io.ReadAll(io.NewSectionReader(f, 0, size))
	r.files[name] = body
	if r.afterUpload != nil {
		r.afterUpload()
	}
	return err
}
func (r *uploadMemoryRemote) Rename(_ context.Context, _ string, e uploadEntry, name string) error {
	if r.preserveExtension {
		name = strings.TrimSuffix(name, path.Ext(name)) + path.Ext(e.Name)
	}
	for index := range r.extra {
		if r.extra[index].ID == e.ID {
			r.extra[index].Name = name
			return nil
		}
	}
	r.files[name] = r.files[e.Name]
	delete(r.files, e.Name)
	if r.loseRenameResponse {
		r.loseRenameResponse = false
		return errors.New("response lost")
	}
	return nil
}
func (r *uploadMemoryRemote) Delete(_ context.Context, _ string, e uploadEntry) error {
	if r.failDelete {
		return errors.New("delete unavailable")
	}
	for index := range r.extra {
		if r.extra[index].ID == e.ID {
			r.extra = append(r.extra[:index], r.extra[index+1:]...)
			r.deleted++
			return nil
		}
	}
	delete(r.files, e.Name)
	if r.loseDeleteResponse {
		r.loseDeleteResponse = false
		return errors.New("delete response lost")
	}
	return nil
}

// TestUploadConflictPolicies exercises final remote contents and restart idempotence for all policies.
func TestUploadConflictPolicies(t *testing.T) {
	for _, policy := range []string{"skip", "overwrite", "keep_both"} {
		t.Run(policy, func(t *testing.T) {
			root := t.TempDir()
			name := filepath.Join(root, "film.mkv")
			if err := os.WriteFile(name, []byte("new video"), 0600); err != nil {
				t.Fatal(err)
			}
			mapping := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: root}
			files, err := uploadSnapshot(context.Background(), mapping)
			if err != nil {
				t.Fatal(err)
			}
			remote := &uploadMemoryRemote{files: map[string][]byte{"film.mkv": []byte("old video")}}
			store := uploadMemoryStore{}
			service := &UploadService{store: store, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
			for n := 0; n < 2; n++ {
				if _, err = service.process(context.Background(), mapping, files[0], policy); err != nil {
					t.Fatal(err)
				}
			}
			switch policy {
			case "skip":
				if len(remote.files) != 1 || string(remote.files["film.mkv"]) != "old video" {
					t.Fatal(remote.files)
				}
			case "overwrite":
				if len(remote.files) != 1 || string(remote.files["film.mkv"]) != "new video" {
					t.Fatal(remote.files)
				}
			case "keep_both":
				if len(remote.files) != 2 || string(remote.files["film (1).mkv"]) != "new video" || string(remote.files["film.mkv"]) != "old video" {
					t.Fatal(remote.files)
				}
			}
		})
	}
}

// TestUploadFailurePreservesOriginal and uncertain rename recovery protect against destructive retries.
func TestUploadFailurePreservesOriginal(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "film.mkv"), []byte("new"), 0600)
	m := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: root}
	files, _ := uploadSnapshot(context.Background(), m)
	remote := &uploadMemoryRemote{files: map[string][]byte{"film.mkv": []byte("old")}, failUpload: true}
	s := &UploadService{store: uploadMemoryStore{}, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	if _, err := s.process(context.Background(), m, files[0], "overwrite"); err == nil {
		t.Fatal("expected failure")
	}
	if string(remote.files["film.mkv"]) != "old" {
		t.Fatal("original lost")
	}
	remote.failUpload = false
	remote.loseRenameResponse = true
	if state, err := s.process(context.Background(), m, files[0], "overwrite"); err != nil || state != "completed" {
		t.Fatalf("direct upload must not rename: %s %v", state, err)
	}
	if err := os.Remove(files[0].Absolute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.process(context.Background(), m, files[0], "overwrite"); err != nil {
		t.Fatal(err)
	}
	if len(remote.files) != 1 || string(remote.files["film.mkv"]) != "new" {
		t.Fatal(remote.files)
	}
}

// TestUploadSettingsRejectInvalidMappings prevents incomplete or traversing mappings from starting a watcher.
func TestUploadSettingsRejectInvalidMappings(t *testing.T) {
	svc, err := NewSettingsService(settingsRepositoryStub{}, "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"skip", "overwrite", "keep_both"} {
		if _, err = svc.Update(context.Background(), map[string]string{"CLOUD_UPLOAD_CONFLICT": policy}); err != nil {
			t.Fatalf("policy %s: %v", policy, err)
		}
	}
	for _, raw := range []string{`[{"kind":"other","id":"0","path":"/","local_path":"/media"}]`, `[{"kind":"115","id":"0","path":"/","local_path":"../media"}]`} {
		if _, err = svc.Update(context.Background(), map[string]string{"CLOUD_UPLOAD_PATHS": raw}); err == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
}

// TestUploadSnapshotIncludesExistingFiles excludes symbolic links and detects already-present files on initial enable.
func TestUploadSnapshotIncludesExistingFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "film.mkv"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]UploadMapping{{Kind: "115", ID: "0", Path: "/", LocalPath: root}})
	mappings, err := parseUploadMappings(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	files, err := uploadSnapshot(context.Background(), mappings[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Relative != "sub/film.mkv" {
		t.Fatalf("files=%+v", files)
	}
}
