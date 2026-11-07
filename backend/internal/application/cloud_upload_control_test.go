package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type uploadFastClearStore struct {
	uploadMemoryStore
	err error
}

func (store *uploadFastClearStore) List(context.Context) ([]domain.UploadRecord, error) {
	return nil, errors.New("unexpected full queue read")
}
func (store *uploadFastClearStore) HideCompleted(ctx context.Context, keys []string) error {
	if store.err != nil {
		return store.err
	}
	for _, key := range keys {
		record, _ := store.Get(ctx, key)
		record.Hidden = true
		if err := store.Save(ctx, *record); err != nil {
			return err
		}
	}
	return nil
}

func TestUploadFastClearCommitsBeforeUpdatingView(t *testing.T) {
	store := &uploadFastClearStore{uploadMemoryStore: uploadMemoryStore{
		"done": {Key: "done", State: "completed", StageID: "receipt"}, "waiting": {Key: "waiting", State: "pending"}, "skip": {Key: "skip", State: "skipped"},
	}, err: errors.New("write failed")}
	service := &UploadService{store: store, status: UploadStatus{Total: 3, Processed: 2, Uploaded: 1, Skipped: 1}, files: map[string]UploadFileProgress{
		"done": {Key: "done", DirectoryKey: "mixed", State: "completed"}, "waiting": {Key: "waiting", DirectoryKey: "mixed", State: "waiting"}, "skip": {Key: "skip", DirectoryKey: "done", State: "skipped"},
	}}
	ctx := context.Background()
	if err := service.applyListClear(ctx, "clear_completed", "files"); err == nil || len(service.files) != 3 {
		t.Fatal("failed commit changed view")
	}
	store.err = nil
	if err := service.applyListClear(ctx, "clear_completed", "directories"); err != nil {
		t.Fatal(err)
	}
	if len(service.files) != 2 || !store.uploadMemoryStore["skip"].Hidden || store.uploadMemoryStore["done"].Hidden {
		t.Fatal("directory scope changed mixed directory")
	}
	if err := service.applyListClear(ctx, "clear_completed", "files"); err != nil {
		t.Fatal(err)
	}
	if len(service.files) != 1 || service.status.Total != 1 || service.status.Processed != 0 || store.uploadMemoryStore["done"].StageID != "receipt" {
		t.Fatal("clear did not preserve pending task and receipt")
	}
}

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
	if !r.Hidden || r.State != "cleanup" || r.Intent != "delete" || r.Backup == "" {
		t.Fatalf("recovery lost: %+v", r)
	}
}

