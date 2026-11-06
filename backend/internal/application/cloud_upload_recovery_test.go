package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// recoveryFixture exercises the real replacement coordinator against a faultable remote.
func recoveryFixture(t *testing.T) (*UploadService, UploadMapping, uploadLocalFile, *uploadMemoryRemote, uploadMemoryStore) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mkv"), []byte("new video"), 0600); err != nil {
		t.Fatal(err)
	}
	m := UploadMapping{Kind: "115", ID: "0", Path: "/", LocalPath: root}
	files, err := uploadSnapshot(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	r := &uploadMemoryRemote{files: map[string][]byte{"film.mkv": []byte("old video")}}
	store := uploadMemoryStore{}
	s := &UploadService{store: store, provider: func(context.Context, string) (uploadRemote, error) { return r, nil }}
	return s, m, files[0], r, store
}

func TestUploadCleanupFailureIsDurableAndNeverReuploads(t *testing.T) {
	s, m, f, r, store := recoveryFixture(t)
	r.failDelete = true
	state, err := s.process(context.Background(), m, f, "overwrite")
	if err == nil || state != "cleanup" {
		t.Fatalf("state=%s err=%v", state, err)
	}
	if string(r.files["film.mkv"]) != "new video" {
		t.Fatal("new target not installed")
	}
	record := store[uploadKey(m, f, "overwrite")]
	if record.State != "cleanup" {
		t.Fatalf("not durable: %+v", record)
	}
	if err = os.Remove(f.Absolute); err != nil {
		t.Fatal(err)
	}
	r.failDelete = false
	restarted := &UploadService{store: store, provider: s.provider}
	if state, err = restarted.process(context.Background(), m, f, "overwrite"); err != nil || state != "completed" {
		t.Fatalf("%s %v", state, err)
	}
	if r.uploads != 1 || len(r.files) != 1 {
		t.Fatalf("uploads=%d files=%v", r.uploads, r.files)
	}
}

func TestUploadKeepBothReselectsOccupiedName(t *testing.T) {
	s, m, f, r, _ := recoveryFixture(t)
	r.failUpload = true
	if _, err := s.process(context.Background(), m, f, "keep_both"); err == nil {
		t.Fatal("expected interrupted upload")
	}
	r.files["film (1).mkv"] = []byte("another file")
	r.failUpload = false
	if _, err := s.process(context.Background(), m, f, "keep_both"); err != nil {
		t.Fatal(err)
	}
	if string(r.files["film (2).mkv"]) != "new video" || string(r.files["film (1).mkv"]) != "another file" {
		t.Fatal(r.files)
	}
}

func TestUploadUsesShortSourceBasedStageName(t *testing.T) {
	s, m, f, _, store := recoveryFixture(t)
	if _, err := s.process(context.Background(), m, f, "overwrite"); err != nil {
		t.Fatal(err)
	}
	record := store[uploadKey(m, f, "overwrite")]
	if record.Stage != "film.mkv.bytemuse-go-tmp" {
		t.Fatalf("stage=%q", record.Stage)
	}
}

func TestUploadRecreatesDeleted115MappingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mkv"), []byte("new video"), 0600); err != nil {
		t.Fatal(err)
	}
	m := UploadMapping{Kind: "115", ID: "deleted", Path: "/restored", LocalPath: root}
	f := uploadLocalFile{Absolute: filepath.Join(root, "film.mkv"), Relative: "film.mkv"}
	info, err := os.Stat(f.Absolute)
	if err != nil {
		t.Fatal(err)
	}
	f.Size, f.Modified = info.Size(), info.ModTime().UnixNano()
	remote := &uploadRecreateRemote{entries: map[string][]uploadEntry{"0": nil}}
	store := uploadMemoryStore{}
	service := &UploadService{store: store, provider: func(context.Context, string) (uploadRemote, error) { return remote, nil }}
	if _, err = service.process(context.Background(), m, f, "overwrite"); err != nil {
		t.Fatal(err)
	}
	if remote.createdRoot == "" || len(remote.entries[remote.createdRoot]) != 1 || remote.entries[remote.createdRoot][0].Name != "film.mkv" {
		t.Fatalf("directory was not recreated: %+v", remote)
	}
}

func TestUploadMatches115EntryByContentWhenRemoteIDChanges(t *testing.T) {
	record := domain.UploadRecord{Kind: "115", Size: 3, StageID: "old-id", SHA1: "abc123"}
	entry := uploadEntry{ID: "new-id", Size: 3, SHA1: "ABC123"}
	if !matchesUpload(&record, entry, false) {
		t.Fatal("same content should identify the uploaded object when 115 changes its listing ID")
	}
}

func TestUploadCleanupPreservesUnexpectedBackup(t *testing.T) {
	s, m, f, r, store := recoveryFixture(t)
	r.failDelete = true
	_, _ = s.process(context.Background(), m, f, "overwrite")
	record := store[uploadKey(m, f, "overwrite")]
	r.files[record.Backup] = []byte("unrelated")
	r.failDelete = false
	if _, err := s.process(context.Background(), m, f, "overwrite"); err == nil {
		t.Fatal("unexpected backup deleted")
	}
	if string(r.files[record.Backup]) != "unrelated" {
		t.Fatal("unrelated data lost")
	}
}

