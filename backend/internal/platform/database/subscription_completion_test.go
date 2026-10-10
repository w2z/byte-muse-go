package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/ports"
)

// TestSubscriptionCompletedMediaNeverSearches 覆盖旧订阅下载成功与无来源的已入库影片，禁止未核实就重新下载。
func TestSubscriptionCompletedMediaNeverSearches(t *testing.T) {
	for _, sample := range []struct{ name, library, status, transfer string }{
		{"download_completed", "unknown", "completed", ""},
		{"transfer_completed", "unknown", "submitted", "completed"},
		{"library_unverifiable", "present", "", ""},
	} {
		t.Run(sample.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "completion.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			if _, err = s.SQLDB().Exec(`INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES('m','TEST-001','test','none',?,?,?)`, sample.library, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			if sample.status != "" {
				if _, err = s.SQLDB().Exec(`INSERT INTO download_tasks(id,media_id,status,transfer_status,created_at,updated_at) VALUES('old','m',?,?,?,?)`, sample.status, sample.transfer, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err = s.Subscriptions().Create(ctx, ports.CreateSubscription{MediaID: "m", IdempotencyKey: "new-subscription", Mode: "strict"})
			if err != nil {
				t.Fatal(err)
			}
			search, client := &flowSearcher{}, &flowDownloader{}
			service := application.NewSubscriptionDownloadService(NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite), search, map[string]application.MagnetDownloader{"qbittorrent": client}, func(context.Context) (map[string]string, error) { return map[string]string{}, nil })
			for range 2 {
				if _, err = service.RunActiveScans(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if search.called != 0 || client.submitted != 0 {
				t.Fatalf("completed media searched=%d submitted=%d", search.called, client.submitted)
			}
		})
	}
}

// TestSubscriptionLibraryCompletionSurvivesRestart 检查存在与缺失分支，完成后即使文件移除也不再执行本订阅。
func TestSubscriptionLibraryCompletionSurvivesRestart(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			dbPath := filepath.Join(root, "library.db")
			file := filepath.Join(root, "TEST-001.mp4")
			if present {
				if err := os.WriteFile(file, []byte("video"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: dbPath})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = s.MediaLibrary().MarkLibraryPresent(ctx, []ports.LibraryMediaItem{{Code: "TEST-001", Source: &ports.LibrarySource{Kind: "local", Location: file}}}); err != nil {
				t.Fatal(err)
			}
			var mediaID string
			if err = s.SQLDB().QueryRow("SELECT id FROM media WHERE code='TEST-001'").Scan(&mediaID); err != nil {
				t.Fatal(err)
			}
			sub, _, err := s.Subscriptions().Create(ctx, ports.CreateSubscription{MediaID: mediaID, IdempotencyKey: "library-test", Mode: "strict"})
			if err != nil {
				t.Fatal(err)
			}
			search, client := &flowSearcher{}, &flowDownloader{}
			run := func() {
				service := application.NewSubscriptionDownloadService(NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite), search, map[string]application.MagnetDownloader{"qbittorrent": client}, func(context.Context) (map[string]string, error) { return map[string]string{}, nil })
				service.SetLibraryPresenceChecker(application.NewLibraryPresenceService(nil, nil, map[string]string{}, nil))
				if _, err = service.RunActiveScans(ctx); err != nil {
					t.Fatal(err)
				}
			}
			run()
			if present {
				if err = os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: dbPath})
				if err != nil {
					t.Fatal(err)
				}
				run()
				if search.called != 0 || client.submitted != 0 {
					t.Fatal("completed subscription ran again")
				}
				if _, err = NewSubscriptionDownloadRepository(s.SQLDB(), DialectSQLite).EnqueueScan(ctx, sub.ID, ports.DownloadOriginUser); !errors.Is(err, ports.ErrSubscriptionSatisfied) {
					t.Fatalf("manual enqueue=%v", err)
				}
			} else if search.called != 1 || client.submitted != 1 {
				t.Fatalf("missing file search=%d submit=%d", search.called, client.submitted)
			}
		})
	}
}

