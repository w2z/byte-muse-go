package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

type receiptUploadRemote struct {
	directUploadRemote
	lists      int
	receiptBad bool
}

func (r *receiptUploadRemote) List(ctx context.Context, parent string) ([]uploadEntry, error) {
	r.lists++
	return r.directUploadRemote.List(ctx, parent)
}
func (r *receiptUploadRemote) UploadWithReceipt(ctx context.Context, parent, name string, f *os.File, size int64, hash string) (uploadEntry, error) {
	if err := r.directUploadRemote.Upload(ctx, parent, name, f, size, hash); err != nil {
		return uploadEntry{}, err
	}
	e := r.entries[len(r.entries)-1]
	e.Info = &pan115.FileInfo{File: pan115.File{ID: e.ID, Name: name, ParentID: parent, PickCode: "pc-new", SHA1: hash, Size: size, SizeKnown: true}}
	if r.receiptBad {
		e.SHA1 = "bad"
	}
	return e, nil
}

func TestDirectUploadReceiptAvoidsVerificationList(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			s, m, f, _, store := recoveryFixture(t)
			r := &receiptUploadRemote{receiptBad: bad}
			s.provider = func(context.Context, string) (uploadRemote, error) { return r, nil }
			state, err := s.process(context.Background(), m, f, "overwrite")
			if err != nil || state != "completed" {
				t.Fatalf("state=%s err=%v", state, err)
			}
			want := 1
			if bad {
				want = 2
			}
			if r.lists != want {
				t.Fatalf("list calls=%d want=%d", r.lists, want)
			}
			if store[uploadKey(m, f, "overwrite")].StageID != "new-1" {
				t.Fatal("receipt identity not persisted")
			}
		})
	}
}

func TestDirectReceiptMoveStillVerifiesSourceAndRecovery(t *testing.T) {
	for _, recovering := range []bool{false, true} {
		t.Run(fmt.Sprint(recovering), func(t *testing.T) {
			s, m, f, _, store := recoveryFixture(t)
			r := &receiptUploadRemote{}
			s.provider = func(context.Context, string) (uploadRemote, error) { return r, nil }
			key := uploadKey(m, f, "overwrite")
			store[key] = domain.UploadRecord{Key: key, Kind: "115", Root: m.ID, LocalRoot: m.LocalPath, Source: f.Absolute, Relative: f.Relative, Size: f.Size, Modified: f.Modified, Mode: "move", Policy: "overwrite", State: "pending"}
			if recovering {
				// A previous verified upload is not proof it still exists when a move resumes.
				record := store[key]
				record.State = "cleanup"
				record.Parent = "0"
				record.Target = f.Relative
				record.Stage = f.Relative
				record.StageID = "missing"
				record.SHA1 = "old-hash"
				store[key] = record
			}
			state, err := s.process(context.Background(), m, f, "overwrite")
			if recovering {
				if err == nil || r.lists == 0 {
					t.Fatalf("move recovery skipped remote: %s %v", state, err)
				}
				if _, err = os.Stat(f.Absolute); err != nil {
					t.Fatal("source lost", err)
				}
				return
			}
			if err != nil || state != "completed" || r.lists != 1 {
				t.Fatalf("move=%s lists=%d err=%v", state, r.lists, err)
			}
			if _, err = os.Stat(f.Absolute); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("source not moved", err)
			}
		})
	}
}

func TestDirectReceiptOverwriteRetainsIdentityChecks(t *testing.T) {
	s, m, f, _, _ := recoveryFixture(t)
	r := &receiptUploadRemote{directUploadRemote: directUploadRemote{entries: []uploadEntry{{ID: "old", Name: f.Relative, Size: 3, SHA1: "old"}}}}
	r.afterDirectUpload = func() { r.entries[0].SHA1 = "external-change" }
	s.provider = func(context.Context, string) (uploadRemote, error) { return r, nil }
	if _, err := s.process(context.Background(), m, f, "overwrite"); err == nil {
		t.Fatal("changed old file deleted using upload receipt")
	}
	if len(r.entries) != 2 {
		t.Fatal("old file lost")
	}
}

type directUploadRemote struct {
	uploadMemoryRemote
	entries           []uploadEntry
	names             []string
	lostResponse      bool
	deleteFailure     bool
	corrupt           bool
	afterDirectUpload func()
}

