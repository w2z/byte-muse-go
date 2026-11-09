package application

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

type localUploadRemote struct{ *directUploadRemote }

func (r localUploadRemote) AccountID(context.Context) (string, error) { return "123", nil }

func TestUploadPostprocessFailureRetainsMoveSource(t *testing.T) {
	ctx := context.Background()
	service, mapping, file, _, store := recoveryFixture(t)
	remote := &directUploadRemote{}
	remote.afterDirectUpload = func() {
		entry := &remote.entries[0]
		entry.Info = &pan115.FileInfo{File: pan115.File{ID: entry.ID, Name: entry.Name, SHA1: entry.SHA1, Size: entry.Size}}
	}
	service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
	key := uploadKey(mapping, file, "overwrite")
	store[key] = domain.UploadRecord{Key: key, Kind: "115", Root: mapping.ID, LocalRoot: mapping.LocalPath, Source: file.Absolute, Relative: file.Relative, Size: file.Size, Modified: file.Modified, Policy: "overwrite", Mode: "move", State: "pending"}
	// The real adapter adds account scope; emulate that boundary without contacting 115.
	service.provider = func(context.Context, string) (uploadRemote, error) { return localUploadRemote{remote}, nil }
	fail := true
	service.SetLocalPostprocess(func(_ context.Context, account string, record domain.UploadRecord, _ pan115.FileInfo) error {
		if _, err := os.Stat(record.Source); err != nil {
			t.Fatal("source deleted before handoff", err)
		}
		if fail {
			return errors.New("local disk unavailable")
		}
		return nil
	})
	if _, err := service.process(ctx, mapping, file, "overwrite"); err == nil {
		t.Fatal("failed handoff reported success")
	}
	if _, err := os.Stat(file.Absolute); err != nil {
		t.Fatal("source lost", err)
	}
	if store[key].State != "cleanup" {
		t.Fatalf("state=%s", store[key].State)
	}
	fail = false
	if state, err := service.process(ctx, mapping, file, "overwrite"); err != nil || state != "completed" {
		t.Fatalf("retry=%s %v", state, err)
	}
	if len(remote.names) != 1 {
		t.Fatal("reuploaded on local retry")
	}
	if _, err := os.Stat(file.Absolute); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("move not completed %v", err)
	}
}

func TestLocalCopyRejectsChangedContent(t *testing.T) {
	s, _, repo, _, _ := managedFixture(t)
	local := t.TempDir()
	source := filepath.Join(local, "movie.srt")
	if err := os.WriteFile(source, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(source)
	receipt := &ports.UploadReceipt{FileID: "43", LocalRoot: local, Relative: "movie.srt", Size: 7, Modified: stat.ModTime().UnixNano(), SHA1: fmt.Sprintf("%x", sha1.Sum([]byte("initial")))}
	dir, err := os.OpenRoot(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err = s.copyUploadedMedia(context.Background(), dir, s.root, "movie.srt", strmSourceFile{ID: "43", Name: "movie.srt"}, receipt); err == nil {
		t.Fatal("changed contents installed")
	}
	if _, err = os.Stat(filepath.Join(s.root, "movie.srt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid destination exists")
	}
	if len(repo.items) != 0 {
		t.Fatal("invalid source acquired ownership")
	}
}

func TestUploadPostprocessGeneratesVideoWithoutSourceOrRemoteCalls(t *testing.T) {
	ctx := context.Background()
	s, api, _, mapping, values := managedFixture(t)
	values[pan115LocalUploadEnabledKey] = "true"
	db, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "video.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s.SetUploadReceipts(database.NewUploadReceiptRepository(db.SQLDB(), database.DialectSQLite))
	info := api.infos["42"]
	info.SHA1, info.Size, info.SizeKnown = "verified", 100, true
	record := domain.UploadRecord{Key: "video", StageID: "42", SHA1: "verified", Size: 100, LocalRoot: t.TempDir(), Relative: "movie.mkv"}
	if err = s.ProcessLocalUpload(ctx, "123", record, info); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.root, "movies", "movie.strm"))
	if err != nil || string(data) != "https://media.example.com/files/play/115/pc42\n" {
		t.Fatalf("strm=%q %v", data, err)
	}
	if handled, err := s.ResumeLocalUpload(ctx, record); err != nil || !handled {
		t.Fatalf("resume=%v %v", handled, err)
	}
	if api.infoCalls != 0 || api.playFile != "" {
		t.Fatal("local video called remote")
	}
	finish := s.BeginLocalUpload()
	if _, err = s.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, []pan115.LifeEvent{{Type: 2, FileID: "42"}}, false); err == nil {
		t.Fatal("event raced local upload")
	}
	finish()
	ctx = context.WithValue(ctx, managedAccountKey{}, "123")
	event := pan115.LifeEvent{Type: 2, FileID: "42", ParentID: "10", UpdatedAt: time.Now().Unix()}
	values[strmPlayBaseSettingKey] = "https://changed.example.com"
	if done, err := s.localUploadEventCompleted(ctx, event); err != nil || done {
		t.Fatalf("changed rules suppressed event: %v %v", done, err)
	}
}

