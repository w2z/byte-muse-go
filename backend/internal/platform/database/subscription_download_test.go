package database

import (
	"bytemuse/backend/internal/ports"
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionDownloadQueueDeduplicatesAndSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "subscriptions.db")
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: path})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "SSIS-001", "film", "active", "absent", now, now)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO legacy_media_metadata (media_id,code,banner_url,legacy_status,legacy_mode) VALUES (?,?,?,?,?)", "m1", "SSIS-001", "https://img.example/banner.jpg", "SUBSCRIBE", "strict")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SQLDB().ExecContext(ctx, "INSERT INTO subscriptions (id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?)", "sub1", "m1", "active", "strict", "{}", "test-key", "hash", now, now, 1)
	if e != nil {
		t.Fatal(e)
	}
	q := NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite)
	first, e := q.Enqueue(ctx, "sub1", ports.DownloadOriginUser)
	if e != nil {
		t.Fatal(e)
	}
	again, e := q.Enqueue(ctx, "sub1", ports.DownloadOriginUser)
	if e != nil {
		t.Fatal(e)
	}
	if first.ID != again.ID {
		t.Fatalf("duplicate tasks: %s %s", first.ID, again.ID)
	}
	if count, e := q.EnqueueActive(ctx); e != nil || count != 0 {
		t.Fatalf("existing queued task counted as new: count=%d err=%v", count, e)
	}
	_ = s.Close()
	s, e = Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: path})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	q = NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite)
	claimed, e := q.Claim(ctx, time.Now())
	if e != nil || claimed == nil || claimed.ID != first.ID {
		t.Fatalf("claim=%#v err=%v", claimed, e)
	}
	if claimed.Title != "film" || claimed.Cover != "https://img.example/banner.jpg" {
		t.Fatalf("claim 文案与封面 = %#v", claimed)
	}
	if claimed.Origin != ports.DownloadOriginUser {
		t.Fatalf("claim 发起方 = %q，期望 user", claimed.Origin)
	}
	if e = q.SetCandidate(ctx, *claimed, "Nyaa BT", "bt", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "0123456789abcdef0123456789abcdef01234567", "qbittorrent", true); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = s.SQLDB().QueryRowContext(ctx, "SELECT status FROM download_tasks WHERE id=?", first.ID).Scan(&status); e != nil || status != "unknown" {
		t.Fatalf("status=%s err=%v", status, e)
	}
	pending, e := q.ClaimPending(ctx, time.Now().Add(3*time.Minute))
	if e != nil || pending == nil || pending.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("pending=%#v err=%v", pending, e)
	}
	if pending.Code != "SSIS-001" || pending.Title != "film" || pending.Cover != "https://img.example/banner.jpg" {
		t.Fatalf("pending 文案与封面 = %#v", pending)
	}
	if e = q.FinishSubmission(ctx, *pending, true, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.SQLDB().QueryRowContext(ctx, "SELECT status FROM download_tasks WHERE id=?", first.ID).Scan(&status); e != nil || status != "submitted" {
		t.Fatalf("status=%s err=%v", status, e)
	}
}

// TestSubscriptionDownloadConcurrentEnqueue expects both callers to receive the same durable task.
func TestSubscriptionDownloadConcurrentEnqueue(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "concurrent.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = s.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "SSIS-001", "film", "active", "absent", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SQLDB().ExecContext(ctx, "INSERT INTO subscriptions (id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?)", "sub1", "m1", "active", "strict", "{}", "key", "hash", now, now, 1); err != nil {
		t.Fatal(err)
	}
	repo := NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite)
	const callers = 8
	ids := make([]string, callers)
	errors := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			task, e := repo.Enqueue(ctx, "sub1", ports.DownloadOriginUser)
			ids[index], errors[index] = task.ID, e
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range ids {
		if errors[i] != nil || ids[i] == "" || ids[i] != ids[0] {
			t.Fatalf("caller %d id=%s err=%v all=%v", i, ids[i], errors[i], ids)
		}
	}
}

func TestDownloadTransferSnapshotFiltersBeforePagination(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "transfer.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range []string{"m1", "m2"} {
		if _, err = s.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", id, id, id, "none", "absent", now, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ id, media, hash string }{{"d1", "m1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"d2", "m2", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}} {
		if _, err = s.SQLDB().ExecContext(ctx, "INSERT INTO download_tasks (id,media_id,status,info_hash,downloader,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", item.id, item.media, "submitted", item.hash, "qbittorrent", now, now); err != nil {
			t.Fatal(err)
		}
	}
	completed := time.Unix(1780000200, 0).UTC()
	added := time.Unix(1780000100, 0).UTC()
	repo := NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite)
	if _, err = repo.SaveTransferStates(ctx, []ports.TransferState{{Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: "paused"}, {Hash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: "completed", AddedAt: &added, CompletedAt: &completed}}); err != nil {
		t.Fatal(err)
	}
	page, err := s.Downloads().List(ctx, ports.DownloadListQuery{Limit: 1, TransferStatus: "completed", CompletedFrom: &completed})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "d2" || page.Items[0].CompletedAt == nil {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	beforeAdded := added.Add(-time.Second)
	afterAdded := added.Add(time.Second)
	page, err = s.Downloads().List(ctx, ports.DownloadListQuery{Limit: 1, AddedFrom: &beforeAdded, AddedTo: &afterAdded})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "d2" {
		t.Fatalf("added page=%+v err=%v", page, err)
	}
	page, err = s.Downloads().List(ctx, ports.DownloadListQuery{Limit: 1, AddedFrom: &afterAdded})
	if err != nil || page.Total != 0 {
		t.Fatalf("added upper bound page=%+v err=%v", page, err)
	}
	page, err = s.Downloads().List(ctx, ports.DownloadListQuery{Limit: 1, CompletedTo: &completed})
	if err != nil || page.Total != 0 {
		t.Fatalf("completed exclusive upper bound page=%+v err=%v", page, err)
	}
}

// TestDownloadFailedFilterIncludesPreTransferFailure covers search failures with no downloader snapshot.
func TestDownloadFailedFilterIncludesPreTransferFailure(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "failed-filter.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = s.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "TEST-1", "test", "none", "absent", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SQLDB().ExecContext(ctx, "INSERT INTO download_tasks (id,media_id,status,created_at,updated_at) VALUES (?,?,?,?,?)", "d1", "m1", "failed", now, now); err != nil {
		t.Fatal(err)
	}
	page, err := s.Downloads().List(ctx, ports.DownloadListQuery{Limit: 10, TransferStatus: "failed"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "d1" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
