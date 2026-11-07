package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

type directUploadRemote struct {
	uploadMemoryRemote
	entries       []uploadEntry
	names         []string
	lostResponse  bool
	deleteFailure bool
	corrupt       bool
}

func (remote *directUploadRemote) List(context.Context, string) ([]uploadEntry, error) {
	return append([]uploadEntry(nil), remote.entries...), nil
}
func (remote *directUploadRemote) Upload(_ context.Context, _, name string, file *os.File, size int64) error {
	remote.names = append(remote.names, name)
	body, err := io.ReadAll(io.NewSectionReader(file, 0, size))
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha1.Sum(body))
	if remote.corrupt {
		hash = "incorrect"
	}
	remote.entries = append(remote.entries, uploadEntry{ID: fmt.Sprintf("new-%d", len(remote.names)), Name: name, Size: size, SHA1: hash})
	if remote.lostResponse {
		remote.lostResponse = false
		return errors.New("response lost")
	}
	return nil
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