func (remote *directUploadRemote) List(context.Context, string) ([]uploadEntry, error) {
	return append([]uploadEntry(nil), remote.entries...), nil
}
func (remote *directUploadRemote) Upload(_ context.Context, _, name string, file *os.File, size int64, sourceSHA1 string) error {
	remote.names = append(remote.names, name)
	body, err := io.ReadAll(io.NewSectionReader(file, 0, size))
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha1.Sum(body))
	if sourceSHA1 != hash {
		return fmt.Errorf("source SHA1 not forwarded: %s", sourceSHA1)
	}
	if remote.corrupt {
		hash = "incorrect"
	}
	remote.entries = append(remote.entries, uploadEntry{ID: fmt.Sprintf("new-%d", len(remote.names)), Name: name, Size: size, SHA1: hash})
	if remote.afterDirectUpload != nil {
		remote.afterDirectUpload()
	}
	if remote.lostResponse {
		remote.lostResponse = false
		return errors.New("response lost")
	}
	return nil
}

func TestUploadDirectMultipleOldFilesSurviveRestart(t *testing.T) {
	service, mapping, file, _, store := recoveryFixture(t)
	remote := &directUploadRemote{entries: []uploadEntry{
		{ID: "old-a", Name: "film.mkv", Size: 8, SHA1: "hash-a"},
		{ID: "old-b", Name: "film.mkv", Size: 10, SHA1: "hash-b"},
	}, lostResponse: true}
	service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
	_, err := service.process(context.Background(), mapping, file, "overwrite")
	if err == nil || len(remote.names) != 1 || len(remote.entries) != 3 {
		t.Fatalf("upload should retain both originals on lost response: %v uploads=%v entries=%v", err, remote.names, remote.entries)
	}
	key := uploadKey(mapping, file, "overwrite")
	raw, _ := json.Marshal(store[key])
	var restored domain.UploadRecord
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	store[key] = restored
	restarted := &UploadService{store: store, provider: service.provider}
	state, err := restarted.process(context.Background(), mapping, file, "overwrite")
	if err != nil || state != "completed" || len(remote.names) != 1 || len(remote.entries) != 1 || remote.entries[0].ID != "new-1" {
		t.Fatalf("state=%s err=%v uploads=%v entries=%v", state, err, remote.names, remote.entries)
	}
}

func TestUploadDirectPreservesLateDuplicate(t *testing.T) {
	service, mapping, file, _, _ := recoveryFixture(t)
	remote := &directUploadRemote{entries: []uploadEntry{{ID: "old-a", Name: "film.mkv", Size: 8, SHA1: "hash-a"}}}
	remote.afterDirectUpload = func() {
		remote.entries = append(remote.entries, uploadEntry{ID: "late", Name: "film.mkv", Size: 7, SHA1: "late-hash"})
	}
	service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
	_, err := service.process(context.Background(), mapping, file, "overwrite")
	if !errors.Is(err, errUploadAttention) || len(remote.entries) != 3 {
		t.Fatalf("late duplicate must not be adopted or deleted: %v %v", err, remote.entries)
	}
}

func TestUploadDirectMultipleOldFilesRejectChangedOrUnknownIdentity(t *testing.T) {
	for _, scenario := range []string{"missing-hash", "changed-after-upload", "partial-delete-restart"} {
		t.Run(scenario, func(t *testing.T) {
			service, mapping, file, _, store := recoveryFixture(t)
			remote := &directUploadRemote{entries: []uploadEntry{
				{ID: "old-a", Name: "film.mkv", Size: 8, SHA1: "hash-a"},
				{ID: "old-b", Name: "film.mkv", Size: 10, SHA1: "hash-b"},
			}}
			if scenario == "missing-hash" {
				remote.entries[1].SHA1 = ""
			}
			if scenario == "changed-after-upload" {
				remote.afterDirectUpload = func() { remote.entries[1].SHA1 = "changed" }
			}
			if scenario == "partial-delete-restart" {
				remote.deleteFailure = true
			}
			service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
			_, err := service.process(context.Background(), mapping, file, "overwrite")
			if err == nil || len(remote.entries) < 2 {
				t.Fatal("unsafe success", err, remote.entries)
			}
			if scenario == "missing-hash" && len(remote.names) != 0 {
				t.Fatal("uploaded without safe old identities")
			}
			if scenario != "partial-delete-restart" {
				return
			}
			remote.entries = remote.entries[1:]
			remote.deleteFailure = false
			restarted := &UploadService{store: store, provider: service.provider}
			state, err := restarted.process(context.Background(), mapping, file, "overwrite")
			if err != nil || state != "completed" || len(remote.entries) != 1 || len(remote.names) != 1 {
				t.Fatalf("partial cleanup not resumed safely: %s %v %v", state, err, remote.entries)
			}
		})
	}
}
func (remote *directUploadRemote) Rename(context.Context, string, uploadEntry, string) error {
	return errors.New("direct uploads must not rename")
}
func (remote *directUploadRemote) Delete(_ context.Context, _ string, entry uploadEntry) error {
	if remote.deleteFailure {
		return errors.New("delete failed")
	}
	for index, candidate := range remote.entries {
		if candidate.ID == entry.ID {
			remote.entries = append(remote.entries[:index], remote.entries[index+1:]...)
			return nil
		}
	}
	return nil
}

