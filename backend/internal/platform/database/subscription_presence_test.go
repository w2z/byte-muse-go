package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/ports"
)

// newPresenceTestStore 建立带一条影片与可复用任务行的隔离库，供下载器状态一致性测试使用。
func newPresenceTestStore(t *testing.T) Store {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "presence.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,poster_url,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", "m1", "SSIS-001", "原标题", "https://img.example/poster.jpg", "none", "absent", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return store
}

// TestMarkTransferMissingClearsTransferStatus 验证下载器里已不存在的任务在置为 failed 时
// 同时清空 transfer_status：failed 任务不再参与 SaveTransferStates，残留值会永久显示为下载中。
func TestMarkTransferMissingClearsTransferStatus(t *testing.T) {
	ctx := context.Background()
	store := newPresenceTestStore(t)
	stamp := time.Now().UTC()
	if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO download_tasks (id,media_id,status,downloader,info_hash,transfer_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", "d1", "m1", "submitted", "qbittorrent", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "downloading", encodeTime(stamp, DialectSQLite), encodeTime(stamp, DialectSQLite)); err != nil {
		t.Fatal(err)
	}
	repo := NewSubscriptionDownloadRepository(store.SQLDB(), DialectSQLite)
	if err := repo.MarkTransferMissing(ctx, activeTransferRow(t, store, "d1")); err != nil {
		t.Fatal(err)
	}
	var status string
	var transfer any
	if err := store.SQLDB().QueryRowContext(ctx, "SELECT status, transfer_status FROM download_tasks WHERE id=?", "d1").Scan(&status, &transfer); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("status = %q, 期望 failed", status)
	}
	if transfer != nil {
		t.Fatalf("transfer_status = %v, 期望 NULL", transfer)
	}
}

// TestStaleFailedTransferStatusMigrationClearsOnlyFailedRows 验证清理迁移只清失败任务的残留状态，
// 不改文件状态与其它任务，并且重复执行无副作用。
func TestStaleFailedTransferStatusMigrationClearsOnlyFailedRows(t *testing.T) {
	ctx := context.Background()
	store := newPresenceTestStore(t)
	stamp := encodeTime(time.Now().UTC(), DialectSQLite)
	rows := []struct{ id, status, transfer string }{
		{"failed-stale", "failed", "downloading"},
		{"failed-terminal", "failed", "failed"},
		{"submitted-live", "submitted", "downloading"},
	}
	for _, row := range rows {
		if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO download_tasks (id,media_id,status,downloader,info_hash,transfer_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", row.id, "m1", row.status, "qbittorrent", "", row.transfer, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	statements := staleFailedTransferStatusMigration(DialectSQLite).Statements
	for _, run := range []int{1, 2} {
		for _, statement := range statements {
			if _, err := store.SQLDB().ExecContext(ctx, statement); err != nil {
				t.Fatalf("第 %d 次执行失败: %v", run, err)
			}
		}
	}
	want := map[string]any{"failed-stale": nil, "submitted-live": "downloading"}
	for id, expected := range want {
		var transfer any
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT transfer_status FROM download_tasks WHERE id=?", id).Scan(&transfer); err != nil {
			t.Fatal(err)
		}
		if transfer != expected {
			t.Fatalf("%s 的 transfer_status = %v, 期望 %v", id, transfer, expected)
		}
	}
	var terminal any
	if err := store.SQLDB().QueryRowContext(ctx, "SELECT transfer_status FROM download_tasks WHERE id=?", "failed-terminal").Scan(&terminal); err != nil {
		t.Fatal(err)
	}
	if terminal != "failed" {
		t.Fatalf("failed-terminal 的 transfer_status = %v, 期望 failed", terminal)
	}
}

// activeTransferRow 读取任务当前 updated_at 快照，构造与线上一致的条件更新入参。
// MarkTransferMissing 只在 updated_at 未变化时生效，因此入参必须来自库内实际值。
func activeTransferRow(t *testing.T, store Store, id string) ports.ActiveTransfer {
	t.Helper()
	var stamp any
	if err := store.SQLDB().QueryRowContext(context.Background(), "SELECT updated_at FROM download_tasks WHERE id=?", id).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	updated, err := valueToTime(stamp)
	if err != nil {
		t.Fatal(err)
	}
	return ports.ActiveTransfer{ID: id, UpdatedAt: updated}
}
