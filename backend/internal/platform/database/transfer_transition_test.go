package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/ports"
)

// TestSaveTransferStatesReportsOnlyTerminalTransitions 验证只有 transfer_status 真正变化
// 且落到终态时才产生一次跃迁：下载器每次轮询都会重复上报同一状态，重复上报不能重复通知。
func TestSaveTransferStatesReportsOnlyTerminalTransitions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "transfers.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,poster_url,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", "m1", "SSIS-001", "原标题", "https://img.example/poster.jpg", "none", "absent", created, created); err != nil {
		t.Fatal(err)
	}
	// 旧版横幅图优先于海报图，通知封面与 application.MediaCover 使用同一套取值规则。
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO legacy_media_metadata (media_id,code,banner_url,legacy_status,legacy_mode) VALUES (?,?,?,?,?)", "m1", "SSIS-001", "https://img.example/banner.jpg", "SUBSCRIBE", "strict"); err != nil {
		t.Fatal(err)
	}
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO download_tasks (id,media_id,status,downloader,info_hash,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "d1", "m1", "submitted", "qbittorrent", hash, created, created); err != nil {
		t.Fatal(err)
	}
	repo := NewSubscriptionDownloadRepository(store.SQLDB(), DialectSQLite)
	observed := time.Now().UTC().Add(time.Minute)

	for _, step := range []struct {
		status string
		want   int
	}{{"downloading", 0}, {"completed", 1}, {"completed", 0}, {"failed", 1}, {"failed", 0}} {
		transitions, e := repo.SaveTransferStates(ctx, []ports.TransferState{{Hash: hash, Status: step.status, ObservedAt: observed}})
		if e != nil {
			t.Fatal(e)
		}
		if len(transitions) != step.want {
			t.Fatalf("状态 %s: 跃迁数 = %d, 期望 %d (%+v)", step.status, len(transitions), step.want, transitions)
		}
		if step.want != 1 {
			continue
		}
		if transitions[0].TaskID != "d1" || transitions[0].Code != "SSIS-001" || transitions[0].Title != "原标题" || transitions[0].Status != step.status || transitions[0].Cover != "https://img.example/banner.jpg" {
			t.Fatalf("跃迁内容 = %+v", transitions[0])
		}
	}
}

// TestSaveTransferStatesFallsBackToPosterCover 验证没有旧版横幅图时封面回退到海报图，
// 保证推送配图规则在 SQL 侧与 application.MediaCover 完全一致。
func TestSaveTransferStatesFallsBackToPosterCover(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "transfers-poster.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,poster_url,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", "m2", "SSIS-002", "原标题2", "https://img.example/poster.jpg", "none", "absent", created, created); err != nil {
		t.Fatal(err)
	}
	const hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO download_tasks (id,media_id,status,downloader,info_hash,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "d2", "m2", "submitted", "qbittorrent", hash, created, created); err != nil {
		t.Fatal(err)
	}
	repo := NewSubscriptionDownloadRepository(store.SQLDB(), DialectSQLite)
	transitions, e := repo.SaveTransferStates(ctx, []ports.TransferState{{Hash: hash, Status: "completed", ObservedAt: time.Now().UTC().Add(time.Minute)}})
	if e != nil {
		t.Fatal(e)
	}
	if len(transitions) != 1 || transitions[0].Cover != "https://img.example/poster.jpg" {
		t.Fatalf("回退海报封面 = %+v", transitions)
	}
}
