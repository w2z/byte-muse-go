package database

import (
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestQueueLeaseRecoveryRejectsStaleWorker(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "lease.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	q := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	if _, e = q.CreateRun(ctx, ports.CollectionRequest{Source: "netflav", Kind: "search", Query: "test", Page: 1}); e != nil {
		t.Fatal(e)
	}
	old, e := q.Claim(ctx, "page", time.Now())
	if e != nil || old == nil {
		t.Fatal(e)
	}
	current, e := q.Claim(ctx, "page", time.Now().Add(3*time.Minute))
	if e != nil || current == nil {
		t.Fatal(e)
	}
	if e = q.SavePage(ctx, *old, ports.CollectionBatch{}); !errors.Is(e, ports.ErrCollectionLeaseLost) {
		t.Fatalf("stale worker committed: %v", e)
	}
	if e = q.SavePage(ctx, *current, ports.CollectionBatch{}); e != nil {
		t.Fatal(e)
	}
}

func TestQueueOlderRankCannotReplaceNewerRun(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "ordering.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	q := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	req := ports.CollectionRequest{Source: "javdb", Kind: "rank", Period: "daily", Page: 1}
	older, e := q.CreateRun(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	newer, e := q.CreateRun(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SQLDB().Exec(`UPDATE collection_runs SET created_ms=1 WHERE id=?`, older.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SQLDB().Exec(`UPDATE collection_runs SET created_ms=2 WHERE id=?`, newer.ID); e != nil {
		t.Fatal(e)
	}
	for _, code := range []string{"OLD-001", "NEW-001"} {
		w, e := q.Claim(ctx, "page", time.Now())
		if e != nil {
			t.Fatal(e)
		}
		if e = q.SavePage(ctx, *w, ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: code, Code: code, Title: code, URL: "https://javdb.com/v/" + code}}}); e != nil {
			t.Fatal(e)
		}
	}
	old, e := q.Claim(ctx, "video", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := q.Claim(ctx, "video", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = q.SaveVideo(ctx, *fresh, false); e != nil {
		t.Fatal(e)
	}
	if e = q.SaveVideo(ctx, *old, false); e != nil {
		t.Fatal(e)
	}
	var code string
	if e = s.SQLDB().QueryRow(`SELECT code FROM rank_entries WHERE rank_type='daily'`).Scan(&code); e != nil || code != "NEW-001" {
		t.Fatalf("stale publication: %s %v", code, e)
	}
}

func TestQueueTranslationNeverOverwritesExistingTitle(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "translation.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	q := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	run, e := q.CreateRun(ctx, ports.CollectionRequest{Source: "netflav", Kind: "search", Query: "test", Page: 1})
	if e != nil {
		t.Fatal(e)
	}
	w, e := q.Claim(ctx, "page", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = q.SavePage(ctx, *w, ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "a", Code: "TEST-001", Title: "测试影片", URL: "https://netflav.com/video?id=a"}}}); e != nil {
		t.Fatal(e)
	}
	w, e = q.Claim(ctx, "video", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = q.SaveVideo(ctx, *w, true); e != nil {
		t.Fatal(e)
	}
	w, e = q.Claim(ctx, "translation", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SQLDB().Exec(`UPDATE media SET translated_title='人工译文'`); e != nil {
		t.Fatal(e)
	}
	if e = q.SaveTranslation(ctx, *w, "机器译文"); e != nil {
		t.Fatal(e)
	}
	var value string
	if e = s.SQLDB().QueryRow(`SELECT translated_title FROM media`).Scan(&value); e != nil || value != "人工译文" {
		t.Fatalf("%s %v", value, e)
	}
	status, e := q.GetRun(ctx, run.ID)
	if e != nil || status.Status != "completed" {
		t.Fatalf("%+v %v", status, e)
	}
}

func TestQueuePersistsAndIsolatesVideoFailures(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "queue.db")
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: path})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	q := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	run, e := q.CreateRun(ctx, ports.CollectionRequest{Source: "netflav", Kind: "search", Query: "test", Page: 1})
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: path})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	q = NewCollectionRepository(s.SQLDB(), DialectSQLite)
	w, e := q.Claim(ctx, "page", time.Now())
	if e != nil || w == nil {
		t.Fatalf("%+v %v", w, e)
	}
	if x, e := q.Claim(ctx, "page", time.Now()); e != nil || x != nil {
		t.Fatalf("double claim %+v %v", x, e)
	}
	batch := ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "a", Code: "TEST-001", Title: "测试影片", URL: "https://netflav.com/video?id=a"}, {SourceID: "b", Code: "TEST-002", URL: "https://netflav.com/video?id=b"}}}
	if e = q.SavePage(ctx, *w, batch); e != nil {
		t.Fatal(e)
	}
	w, e = q.Claim(ctx, "video", time.Now())
	if e != nil || w == nil {
		t.Fatal(e)
	}
	if e = q.SaveVideo(ctx, *w, true); e != nil {
		t.Fatal(e)
	}
	w, e = q.Claim(ctx, "video", time.Now())
	if e != nil || w == nil {
		t.Fatal(e)
	}
	if e = q.SaveVideo(ctx, *w, true); e == nil {
		t.Fatal("bad video accepted")
	}
	var n int
	if e = s.SQLDB().QueryRow(`SELECT count(*) FROM media`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("good video rolled back: %d %v", n, e)
	}
	tr, e := q.Claim(ctx, "translation", time.Now())
	if e != nil || tr == nil {
		t.Fatalf("translation not registered: %+v %v", tr, e)
	}
	if e = q.SaveTranslation(ctx, *tr, "测试译文"); e != nil {
		t.Fatal(e)
	}
	got, e := q.GetRun(ctx, run.ID)
	if e != nil || got.Saved != 1 || got.Translated != 1 {
		t.Fatalf("%+v %v", got, e)
	}
}

func TestQueueMigrationFromThirteenPreservesSourceRecords(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "upgrade.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = ensureMigrationTable(ctx, s.SQLDB(), DialectSQLite); e != nil {
		t.Fatal(e)
	}
	for _, m := range MigrationPlan(DialectSQLite) {
		if m.Version <= 13 {
			if e = applyMigration(ctx, s.SQLDB(), DialectSQLite, m); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = s.SQLDB().Exec(`INSERT INTO collection_records(source,source_id,code,payload_json,collected_at) VALUES('netflav','old','TEST-001','{}','2026-09-28')`); e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.SQLDB().QueryRow(`SELECT count(*) FROM collection_records`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("source records changed: %d %v", n, e)
	}
	if e = s.SQLDB().QueryRow(`SELECT count(*) FROM collection_work`).Scan(&n); e != nil || n != 0 {
		t.Fatalf("historical data enqueued: %d %v", n, e)
	}
}
