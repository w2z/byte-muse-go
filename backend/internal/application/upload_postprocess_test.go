package application

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

type localUploadRemote struct{ *directUploadRemote }

func (r localUploadRemote) AccountID(context.Context) (string, error) { return "123", nil }

func TestStrmConflictPolicyRequiresMatchingUploadReceipt(t *testing.T) {
	for _, receipt := range []*ports.UploadReceipt{nil, {FileID: "other"}} {
		s, _, _, _, _ := managedFixture(t)
		ctx := context.WithValue(context.Background(), localUploadPolicyKey{}, "overwrite")
		if receipt != nil {
			ctx = context.WithValue(ctx, localUploadSourceKey{}, receipt)
		}
		path := filepath.Join(s.root, "movie.strm")
		const previous = "https://media.example.com/files/play/115/old\n"
		if err := os.WriteFile(path, []byte(previous), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := s.generateStrmFile(ctx, s.root, s.root, "115", "https://media.example.com", domain.StrmGenerateIdempotent, strmSourceFile{ID: "42", PickCode: "pc42", Name: "movie.mkv"})
		if err == nil {
			t.Fatal("unrelated event overwrote existing STRM")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != previous {
			t.Fatalf("existing STRM=%q err=%v", data, err)
		}
	}
}

// Exercise the actual receipt-to-STRM pipeline, including retries after the file was written.
func TestLocalUploadStrmConflictPolicies(t *testing.T) {
	const old = "https://media.example.com/files/play/115/old\n"
	const current = "https://media.example.com/files/play/115/pc42\n"
	for _, tc := range []struct {
		name, policy, existing, want string
		count                        int
		wantErr                      bool
	}{
		{"overwrite", "overwrite", old, current, 2, false},
		{"skip", "skip", old, old, 2, false},
		{"skip_same_source_old_host", "skip", "https://old.example.com/files/play/115/pc42\n", "https://old.example.com/files/play/115/pc42\n", 2, false},
		{"keep_both", "keep_both", old, old, 3, false},
		{"same_source", "keep_both", current, current, 2, false},
		{"missing_policy", "", old, old, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, api, managed, _, values := managedFixture(t)
			values[pan115LocalUploadEnabledKey] = "true"
			db, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "strm-policy.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			receipts := database.NewUploadReceiptRepository(db.SQLDB(), database.DialectSQLite)
			s.SetUploadReceipts(receipts)
			info := api.infos["42"]
			info.SHA1, info.Size, info.SizeKnown = "verified", 100, true
			record := domain.UploadRecord{Key: "video", StageID: "42", Policy: tc.policy, SHA1: "verified", Size: 100, LocalRoot: t.TempDir(), Relative: "movie.mkv"}
			dest := filepath.Join(s.root, "movies")
			if err = os.MkdirAll(dest, 0755); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"movie.strm": tc.existing, "movie (1).strm": "another file\n"} {
				if err = os.WriteFile(filepath.Join(dest, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				if attempt == 1 {
					if _, err = db.SQLDB().ExecContext(ctx, "UPDATE upload_postprocess SET state='pending'"); err != nil {
						t.Fatal(err)
					}
				}
				err = s.ProcessLocalUpload(ctx, "123", record, info)
				if (err != nil) != tc.wantErr {
					t.Fatalf("attempt %d: %v", attempt, err)
				}
				receipt, err := receipts.Get(ctx, journalKey("123", record.Key, info.ID))
				if err != nil || receipt == nil {
					t.Fatalf("receipt=%+v err=%v", receipt, err)
				}
				if (receipt.State == "completed") == tc.wantErr {
					t.Fatalf("state=%s", receipt.State)
				}
			}
			for name, want := range map[string]string{"movie.strm": tc.want, "movie (1).strm": "another file\n"} {
				body, err := os.ReadFile(filepath.Join(dest, name))
				if err != nil || string(body) != want {
					t.Fatalf("%s=%q err=%v", name, body, err)
				}
			}
			if tc.count == 3 {
				body, err := os.ReadFile(filepath.Join(dest, "movie (2).strm"))
				if err != nil || string(body) != current {
					t.Fatalf("numbered output=%q err=%v", body, err)
				}
			}
			entries, err := os.ReadDir(dest)
			if err != nil || len(entries) != tc.count {
				t.Fatalf("outputs=%d err=%v", len(entries), err)
			}
			if tc.name == "overwrite" || tc.name == "keep_both" {
				if len(managed.items) != 1 {
					t.Fatalf("ownership records=%d", len(managed.items))
				}
				for _, item := range managed.items {
					if item.FileID != "42" {
						t.Fatalf("wrong owner: %+v", item)
					}
				}
			}
			if tc.policy == "skip" && len(managed.items) != 0 {
				t.Fatal("skip acquired ownership of an existing file")
			}
			if api.infoCalls != 0 || api.playFile != "" {
				t.Fatal("local STRM accessed remote")
			}
		})
	}
}

// Local attachments consume the task policy and remain idempotent before receipt completion.
func TestLocalUploadAttachmentConflictPolicies(t *testing.T) {
	for _, policy := range []string{"skip", "overwrite", "keep_both"} {
		t.Run(policy, func(t *testing.T) {
			s, api, _, _, values := managedFixture(t)
			values[pan115LocalUploadEnabledKey], values[strmDownloadEnableSettingKey] = "true", "true"
			ctx := context.Background()
			db, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "policy.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			s.SetUploadReceipts(database.NewUploadReceiptRepository(db.SQLDB(), database.DialectSQLite))
			local := t.TempDir()
			source := filepath.Join(local, "movie.srt")
			if err = os.WriteFile(source, []byte("new subtitle"), 0600); err != nil {
				t.Fatal(err)
			}
			stat, _ := os.Stat(source)
			r := domain.UploadRecord{Key: "policy", Kind: "115", Policy: policy, Mode: "move", Source: source, LocalRoot: local, Relative: "movie.srt", Size: stat.Size(), Modified: stat.ModTime().UnixNano(), SHA1: fmt.Sprintf("%x", sha1.Sum([]byte("new subtitle")))}
			info := pan115.FileInfo{File: pan115.File{ID: "43", ParentID: "10", Name: "movie.srt", PickCode: "pc43", Size: r.Size, SizeKnown: true, SHA1: r.SHA1}, Path: []pan115.Directory{{ID: "10", Name: "movies"}}}
			dest := filepath.Join(s.root, "movies")
			if err = os.MkdirAll(dest, 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dest, "movie.srt"), []byte("old subtitle"), 0600); err != nil {
				t.Fatal(err)
			}
			// An occupied numbered file must also be preserved.
			if err = os.WriteFile(filepath.Join(dest, "movie (1).srt"), []byte("another subtitle"), 0600); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if attempt == 1 {
					// Simulate interruption after output installation but before receipt completion.
					if _, err = db.SQLDB().ExecContext(ctx, "UPDATE upload_postprocess SET state='pending'"); err != nil {
						t.Fatal(err)
					}
				}
				if err = s.ProcessLocalUpload(ctx, "123", r, info); err != nil {
					t.Fatal(err)
				}
			}
			want := "old subtitle"
			if policy == "overwrite" {
				want = "new subtitle"
			}
			body, err := os.ReadFile(filepath.Join(dest, "movie.srt"))
			if err != nil || string(body) != want {
				t.Fatalf("target=%q err=%v", body, err)
			}
			body, err = os.ReadFile(filepath.Join(dest, "movie (1).srt"))
			if err != nil || string(body) != "another subtitle" {
				t.Fatal("existing numbered file replaced")
			}
			entries, err := os.ReadDir(dest)
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 2
			if policy == "keep_both" {
				wantCount = 3
				body, err = os.ReadFile(filepath.Join(dest, "movie (2).srt"))
				if err != nil || string(body) != "new subtitle" {
					t.Fatalf("retained copy=%q err=%v", body, err)
				}
			}
			if len(entries) != wantCount {
				t.Fatalf("retry produced extra outputs: %d", len(entries))
			}
			if _, err = os.Stat(source); err != nil {
				t.Fatal("postprocess removed move source", err)
			}
			if api.infoCalls != 0 || api.playFile != "" {
				t.Fatal("local policy accessed remote")
			}
		})
	}
}

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
	if err = s.copyUploadedMedia(context.Background(), dir, s.root, "movie.srt", strmSourceFile{ID: "43", Name: "movie.srt"}, receipt, false); err == nil {
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
	if handled, err := s.ResumeLocalUpload(ctx, record); !handled || !errors.Is(err, errLocalUploadConflict) {
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

// A durable local handoff failure must not open a remote client, including for move.
func TestMovePostprocessFailureRetriesLocally(t *testing.T) {
	s, m, f, _, store := recoveryFixture(t)
	key := uploadKey(m, f, "overwrite")
	store[key] = domain.UploadRecord{Key: key, Kind: "115", State: "cleanup", Mode: "move", Parent: "0", Stage: f.Relative, Target: f.Relative, StageID: "uploaded", Source: f.Absolute, LocalRoot: m.LocalPath, Relative: f.Relative, Size: f.Size, Modified: f.Modified}
	calls := 0
	s.provider = func(context.Context, string) (uploadRemote, error) {
		calls++
		return nil, errors.New("remote must not run")
	}
	s.SetLocalUploadRecovery(func(context.Context, domain.UploadRecord) (bool, error) {
		return true, errors.New("local disk unavailable")
	}, nil)
	_, err := s.process(context.Background(), m, f, "overwrite")
	if err == nil || calls != 0 {
		t.Fatalf("local retry accessed remote: calls=%d err=%v", calls, err)
	}
	if _, err = os.Stat(f.Absolute); err != nil {
		t.Fatal("source lost", err)
	}
}

func TestLocalConflictRejectsNonRegularTarget(t *testing.T) {
	s, _, _, _, values := managedFixture(t)
	local := t.TempDir()
	source := filepath.Join(local, "poster.jpg")
	body := []byte("source")
	if err := os.WriteFile(source, body, 0600); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(source)
	receipt := &ports.UploadReceipt{FileID: "42", ParentID: "10", Info: `{"id":"42"}`, LocalRoot: local, Relative: "poster.jpg", Size: int64(len(body)), SHA1: fmt.Sprintf("%x", sha1.Sum(body)), Modified: stat.ModTime().UnixNano()}
	target := filepath.Join(s.root, "movies", "poster.jpg")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), localUploadSourceKey{}, receipt)
	var conflict atomic.Bool
	ctx = context.WithValue(ctx, localUploadConflictKey{}, &conflict)
	_, err := s.downloadStrmMediaOnce(ctx, s.root, filepath.Join(s.root, "movies"), "115", strmSourceFile{ID: "42", Name: "poster.jpg", SizeKnown: true, Size: int64(len(body))}, domain.StrmGenerateIncremental)
	if !errors.Is(err, errLocalUploadConflict) || !conflict.Load() {
		t.Fatalf("non-regular target not classified: %v", err)
	}
	_ = values
}

