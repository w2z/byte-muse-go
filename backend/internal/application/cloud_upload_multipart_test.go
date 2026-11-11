package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
)

type multipartRemoteStub struct {
	receiptUploadRemote
	aborts        int
	abortErr      error
	failTransfer  bool
	seenSession   string
	missingParent string
}

func (r *multipartRemoteStub) List(ctx context.Context, parent string) ([]uploadEntry, error) {
	if parent == r.missingParent && parent != "" {
		return nil, &pan115.APIError{Code: 20018, Message: "目录不存在"}
	}
	return r.receiptUploadRemote.List(ctx, parent)
}

func TestMultipartMissingParentCleansBeforeRecreation(t *testing.T) {
	s, m, f, _, store := recoveryFixture(t)
	r := &multipartRemoteStub{missingParent: "deleted", abortErr: errors.New("offline")}
	s.provider = func(context.Context, string) (uploadRemote, error) { return r, nil }
	key := uploadKey(m, f, "overwrite")
	store[key] = domain.UploadRecord{Key: key, Kind: "115", Root: m.ID, State: "pending", Parent: "deleted", Stage: f.Relative, Target: f.Relative, Relative: f.Relative, Source: f.Absolute, LocalRoot: m.LocalPath, Size: f.Size, Modified: f.Modified, Policy: "overwrite", Multipart: &domain.UploadMultipart{UploadID: "session", Parent: "deleted"}}
	if _, err := s.process(context.Background(), m, f, "overwrite"); !errors.Is(err, errUploadRemoteMissing) {
		t.Fatalf("missing parent: %v", err)
	}
	if _, err := s.process(context.Background(), m, f, "overwrite"); err == nil {
		t.Fatal("recreated destination despite failed abort")
	}
	if store[key].Multipart == nil || store[key].Parent != "" || r.aborts != 1 || r.seenSession != "" || uploadRetriesExhausted(store[key]) {
		t.Fatalf("lost reset obligation: %+v aborts=%d", store[key], r.aborts)
	}
	r.abortErr = nil
	state, err := s.process(context.Background(), m, f, "overwrite")
	if err != nil || state != "completed" || store[key].Multipart != nil || store[key].Parent != "0" || r.seenSession != "" {
		t.Fatalf("recreate: %s %v %+v", state, err, store[key])
	}
}

func (r *multipartRemoteStub) AbortUpload(_ context.Context, session domain.UploadMultipart) error {
	r.aborts++
	if session.UploadID != "session" {
		return errors.New("wrong session")
	}
	return r.abortErr
}

func (r *multipartRemoteStub) UploadWithReceipt(ctx context.Context, parent, name string, f *os.File, size int64, hash string) (uploadEntry, error) {
	j := domain.UploadMultipartFromContext(ctx)
	if j == nil {
		return uploadEntry{}, errors.New("missing durable multipart journal")
	}
	if j.Session != nil {
		r.seenSession = j.Session.UploadID
	} else {
		if err := j.Save(ctx, &domain.UploadMultipart{UploadID: "session", Parent: parent, Name: name, Size: size, SHA1: hash}); err != nil {
			return uploadEntry{}, err
		}
	}
	if r.failTransfer {
		return uploadEntry{}, context.Canceled
	}
	return r.receiptUploadRemote.UploadWithReceipt(ctx, parent, name, f, size, hash)
}

func TestMultipartTaskResumeAndSuccessClearsCheckpoint(t *testing.T) {
	s, m, f, _, store := recoveryFixture(t)
	r := &multipartRemoteStub{failTransfer: true}
	s.provider = func(context.Context, string) (uploadRemote, error) { return r, nil }
	key := uploadKey(m, f, "overwrite")
	if _, err := s.process(context.Background(), m, f, "overwrite"); !errors.Is(err, context.Canceled) {
		t.Fatalf("first attempt: %v", err)
	}
	if store[key].Multipart == nil {
		t.Fatal("interrupted transfer lost checkpoint")
	}
	r.failTransfer = false
	restarted := &UploadService{store: store, provider: s.provider}
	state, err := restarted.process(context.Background(), m, f, "overwrite")
	if err != nil || state != "completed" || r.seenSession != "session" || store[key].Multipart != nil || store[key].StageID == "" {
		t.Fatalf("resume state=%s err=%v record=%+v", state, err, store[key])
	}
}