func TestUploadLostStageRestoresOriginalName(t *testing.T) {
	s, m, f, r, store := recoveryFixture(t)
	r.loseRenameResponse = true
	_, _ = s.process(context.Background(), m, f, "overwrite")
	record := store[uploadKey(m, f, "overwrite")]
	delete(r.files, record.Stage)
	_, err := s.process(context.Background(), m, f, "overwrite")
	if err == nil || string(r.files["film.mkv"]) != "old video" {
		t.Fatalf("err=%v files=%v", err, r.files)
	}
}

func TestUploadReservesKeepBothAcrossPendingTasks(t *testing.T) {
	s, m, f, r, _ := recoveryFixture(t)
	r.failUpload = true
	_, _ = s.process(context.Background(), m, f, "keep_both")
	second := filepath.Join(m.LocalPath, "film (1).mkv")
	if err := os.WriteFile(second, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(second)
	r.failUpload = false
	if _, err := s.process(context.Background(), m, uploadLocalFile{Absolute: second, Relative: "film (1).mkv", Size: info.Size(), Modified: info.ModTime().UnixNano()}, "keep_both"); err != nil {
		t.Fatal(err)
	}
	if _, exists := r.files["film (1).mkv"]; exists {
		t.Fatal("pending reservation stolen")
	}
	if _, err := s.process(context.Background(), m, f, "keep_both"); err != nil {
		t.Fatal(err)
	}
	if string(r.files["film (1).mkv"]) != "new video" {
		t.Fatal(r.files)
	}
}

func TestUploadSourceChangedCleansOldStageBeforeNewVersion(t *testing.T) {
	s, m, f, r, store := recoveryFixture(t)
	r.afterUpload = func() {
		err := os.WriteFile(f.Absolute, []byte("changed version"), 0600)
		if runtime.GOOS == "windows" {
			if err == nil {
				t.Fatal("upload guard allowed source modification")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := s.process(context.Background(), m, f, "overwrite")
	if runtime.GOOS == "windows" {
		if err != nil || state != "completed" || string(r.files["film.mkv"]) != "new video" {
			t.Fatalf("guarded upload: state=%s err=%v files=%v", state, err, r.files)
		}
		return
	}
	if err != nil || state != "discarded" || len(r.files) != 1 || string(r.files["film.mkv"]) != "old video" {
		t.Fatalf("state=%s err=%v files=%v", state, err, r.files)
	}
	if store[uploadKey(m, f, "overwrite")].State != "discarded" {
		t.Fatal("obsolete version not persisted")
	}
	r.afterUpload = nil
	files, _ := uploadSnapshot(context.Background(), m)
	if _, err = s.process(context.Background(), m, files[0], "overwrite"); err != nil {
		t.Fatal(err)
	}
	if string(r.files["film.mkv"]) != "changed version" {
		t.Fatal(r.files)
	}
}

func TestUploadUncertainDeleteReconcilesWithoutReupload(t *testing.T) {
	s, m, f, r, _ := recoveryFixture(t)
	r.loseDeleteResponse = true
	if state, err := s.process(context.Background(), m, f, "overwrite"); state != "cleanup" || err == nil {
		t.Fatalf("%s %v", state, err)
	}
	if _, err := s.process(context.Background(), m, f, "overwrite"); err != nil {
		t.Fatal(err)
	}
	if r.uploads != 1 || len(r.files) != 1 {
		t.Fatalf("uploads=%d files=%v", r.uploads, r.files)
	}
}

func TestUploadRetryAfterRemoteObjectsDeleted(t *testing.T) {
	service, mapping, file, remote, store := recoveryFixture(t)
	remote.failDelete = true
	if _, err := service.process(context.Background(), mapping, file, "overwrite"); err == nil {
		t.Fatal("expected cleanup failure")
	}
	key := uploadKey(mapping, file, "overwrite")
	record := store[key]
	record.Retries = 3
	store[key] = record
	remote.files = map[string][]byte{}
	remote.failDelete = false
	service.mode = "running"
	service.status.Enabled = true
	if err := service.applyScopedUploadControl(context.Background(), "retry", "file", key); err != nil {
		t.Fatal(err)
	}
	if _, err := service.process(context.Background(), mapping, file, "overwrite"); err != nil {
		t.Fatal(err)
	}
	if store[key].State != "pending" || store[key].Parent != "" {
		t.Fatalf("missing objects were not reset: %+v", store[key])
	}
	if state, err := service.process(context.Background(), mapping, file, "overwrite"); err != nil || state != "completed" {
		t.Fatalf("retry state=%s err=%v", state, err)
	}
	if remote.uploads != 2 || string(remote.files["film.mkv"]) != "new video" {
		t.Fatalf("retry did not upload source: %+v", remote)
	}
}

func TestUploadStopDoesNotRecreateDeletedParent(t *testing.T) {
	service, mapping, file, _, store := recoveryFixture(t)
	key := uploadKey(mapping, file, "overwrite")
	store[key] = domain.UploadRecord{Key: key, State: "discarding", Kind: "115", Parent: "deleted", Intent: "stop"}
	remote := &uploadRecreateRemote{entries: map[string][]uploadEntry{}}
	service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
	if state, err := service.process(context.Background(), mapping, file, "overwrite"); err != nil || state != "stopped" {
		t.Fatalf("stop state=%s err=%v", state, err)
	}
	if remote.uploaded != "" || len(remote.entries) != 0 {
		t.Fatal("stop recreated remote data")
	}
}