func TestUploadDeleteDoesNotRestartPendingTransfer(t *testing.T) {
	record := domain.UploadRecord{Key: "pending", State: "pending", Parent: "parent", Intent: "delete"}
	next, ok := transformUploadControl(record, "delete", "running", true)
	if !ok || next.State != "discarding" || next.Intent != "delete" {
		t.Fatalf("delete should discard pending transfer: %+v", next)
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
	for _, item := range s.Files(1, 10, "").Items {
		if item.Key == "a3" && item.State == "paused" && item.Actions["start"] && !item.Actions["pause"] {
			paused = true
		}
	}
	if !paused {
		t.Fatalf("uploaded file not presented as paused: %+v", s.Files(1, 10, "").Items)
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
	if !store["b1"].Hidden || store["a1"].Hidden || s.Files(1, 10, "").Total != 3 {
		t.Fatalf("delete leaked: %+v files=%+v", store["b1"], s.Files(1, 10, ""))
	}
	if err := s.applyScopedUploadControl(ctx, "delete", "directory", directory); err != nil {
		t.Fatal(err)
	}
	if !store["a1"].Hidden || !store["a2"].Hidden || store["a3"].State != "discarding" || store["a3"].Intent != "delete" || !store["a3"].Hidden || !store["b1"].Hidden {
		t.Fatalf("directory delete leaked: %+v", store)
	}
	if s.Directories("") != nil && len(s.Directories("")) != 0 {
		t.Fatal("directories require configured mappings")
	}
}

func TestUploadRetryBudgetStopsAfterThreeFailures(t *testing.T) {
	past := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	attention := domain.UploadRecord{Error: "需处理：远端文件身份无法确认", ErrorKind: "attention", Retries: 2, NextRetry: past}
	if !uploadRetryDue(attention, time.Now()) {
		t.Fatal("second failure must remain retryable")
	}
	attention.Retries = uploadMaxAutoRetries
	if uploadRetryDue(attention, time.Now()) {
		t.Fatal("third failure must wait for manual retry")
	}
	blocked := domain.UploadRecord{Error: "unauthorized", ErrorKind: "blocked", Retries: 5, NextRetry: past}
	if !uploadRetryDue(blocked, time.Now()) {
		t.Fatal("blocked account keeps its own schedule")
	}
	transient := domain.UploadRecord{Error: "传输失败", ErrorKind: "transient", Retries: 1, NextRetry: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	if !uploadRetryDue(transient, time.Now()) {
		t.Fatal("ordinary failures must retry immediately")
	}
}

func TestUploadExhaustedFailureHidesPauseAndStop(t *testing.T) {
	actions := uploadProgressActions("failed")
	if actions["pause"] || actions["stop"] || !actions["retry"] || !actions["delete"] {
		t.Fatalf("failed actions: %+v", actions)
	}
	for _, action := range []string{"pause", "stop"} {
		if _, allowed := transformUploadControl(domain.UploadRecord{State: "pending", Error: "failed", Retries: 3}, action, "running", true); allowed {
			t.Fatalf("exhausted task accepted %s", action)
		}
	}
}

func TestUploadRetryAllResetsEveryError(t *testing.T) {
	ctx := context.Background()
	store := uploadMemoryStore{
		"failed":  {Key: "failed", State: "pending", Error: "传输失败", ErrorKind: "transient", Retries: 3},
		"blocked": {Key: "blocked", State: "pending", Error: "账号失效", ErrorKind: "blocked", Retries: 1, NextRetry: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)},
	}
	service := &UploadService{store: store, ready: true, mode: "running", status: UploadStatus{Enabled: true}, files: map[string]UploadFileProgress{
		"failed":  {Key: "failed", State: "failed", Error: "传输失败"},
		"blocked": {Key: "blocked", State: "blocked", Error: "账号失效"},
	}}
	if !service.uploadActionsLocked()["retry_all"] {
		t.Fatal("retry_all should be available")
	}
	if err := service.applyUploadControl(ctx, "retry_all"); err != nil {
		t.Fatal(err)
	}
	for key, record := range store {
		if key == uploadControlKey {
			continue
		}
		if record.Retries != 0 || record.Error != "" || record.ErrorKind != "" || record.NextRetry != "" || record.Intent != "run" {
			t.Fatalf("retry_all did not reset %s: %+v", key, record)
		}
	}
}

func TestUploadRetryPresentationAndManualRestart(t *testing.T) {
	ctx := context.Background()
	store := uploadMemoryStore{
		"first":    {Key: "first", State: "committing", Error: "首次传输失败", ErrorKind: "transient", Retries: 1, Source: "/videos/first.mkv", Size: 4},
		"retrying": {Key: "retrying", State: "pending", Error: "需处理：远端文件身份无法确认", ErrorKind: "attention", Retries: 2, Source: "/videos/a.mkv", Size: 4},
		"failed":   {Key: "failed", State: "pending", Error: "传输失败", ErrorKind: "transient", Retries: 3, Source: "/videos/b.mkv", Size: 4},
	}
	s := &UploadService{store: store, mode: "running", status: UploadStatus{Enabled: true}, files: map[string]UploadFileProgress{}}
	if err := s.refreshUploadView(ctx); err != nil {
		t.Fatal(err)
	}
	items := map[string]UploadFileProgress{}
	for _, item := range s.Files(1, 10, "").Items {
		items[item.Key] = item
	}
	if items["first"].State != "retrying" || items["first"].Error != "首次传输失败" {
		t.Fatalf("first failure: %+v", items["first"])
	}
	if items["retrying"].State != "retrying" || items["retrying"].Actions["retry"] || !items["retrying"].Actions["pause"] || items["retrying"].MaxRetries != 3 {
		t.Fatalf("retrying: %+v", items["retrying"])
	}
	if items["failed"].State != "failed" || !items["failed"].Actions["retry"] || items["failed"].Actions["start"] {
		t.Fatalf("failed: %+v", items["failed"])
	}
	if len(s.Files(1, 10, "running").Items) != 2 || len(s.Files(1, 10, "failed").Items) != 1 {
		t.Fatal("retry filter mismatch")
	}
	if err := s.applyScopedUploadControl(ctx, "retry", "file", "failed"); err != nil {
		t.Fatal(err)
	}
	if store["failed"].Retries != 0 || store["failed"].Error != "" || store["failed"].Intent != "" {
		t.Fatalf("monitored retry: %+v", store["failed"])
	}
	s.status.Enabled = false
	store["failed"] = domain.UploadRecord{Key: "failed", State: "pending", Error: "传输失败", ErrorKind: "transient", Retries: 3, Source: "/videos/b.mkv", Size: 4}
	if err := s.applyScopedUploadControl(ctx, "retry", "file", "failed"); err != nil {
		t.Fatal(err)
	}
	if store["failed"].Intent != "run" || store["failed"].Retries != 0 || store["failed"].Error != "" {
		t.Fatalf("manual retry: %+v", store["failed"])
	}
}

func TestUploadListClearFollowsSelectedTab(t *testing.T) {
	ctx := context.Background()
	store := uploadMemoryStore{
		"d1": {Key: "d1", Kind: "115", Root: "1", LocalRoot: "/done", State: "completed"},
		"d2": {Key: "d2", Kind: "115", Root: "1", LocalRoot: "/done", State: "skipped"},
		"m1": {Key: "m1", Kind: "115", Root: "1", LocalRoot: "/mix", State: "completed"},
		"m2": {Key: "m2", Kind: "115", Root: "1", LocalRoot: "/mix", State: "pending"},
		"b1": {Key: "b1", Kind: "115", Root: "1", LocalRoot: "/bad", State: "pending", Error: "传输失败", ErrorKind: "transient", Retries: 3},
		"b2": {Key: "b2", Kind: "115", Root: "1", LocalRoot: "/bad", State: "pending", Error: "需处理：远端文件身份无法确认", ErrorKind: "attention", Retries: 3},
		"mb": {Key: "mb", Kind: "115", Root: "1", LocalRoot: "/mix", State: "pending", Error: "传输失败", ErrorKind: "transient", Retries: 3},
	}
	s := &UploadService{store: store, mode: "running", files: map[string]UploadFileProgress{}}
	if err := s.applyListClear(ctx, "clear_completed", "directories"); err != nil {
		t.Fatal(err)
	}
	if !store["d1"].Hidden || !store["d2"].Hidden || store["m1"].Hidden || store["b1"].Hidden {
		t.Fatalf("directory completed clear: %+v", store)
	}
	if err := s.applyListClear(ctx, "clear_completed", "files"); err != nil {
		t.Fatal(err)
	}
	if !store["m1"].Hidden || store["m2"].Hidden {
		t.Fatalf("file completed clear: %+v", store["m1"])
	}
	if err := s.applyListClear(ctx, "clear_failed", "directories"); err != nil {
		t.Fatal(err)
	}
	if !store["b1"].Hidden || !store["b2"].Hidden || store["mb"].Hidden || store["m2"].Hidden {
		t.Fatalf("directory failed clear: %+v", store)
	}
	if err := s.applyListClear(ctx, "clear_failed", "files"); err != nil {
		t.Fatal(err)
	}
	if !store["mb"].Hidden || store["m2"].Hidden {
		t.Fatalf("file failed clear: %+v", store["mb"])
	}
}
func TestUploadPresentationPreservesLivePhaseAndPause(t *testing.T) {
	if got := uploadPresentationState(domain.UploadRecord{State: "pending"}, "waiting", true, "running", true, false); got != "uploading" {
		t.Fatalf("active preparation hidden as %s", got)
	}
	for _, phase := range []string{"verifying", "uploading", "transferring"} {
		if got := uploadPresentationState(domain.UploadRecord{State: "pending"}, phase, true, "running", true, false); got != phase {
			t.Fatalf("phase %s became %s", phase, got)
		}
	}
	record := domain.UploadRecord{State: "pending", Intent: "pause", Error: "network error", Retries: 1}
	if got := uploadPresentationState(record, "uploading", false, "running", true, false); got != "paused" {
		t.Fatalf("paused retry became %s", got)
	}
	if got := uploadPresentationState(domain.UploadRecord{State: "pending"}, "failed", true, "running", true, false); got == "failed" {
		t.Fatal("manual retry retained stale failed state")
	}
}

func TestUploadRunningFilterIncludesAllActivePreparations(t *testing.T) {
	store := uploadMemoryStore{}
	service := &UploadService{store: store, mode: "running", manualUpload: true, active: map[string]bool{}}
	for index := 0; index < 5; index++ {
		key := fmt.Sprint(index)
		store[key] = domain.UploadRecord{Key: key, State: "pending"}
		service.active[key] = true
	}
	if err := service.refreshUploadView(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := service.Files(1, 20, "running").Total; got != 5 {
		t.Fatalf("active workers displayed: %d", got)
	}
}

func TestUploadClearFailedRetainsRetryBudget(t *testing.T) {
	store := uploadMemoryStore{"failed": {Key: "failed", State: "pending", Error: "failed", Retries: 3}}
	service := &UploadService{store: store}
	if err := service.applyListClear(context.Background(), "clear_failed", "files"); err != nil {
		t.Fatal(err)
	}
	if uploadRetryDue(store["failed"], time.Now()) {
		t.Fatal("cleared failure became runnable")
	}
}

func TestUploadDeleteCleanupHasFiniteRetryBudget(t *testing.T) {
	record := domain.UploadRecord{Intent: "delete", State: "discarding", Error: "remote unavailable", Retries: 3}
	if uploadRetryDue(record, time.Now()) {
		t.Fatal("delete cleanup retries forever")
	}
}

func TestUploadRetryAllRestartsVisibleLegacyDeleteFailures(t *testing.T) {
	store := uploadMemoryStore{"legacy": {Key: "legacy", State: "committing", Intent: "delete", Error: "115 错误 20018：文件不存在或已删除。", Retries: 3}}
	service := &UploadService{store: store, mode: "stopped"}
	if err := service.applyUploadControl(context.Background(), "retry_all"); err != nil {
		t.Fatal(err)
	}
	if record := store["legacy"]; record.Intent != "run" || record.Retries != 0 || record.Error != "" {
		t.Fatalf("explicit retry retained old delete intent: %+v", record)
	}
}