func TestLocalConflictStopsAutomaticRetryAndAllowsManualRetry(t *testing.T) {
	s, m, f, _, store := recoveryFixture(t)
	key := uploadKey(m, f, "overwrite")
	store[key] = domain.UploadRecord{Key: key, Kind: "115", State: "cleanup", Mode: "move", Parent: "0", StageID: "installed", Source: f.Absolute}
	s.SetLocalUploadRecovery(func(context.Context, domain.UploadRecord) (bool, error) { return true, errLocalUploadConflict }, nil)
	_, err := s.process(context.Background(), m, f, "overwrite")
	r := store[key]
	if !errors.Is(err, errLocalUploadConflict) || uploadRetryDue(r, time.Now()) || uploadPresentationState(r, "", false, "running", true, true) != "failed" {
		t.Fatalf("conflict auto-retried: %+v %v", r, err)
	}
	retry, changed := transformUploadControl(r, "retry", "paused", false)
	if !changed || retry.Error != "" || !uploadRetryDue(retry, time.Now()) {
		t.Fatalf("manual retry unavailable: %+v", retry)
	}
}

func TestMoveLocalRecoveryVerifiesRemoteBeforeDeletingSource(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			s, m, f, _, store := recoveryFixture(t)
			body, _ := os.ReadFile(f.Absolute)
			hash := fmt.Sprintf("%x", sha1.Sum(body))
			key := uploadKey(m, f, "overwrite")
			store[key] = domain.UploadRecord{Key: key, Kind: "115", State: "cleanup", Mode: "move", Parent: "0", Target: f.Relative, Stage: f.Relative, StageID: "installed", Source: f.Absolute, LocalRoot: m.LocalPath, Relative: f.Relative, Size: f.Size, Modified: f.Modified, SHA1: hash}
			remote := &receiptUploadRemote{}
			if present {
				remote.entries = []uploadEntry{{ID: "installed", Name: f.Relative, Size: f.Size, SHA1: hash}}
			}
			s.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
			s.SetLocalUploadRecovery(func(context.Context, domain.UploadRecord) (bool, error) { return true, nil }, nil)
			state, err := s.process(context.Background(), m, f, "overwrite")
			_, statErr := os.Stat(f.Absolute)
			if remote.lists != 1 || len(remote.names) != 0 {
				t.Fatalf("reads=%d uploads=%d", remote.lists, len(remote.names))
			}
			if present {
				if err != nil || state != "completed" || !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("move=%s %v source=%v", state, err, statErr)
				}
			} else if err == nil || statErr != nil {
				t.Fatalf("unverified source deleted: %v %v", err, statErr)
			}
		})
	}
}