// TestSubscriptionPresenceMigration 验证空库、42 升级、重复执行与历史数据不回填。
func TestSubscriptionPresenceMigration(t *testing.T) {
	for _, upgrade := range []bool{true, false} {
		t.Run(fmt.Sprint(upgrade), func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "migration.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if upgrade {
				if err = ensureMigrationTable(ctx, s.SQLDB(), DialectSQLite); err != nil {
					t.Fatal(err)
				}
				for _, m := range MigrationPlan(DialectSQLite) {
					if m.Version < 43 {
						if err = applyMigration(ctx, s.SQLDB(), DialectSQLite, m); err != nil {
							t.Fatal(err)
						}
					}
				}
				if _, err = s.SQLDB().Exec("INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES('history','TEST-OLD','old','none','present','2026-01-01','2026-01-01')"); err != nil {
					t.Fatal(err)
				}
			}
			// 模拟仅第一条 DDL 已成功、版本尚未登记后的重跑。
			if upgrade {
				if _, err = s.SQLDB().Exec(subscriptionPresenceMigration(DialectSQLite).Statements[0]); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err = s.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			for _, table := range []string{"media_library_sources", "subscription_completions"} {
				var n int
				if err = s.SQLDB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("%s count=%d err=%v", table, n, err)
				}
			}
			if upgrade {
				var status string
				if err = s.SQLDB().QueryRow("SELECT library_status FROM media WHERE id='history'").Scan(&status); err != nil || status != "present" {
					t.Fatalf("history=%s err=%v", status, err)
				}
			}
		})
	}
}

// TestSubscriptionActiveDownloaderPresence 覆盖下载器存在、明确缺失与连接失败，同影片旧订阅任务也参与去重。
func TestSubscriptionActiveDownloaderPresence(t *testing.T) {
	for _, mode := range []string{"present", "missing", "offline", "completed", "control_completed", "observed_completed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "transfer.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			stamp := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
			if _, err = store.SQLDB().Exec("INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES('m','TEST-001','test','none','unknown',?,?)", stamp, stamp); err != nil {
				t.Fatal(err)
			}
			sub, _, err := store.Subscriptions().Create(ctx, ports.CreateSubscription{MediaID: "m", IdempotencyKey: "sub", Mode: "strict"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SQLDB().Exec("INSERT INTO download_tasks(id,media_id,subscription_id,status,info_hash,downloader,created_at,updated_at) VALUES('old','m','old-sub','submitted','hash','qbittorrent',?,?)", stamp, stamp); err != nil {
				t.Fatal(err)
			}
			repo := NewSubscriptionDownloadRepository(store.SQLDB(), DialectSQLite)
			client := &presenceDownloader{exists: mode != "missing", offline: mode == "offline", completed: mode == "observed_completed"}
			search := &flowSearcher{}
			service := application.NewSubscriptionDownloadService(repo, search, map[string]application.MagnetDownloader{"qbittorrent": client}, func(context.Context) (map[string]string, error) { return map[string]string{}, nil })
			if mode == "completed" {
				if _, err = repo.SaveTransferStates(ctx, []ports.TransferState{{Hash: "hash", Status: "completed"}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "control_completed" {
				_, token, e := repo.LockControl(ctx, "old")
				if e != nil {
					t.Fatal(e)
				}
				if err = repo.FinishControl(ctx, "old", token, "resume", &ports.TransferState{Status: "completed"}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "completed" || mode == "control_completed" {
				if _, err = store.SQLDB().Exec("DELETE FROM download_tasks WHERE id='old'"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = service.RunActiveScans(ctx); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "missing" {
				want = 1
			}
			if search.called != want || client.submits != want {
				t.Fatalf("search=%d submit=%d want=%d", search.called, client.submits, want)
			}
			if mode == "observed_completed" {
				if _, err = store.SQLDB().Exec("DELETE FROM download_tasks WHERE id='old'"); err != nil {
					t.Fatal(err)
				}
				if _, err = service.RunActiveScans(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "completed" || mode == "control_completed" || mode == "observed_completed" {
				if _, err = service.Enqueue(ctx, sub.ID, ports.DownloadOriginUser); !errors.Is(err, ports.ErrSubscriptionSatisfied) {
					t.Fatalf("enqueue=%v", err)
				}
			} else if client.checks == 0 {
				t.Fatal("download client was not checked")
			}
		})
	}
}

type presenceDownloader struct {
	exists, offline, completed bool
	checks, submits            int
}

func (d *presenceDownloader) HasHash(context.Context, string) (bool, error) {
	d.checks++
	if d.offline {
		return false, errors.New("offline")
	}
	return d.exists, nil
}
func (d *presenceDownloader) Submit(context.Context, string) error {
	d.submits++
	d.exists = true
	return nil
}

func (d *presenceDownloader) Observe(ctx context.Context, hash string) (*ports.TransferState, error) {
	exists, err := d.HasHash(ctx, hash)
	if err != nil || !exists {
		return nil, err
	}
	status := "downloading"
	if d.completed {
		status = "completed"
	}
	return &ports.TransferState{Hash: hash, Status: status}, nil
}
