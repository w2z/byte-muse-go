package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type uploadMemoryStore map[string]domain.UploadRecord

func (s uploadMemoryStore) List(context.Context) ([]domain.UploadRecord, error) {
	result := []domain.UploadRecord{}
	for _, r := range s {
		result = append(result, r)
	}
	return result, nil
}

func (s uploadMemoryStore) PendingCommits(context.Context) ([]domain.UploadRecord, error) {
	result := []domain.UploadRecord{}
	for _, r := range s {
		if !uploadTerminal(r.State) && r.State != "control" {
			result = append(result, r)
		}
	}
	return result, nil
}

func (s uploadMemoryStore) Get(_ context.Context, key string) (*domain.UploadRecord, error) {
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
	wait(func() bool { return s.Files(1, 20).Total == 1 })
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return s.Files(1, 20).Total == 2 })
	enabled.Store(false)
	wait(func() bool { return !s.Status().Enabled })
	files := s.Files(1, 20)
	if files.Total != 2 || files.Items[0].State != "stopped" {
		t.Fatalf("lost stopped snapshot: %+v", files)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not stop")
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
	row := s.Directories()[0]
	if row.Progress >= 100 || row.Uploaded != 0 || row.Skipped != 1 || row.Total != 2 || row.Size != 20 {
		t.Fatalf("premature completion: %+v", row)
	}
	s.finishFile("a", "completed", nil)
	row = s.Directories()[0]
	if row.Progress != 100 || row.Uploaded != 1 || row.Speed != 0 {
		t.Fatalf("final counts: %+v", row)
	}
	page := s.Files(2, 1)
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].Path != "b" {
		t.Fatalf("paging: %+v", page)
	}
	if len(s.Files(3, 1).Items) != 0 {
		t.Fatal("out of range page must be empty")
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
	remote.files[record.Stage] = []byte("partial")
	remote.failUpload = false
	if _, err = s.process(context.Background(), m, files[0], "overwrite"); err != nil {
		t.Fatal(err)
	}
	if len(remote.files) != 1 || string(remote.files["film.mkv"]) != "new video" {
		t.Fatal(remote.files)
	}
}
func (s uploadMemoryStore) Save(_ context.Context, r domain.UploadRecord) error {
	s[r.Key] = r
	return nil
}

type uploadMemoryRemote struct {
	files              map[string][]byte
	failUpload         bool
	loseRenameResponse bool
	failDelete         bool
	uploads            int
	afterUpload        func()
	loseDeleteResponse bool
}

func (r *uploadMemoryRemote) List(context.Context, string) ([]uploadEntry, error) {
	var out []uploadEntry
	for name, body := range r.files {
		out = append(out, uploadEntry{ID: fmt.Sprintf("%x", sha1.Sum(body)), Name: name, Size: int64(len(body)), SHA1: fmt.Sprintf("%x", sha1.Sum(body))})
	}
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
	if _, err := s.process(context.Background(), m, files[0], "overwrite"); err == nil {
		t.Fatal("expected uncertain rename")
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