func TestMultipartStopAndDeleteRetainFailedCleanup(t *testing.T) {
	for _, action := range []string{"stop", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, m, f, _, store := recoveryFixture(t)
			r := &multipartRemoteStub{abortErr: errors.New("offline")}
			s.provider = func(context.Context, string) (uploadRemote, error) { return r, nil }
			key := uploadKey(m, f, "overwrite")
			record := domain.UploadRecord{Key: key, Kind: "115", State: "pending", Parent: "0", Stage: f.Relative, Target: f.Relative, Relative: f.Relative, Source: f.Absolute, LocalRoot: m.LocalPath, Multipart: &domain.UploadMultipart{UploadID: "session"}}
			next, ok := transformUploadControl(record, action, "running", true)
			if !ok {
				t.Fatal("control rejected")
			}
			store[key] = next
			if err := os.Remove(f.Absolute); err != nil {
				t.Fatal(err)
			} // cleanup cannot depend on the source still existing
			if _, err := s.process(context.Background(), m, f, "overwrite"); err == nil {
				t.Fatal("failed abort reported success")
			}
			if store[key].Multipart == nil || uploadTerminal(store[key].State) {
				t.Fatal("cleanup obligation lost")
			}
			r.abortErr = nil
			state, err := s.process(context.Background(), m, f, "overwrite")
			if err != nil || state != "stopped" || store[key].Multipart != nil || r.aborts != 2 || store[key].Hidden != (action == "delete") {
				t.Fatalf("cleanup=%s %v record=%+v", state, err, store[key])
			}
		})
	}
}

func TestMultipartPauseAndClearPolicies(t *testing.T) {
	r := domain.UploadRecord{Key: "task", Kind: "115", Parent: "0", State: "pending", Multipart: &domain.UploadMultipart{UploadID: "session"}}
	paused, ok := transformUploadControl(r, "pause", "running", true)
	if !ok || paused.Multipart == nil || paused.Intent != "pause" {
		t.Fatal("pause discarded resume state")
	}
	r.Error, r.ErrorKind, r.Retries = "network", "transient", 3
	store := uploadMemoryStore{r.Key: r}
	s := &UploadService{store: store, files: map[string]UploadFileProgress{}}
	if err := s.applyListClear(context.Background(), "clear_failed", "files"); err != nil {
		t.Fatal(err)
	}
	got := store[r.Key]
	if !got.Hidden || got.Intent != "delete" || got.State != "discarding" || got.Multipart == nil || !uploadRetryDue(got, time.Now()) {
		t.Fatalf("hidden cleanup lost: %+v", got)
	}
}

func TestExpiredMultipartSessionCanStartAgainAfterSafeReset(t *testing.T) {
	r := domain.UploadRecord{Key: "task", Kind: "115", State: "pending", Intent: "run", Retries: 3, ErrorKind: "transient", Error: "正在重试 (3/3)；OSS 断点已失效且目录未找到完成文件，下次重新上传"}
	if uploadRetriesExhausted(r) {
		t.Fatal("safe session reset remained exhausted")
	}
	next, ok := transformUploadControl(r, "start", "running", true)
	if !ok || next.Error != "" || next.ErrorKind != "" || next.Retries != 3 {
		t.Fatalf("start did not clear legacy reset error: %+v ok=%v", next, ok)
	}
}

func TestMultipartQueueCleansMissingSourceWithoutParent(t *testing.T) {
	s, m, f, _, store := recoveryFixture(t)
	r := &multipartRemoteStub{}
	s.provider = func(context.Context, string) (uploadRemote, error) { return r, nil }
	raw, _ := json.Marshal([]UploadMapping{m})
	s.settings = func(context.Context) (map[string]string, error) {
		return map[string]string{"CLOUD_UPLOAD_PATHS": string(raw)}, nil
	}
	key := uploadKey(m, f, "overwrite")
	store[key] = domain.UploadRecord{Key: key, Kind: "115", Root: m.ID, State: "pending", Intent: "run", Relative: f.Relative, Source: f.Absolute, LocalRoot: m.LocalPath, Size: f.Size, Modified: f.Modified, Policy: "overwrite", Multipart: &domain.UploadMultipart{UploadID: "session"}}
	if err := os.Remove(f.Absolute); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(5 * time.Second)
	for {
		record, err := store.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if uploadTerminal(record.State) {
			cancel()
			<-done
			if record.Multipart != nil || r.aborts != 1 || record.State != "discarded" {
				t.Fatalf("orphaned session: %+v aborts=%d", record, r.aborts)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("cleanup not scheduled")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