func TestUploadDirect115PreservesOldUntilVerified(t *testing.T) {
	for _, scenario := range []string{"normal", "lost_response", "delete_failure", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			service, mapping, file, _, store := recoveryFixture(t)
			remote := &directUploadRemote{entries: []uploadEntry{{ID: "old", Name: "film.mkv", Size: 9, SHA1: "old-hash"}}, lostResponse: scenario == "lost_response", deleteFailure: scenario == "delete_failure", corrupt: scenario == "corrupt"}
			service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
			state, err := service.process(context.Background(), mapping, file, "overwrite")
			if len(remote.names) != 1 || remote.names[0] != "film.mkv" {
				t.Fatalf("not direct: %v state=%s err=%v", remote.names, state, err)
			}
			if scenario != "normal" {
				if err == nil || len(remote.entries) != 2 {
					t.Fatalf("old object not retained: %v %v", remote.entries, err)
				}
				if scenario == "corrupt" {
					return
				}
				remote.deleteFailure = false
				state, err = service.process(context.Background(), mapping, file, "overwrite")
			}
			if err != nil || state != "completed" || len(remote.entries) != 1 || remote.entries[0].ID == "old" || len(remote.names) != 1 {
				t.Fatalf("state=%s err=%v entries=%v uploads=%v record=%+v", state, err, remote.entries, remote.names, store[uploadKey(mapping, file, "overwrite")])
			}
		})
	}
}

func TestUploadDirectStopPreservesFinalAndOldFiles(t *testing.T) {
	service, mapping, file, _, store := recoveryFixture(t)
	remote := &directUploadRemote{entries: []uploadEntry{{ID: "old", Name: "film.mkv", Size: 9, SHA1: "old-hash"}}, lostResponse: true}
	service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
	_, _ = service.process(context.Background(), mapping, file, "overwrite")
	key := uploadKey(mapping, file, "overwrite")
	record := store[key]
	record.Intent = "delete"
	record.Hidden = true
	store[key] = record
	state, err := service.process(context.Background(), mapping, file, "overwrite")
	if err != nil || state != "stopped" || len(remote.entries) != 2 {
		t.Fatalf("state=%s err=%v entries=%v", state, err, remote.entries)
	}
}

func TestUploadDirectLegacy115LeavesTemporaryObjectsUntouched(t *testing.T) {
	service, mapping, file, _, store := recoveryFixture(t)
	key := uploadKey(mapping, file, "overwrite")
	store[key] = domain.UploadRecord{Key: key, Kind: "115", State: "committing", Intent: "run", Parent: "0", Target: "film.mkv", Stage: "film.bytemuse-go-tmp", StageID: "legacy", Source: file.Absolute, Relative: file.Relative, Size: file.Size, Modified: file.Modified}
	remote := &directUploadRemote{entries: []uploadEntry{{ID: "legacy", Name: "film.bytemuse-go-tmp", Size: 9}}}
	service.provider = func(context.Context, string) (uploadRemote, error) { return remote, nil }
	state, err := service.process(context.Background(), mapping, file, "overwrite")
	if err != nil || state != "completed" || len(remote.entries) != 2 || remote.entries[0].ID != "legacy" || len(remote.names) != 1 || remote.names[0] != "film.mkv" {
		t.Fatalf("state=%s err=%v entries=%v", state, err, remote.entries)
	}
}
