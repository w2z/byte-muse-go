package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

func TestCancelSubscriptionDeletesRecordAndResetsMediaStatus(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "cancel.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"media-1", "ABC-001", "测试影片", domain.SubscriptionStatusNone, domain.LibraryStatusAbsent, now, now); err != nil {
		t.Fatal(err)
	}
	repository := store.Subscriptions()
	created, _, err := repository.Create(ctx, ports.CreateSubscription{
		IdempotencyKey: "cancel-test-key",
		MediaID:        "media-1",
		Mode:           domain.SubscriptionModeStrict,
		Filter:         map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Cancel(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	var subscriptionCount int
	if err := store.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM subscriptions WHERE id = ?`, created.ID).Scan(&subscriptionCount); err != nil {
		t.Fatal(err)
	}
	if subscriptionCount != 0 {
		t.Fatalf("cancelled subscription remains in database: %d", subscriptionCount)
	}
	var mediaStatus string
	if err := store.SQLDB().QueryRowContext(ctx, `SELECT subscription_status FROM media WHERE id = ?`, created.MediaID).Scan(&mediaStatus); err != nil {
		t.Fatal(err)
	}
	if mediaStatus != string(domain.SubscriptionStatusNone) {
		t.Fatalf("media subscription status = %q, want %q", mediaStatus, domain.SubscriptionStatusNone)
	}
	if _, _, err := repository.Cancel(ctx, created.ID); !errors.Is(err, ports.ErrSubscriptionNotFound) {
		t.Fatalf("second cancel error = %v, want ErrSubscriptionNotFound", err)
	}
}

func TestCanceledSubscriptionIsAbsentFromList(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "list.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"media-2", "ABC-002", "测试影片 2", domain.SubscriptionStatusNone, domain.LibraryStatusAbsent, now, now); err != nil {
		t.Fatal(err)
	}
	repository := store.Subscriptions()
	created, _, err := repository.Create(ctx, ports.CreateSubscription{
		IdempotencyKey: "cancel-list-test-key",
		MediaID:        "media-2",
		Mode:           domain.SubscriptionModeStrict,
		Filter:         map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Cancel(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	page, err := repository.List(ctx, ports.SubscriptionListQuery{Limit: 50, Status: domain.SubscriptionStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("active subscription list = total %d, items %d; want empty", page.Total, len(page.Items))
	}
}

func TestCancelSubscriptionDeletesPendingDownloadTasksOnly(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "pending.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"media-3", "ABC-003", "测试影片 3", domain.SubscriptionStatusNone, domain.LibraryStatusAbsent, now, now); err != nil {
		t.Fatal(err)
	}
	repository := store.Subscriptions()
	created, _, err := repository.Create(ctx, ports.CreateSubscription{IdempotencyKey: "cancel-task-test-key", MediaID: "media-3", Mode: domain.SubscriptionModeStrict, Filter: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []struct {
		id     string
		status domain.DownloadStatus
	}{{"queued-task", domain.DownloadStatusQueued}, {"searching-task", domain.DownloadStatusSearching}, {"submitted-task", domain.DownloadStatusSubmitted}} {
		if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO download_tasks (id, media_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, task.id, created.MediaID, task.status, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := repository.Cancel(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := store.SQLDB().QueryContext(ctx, `SELECT id, status FROM download_tasks WHERE media_id = ? ORDER BY id`, created.MediaID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id+":"+status)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "submitted-task:submitted" {
		t.Fatalf("remaining tasks = %v, want submitted task only", ids)
	}
}

func TestCreateSubscriptionRejectsDuplicateActiveSubscription(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "duplicate.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"media-duplicate", "ABC-DUP", "重复测试影片", domain.SubscriptionStatusNone, domain.LibraryStatusAbsent, now, now); err != nil {
		t.Fatal(err)
	}
	repository := store.Subscriptions()
	first, created, err := repository.Create(ctx, ports.CreateSubscription{IdempotencyKey: "duplicate-first-key", MediaID: "media-duplicate", Mode: domain.SubscriptionModeStrict, Filter: map[string]any{}})
	if err != nil || !created {
		t.Fatalf("first create = (%v, %v), want created", first.ID, err)
	}
	if _, _, err := repository.Create(ctx, ports.CreateSubscription{IdempotencyKey: "duplicate-second-key", MediaID: "media-duplicate", Mode: domain.SubscriptionModePreload, Filter: map[string]any{"only_free": true}}); !errors.Is(err, ports.ErrActiveSubscriptionExists) {
		t.Fatalf("duplicate create error = %v, want ErrActiveSubscriptionExists", err)
	}
	var count int
	if err := store.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM subscriptions WHERE media_id = ? AND status = ?`, "media-duplicate", domain.SubscriptionStatusActive).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("active subscription count = %d, want 1", count)
	}
}

func TestSubscriptionListDefaultsToActiveOnly(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "active-list.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, "media-legacy-canceled", "ABC-CANCELED", "历史取消订阅", domain.SubscriptionStatusNone, domain.LibraryStatusAbsent, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO subscriptions (id, media_id, status, mode, filter_json, idempotency_key, idempotency_hash, created_at, updated_at, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-canceled", "media-legacy-canceled", domain.SubscriptionStatusCanceled, domain.SubscriptionModeStrict, "{}", "legacy-canceled-key", "legacy-canceled-hash", stamp, stamp, 1); err != nil {
		t.Fatal(err)
	}
	page, err := store.Subscriptions().List(ctx, ports.SubscriptionListQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("default subscription list = total %d, items %d; want active rows only", page.Total, len(page.Items))
	}
}

func TestMediaProjectionUsesSubscriptionRowAsAuthoritativeStatus(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "projection-status.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO media (id, code, title, subscription_status, library_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, "media-status-mismatch", "OFJE-670", "状态不一致影片", domain.SubscriptionStatusNone, domain.LibraryStatusAbsent, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO subscriptions (id, media_id, status, mode, filter_json, idempotency_key, idempotency_hash, created_at, updated_at, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "status-mismatch-sub", "media-status-mismatch", domain.SubscriptionStatusActive, domain.SubscriptionModeStrict, "{}", "status-mismatch-key", "status-mismatch-hash", stamp, stamp, 1); err != nil {
		t.Fatal(err)
	}
	item, err := store.Media().Get(ctx, "media-status-mismatch")
	if err != nil {
		t.Fatal(err)
	}
	if item.SubscriptionStatus != domain.SubscriptionStatusActive || item.DisplayStatus != domain.MediaDisplayStatusSubscribed {
		t.Fatalf("projection status = %q/display %q, want active/subscribed", item.SubscriptionStatus, item.DisplayStatus)
	}
}
