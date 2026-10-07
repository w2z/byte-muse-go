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