func TestUploadPostprocessPendingCopyResumesWithoutRemote(t *testing.T) {
	ctx := context.Background()
	s, api, _, _, values := managedFixture(t)
	values[pan115LocalUploadEnabledKey], values[strmDownloadEnableSettingKey] = "true", "true"
	db, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "pending.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := database.NewUploadReceiptRepository(db.SQLDB(), database.DialectSQLite)
	s.SetUploadReceipts(repo)
	local := t.TempDir()
	if err = os.WriteFile(filepath.Join(local, "movie.srt"), []byte("subtitle"), 0600); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(filepath.Join(local, "movie.srt"))
	record := domain.UploadRecord{Key: "pending", Kind: "115", StageID: "43", SHA1: fmt.Sprintf("%x", sha1.Sum([]byte("subtitle"))), Size: 8, Modified: stat.ModTime().UnixNano(), LocalRoot: local, Relative: "movie.srt", Source: filepath.Join(local, "movie.srt"), State: "cleanup"}
	mapping := UploadMapping{Kind: "115", ID: "10", LocalPath: local, Path: "/movies"}
	file := uploadLocalFile{Absolute: record.Source, Relative: record.Relative, Size: record.Size, Modified: record.Modified}
	record.Key = uploadKey(mapping, file, "overwrite")
	info := pan115.FileInfo{File: pan115.File{ID: "43", ParentID: "10", Name: "movie.srt", PickCode: "pc43", Size: 8, SHA1: record.SHA1, SizeKnown: true}, Path: []pan115.Directory{{ID: "10", Name: "movies"}}}
	if err = os.WriteFile(filepath.Join(s.root, "movies"), []byte("blocked directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessLocalUpload(ctx, "123", record, info); err == nil {
		t.Fatal("destination failure acknowledged")
	}
	got, err := repo.Find(ctx, "123", "43")
	if err != nil || got == nil || got.State != "pending" {
		t.Fatalf("pending receipt=%+v %v", got, err)
	}
	if err = os.Remove(filepath.Join(s.root, "movies")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(s.root, "movies"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.root, "movies", "movie.srt"), []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if handled, err := s.ResumeLocalUpload(ctx, record); !handled || err == nil {
		t.Fatal("different existing attachment accepted")
	}
	if err = os.Remove(filepath.Join(s.root, "movies", "movie.srt")); err != nil {
		t.Fatal(err)
	}
	s.SetUploadReceipts(database.NewUploadReceiptRepository(db.SQLDB(), database.DialectSQLite))
	store := uploadMemoryStore{record.Key: record}
	upload := &UploadService{store: store, provider: func(context.Context, string) (uploadRemote, error) {
		t.Fatal("copy recovery accessed remote")
		return nil, errors.New("unexpected")
	}}
	upload.SetLocalUploadRecovery(s.ResumeLocalUpload, s.BeginLocalUpload)
	if state, err := upload.process(ctx, mapping, file, "overwrite"); err != nil || state != "completed" {
		t.Fatalf("resume=%v %v", state, err)
	}
	body, err := os.ReadFile(filepath.Join(s.root, "movies", "movie.srt"))
	if err != nil || string(body) != "subtitle" {
		t.Fatalf("copy=%q %v", body, err)
	}
	if api.infoCalls != 0 || api.playFile != "" {
		t.Fatal("recovery called 115")
	}
}

// TestUploadPostprocessCopiesBeforeMoveAndDeduplicatesEvents exercises the real durable receipt and disk pipeline.
func TestUploadPostprocessCopiesBeforeMoveAndDeduplicatesEvents(t *testing.T) {
	ctx := context.Background()
	s, api, _, mapping, values := managedFixture(t)
	values[pan115LocalUploadEnabledKey] = "true"
	values[strmDownloadEnableSettingKey] = "true"
	db, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "post.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s.SetUploadReceipts(database.NewUploadReceiptRepository(db.SQLDB(), database.DialectSQLite))
	local := t.TempDir()
	body := []byte("local subtitle")
	if err = os.WriteFile(filepath.Join(local, "movie.srt"), body, 0600); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(filepath.Join(local, "movie.srt"))
	record := domain.UploadRecord{Key: "upload-1", Kind: "115", Source: filepath.Join(local, "movie.srt"), LocalRoot: local, Relative: "movie.srt", Size: int64(len(body)), Modified: stat.ModTime().UnixNano(), SHA1: fmt.Sprintf("%x", sha1.Sum(body))}
	info := pan115.FileInfo{File: pan115.File{ID: "43", ParentID: "10", Name: "movie.srt", PickCode: "pc43", Size: int64(len(body)), SizeKnown: true, SHA1: record.SHA1}, Path: []pan115.Directory{{ID: "10", Name: "movies"}}}
	if err = s.ProcessLocalUpload(ctx, "123", record, info); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(s.root, "movies", "movie.srt"))
	if err != nil || string(got) != string(body) {
		t.Fatalf("copy=%q err=%v", got, err)
	}
	if err = os.Remove(record.Source); err != nil {
		t.Fatal(err)
	}
	// Rebuild the repository to exercise persisted completion after the move source is gone.
	s.SetUploadReceipts(database.NewUploadReceiptRepository(db.SQLDB(), database.DialectSQLite))
	if err = s.ProcessLocalUpload(ctx, "123", record, info); err != nil {
		t.Fatal(err)
	}
	events := []pan115.LifeEvent{{Type: 2, FileID: "43", ParentID: "10", UpdatedAt: time.Now().Unix()}}
	ctx = context.WithValue(ctx, managedAccountKey{}, "123")
	if _, err = s.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, events, false); err != nil {
		t.Fatal(err)
	}
	if api.infoCalls != 0 || api.playFile != "" {
		t.Fatalf("redundant APIs info=%d play=%v", api.infoCalls, api.playFile)
	}
	// A move must still consult the remote state even though the file has a local receipt.
	api.infos["43"] = info
	events[0].Type = 6
	if _, err = s.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, events, false); err != nil {
		t.Fatal(err)
	}
	if api.infoCalls != 1 {
		t.Fatalf("move info calls=%d", api.infoCalls)
	}
}
