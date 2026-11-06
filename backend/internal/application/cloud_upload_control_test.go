package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"testing"
)

func TestUploadControlsPreserveReceiptsAndRecoverMode(t *testing.T) {
	store := uploadMemoryStore{"done": {Key: "done", State: "completed"}, "wait": {Key: "wait", State: "pending"}}
	s := &UploadService{store: store, ready: true, status: UploadStatus{Enabled: true}, mode: "running", files: map[string]UploadFileProgress{"done": {State: "completed"}, "wait": {State: "waiting"}}}
	ctx := context.Background()
	if err := s.applyUploadControl(ctx, "pause"); err != nil {
		t.Fatal(err)
	}
	control, _ := store.Get(ctx, uploadControlKey)
	if control == nil || control.Intent != "paused" {
		t.Fatal("pause lost across restart")
	}
	if err := s.applyUploadControl(ctx, "clear_completed"); err != nil {
		t.Fatal(err)
	}
	if !store["done"].Hidden || store["wait"].Hidden {
		t.Fatal("wrong records cleared")
	}
	if err := s.applyUploadControl(ctx, "resume"); err != nil {
		t.Fatal(err)
	}
	if err := s.applyUploadControl(ctx, "stop"); err != nil {
		t.Fatal(err)
	}
	if store["wait"].State != "stopped" {
		t.Fatalf("%+v", store["wait"])
	}
	if s.Status().Actions["resume"] {
		t.Fatal("stop incorrectly treated as pause")
	}
	if err := s.applyUploadControl(ctx, "delete_all"); err != nil {
		t.Fatal(err)
	}
	if !store["wait"].Hidden || !store["done"].Hidden {
		t.Fatal("records still visible")
	}
	if len(store) != 3 {
		t.Fatal("deduplication receipts removed: unchanged files would upload again")
	}
}

func TestUploadDeleteDefersCleanupWithoutErasingRecovery(t *testing.T) {
	store := uploadMemoryStore{"active": domain.UploadRecord{Key: "active", State: "cleanup", Backup: "owned-backup"}}
	s := &UploadService{store: store, mode: "running"}
	if err := s.applyUploadControl(context.Background(), "delete_all"); err != nil {
		t.Fatal(err)
	}
	r := store["active"]
	if r.Hidden || r.State != "cleanup" || r.Intent != "delete" || r.Backup == "" {
		t.Fatalf("recovery lost: %+v", r)
	}
}

func TestScopedUploadControlChangesOnlySelectedFiles(t *testing.T) {
	ctx := context.Background()
	store := uploadMemoryStore{
		"a1": {Key: "a1", Kind: "115", Root: "1", LocalRoot: "/a", State: "pending"},
		"a2": {Key: "a2", Kind: "115", Root: "1", LocalRoot: "/a", State: "pending"},
		"b1": {Key: "b1", Kind: "115", Root: "2", LocalRoot: "/b", State: "pending"},
	}
	s := &UploadService{store: store, mode: "running", files: map[string]UploadFileProgress{}}
	s.status.Enabled = true
	directory := uploadDirectoryKey(UploadMapping{Kind: "115", ID: "1", LocalPath: "/a"})
	if err := s.applyScopedUploadControl(ctx, "pause", "file", "a1"); err != nil {
		t.Fatal(err)
	}
	if store["a1"].Intent != "pause" || store["a2"].Intent != "" || store["b1"].State != "pending" || s.mode != "running" {
		t.Fatalf("file pause leaked: %+v mode=%s", store, s.mode)
	}
	if err := s.applyScopedUploadControl(ctx, "stop", "directory", directory); err != nil {
		t.Fatal(err)
	}
	if store["a1"].State != "stopped" || store["a2"].State != "stopped" || store["b1"].State != "pending" {
		t.Fatalf("directory stop leaked: %+v", store)
	}
	if err := s.applyScopedUploadControl(ctx, "start", "directory", directory); err != nil {
		t.Fatal(err)
	}
	if store["a1"].State != "pending" || store["a1"].Intent != "" || store["a2"].State != "pending" || store["b1"].State != "pending" {
		t.Fatalf("directory start leaked: %+v", store)
	}
	store["a3"] = domain.UploadRecord{Key: "a3", Kind: "115", Root: "1", LocalRoot: "/a", State: "uploaded"}
	if err := s.applyScopedUploadControl(ctx, "pause", "directory", directory); err != nil {
		t.Fatal(err)
	}
	if store["a1"].Intent != "pause" || store["a2"].Intent != "pause" || store["a3"].Intent != "pause" || store["a3"].State != "uploaded" || store["b1"].Intent != "" {
		t.Fatalf("directory pause missed uploaded file: %+v", store)
	}
	paused := false
	for _, item := range s.Files(1, 10).Items {
		if item.Key == "a3" && item.State == "paused" && item.Actions["start"] && !item.Actions["pause"] {
			paused = true
		}
	}
	if !paused {
		t.Fatalf("uploaded file not presented as paused: %+v", s.Files(1, 10).Items)
	}
	s.status.Enabled = false
	if err := s.applyScopedUploadControl(ctx, "start", "directory", directory); err != nil {
		t.Fatal(err)
	}
	if store["a1"].Intent != "run" || store["a2"].Intent != "run" || store["a3"].Intent != "run" || store["b1"].Intent != "" || s.mode != "running" {
		t.Fatalf("unmonitored directory start leaked: %+v mode=%s", store, s.mode)
	}
	if err := s.applyScopedUploadControl(ctx, "delete", "file", "b1"); err != nil {
		t.Fatal(err)
	}
	if !store["b1"].Hidden || store["a1"].Hidden || s.Files(1, 10).Total != 3 {
		t.Fatalf("delete leaked: %+v files=%+v", store["b1"], s.Files(1, 10))
	}
	if err := s.applyScopedUploadControl(ctx, "delete", "directory", directory); err != nil {
		t.Fatal(err)
	}
	if !store["a1"].Hidden || !store["a2"].Hidden || store["a3"].State != "discarding" || store["a3"].Intent != "delete" || store["a3"].Hidden || !store["b1"].Hidden {
		t.Fatalf("directory delete leaked: %+v", store)
	}
	if s.Directories() != nil && len(s.Directories()) != 0 {
		t.Fatal("directories require configured mappings")
	}
}
