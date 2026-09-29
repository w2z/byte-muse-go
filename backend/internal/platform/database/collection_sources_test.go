package database

import (
	"bytemuse/backend/internal/ports"
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestAdditionalSourcePersistence 验证未启用来源不能绕过入口写入，已有来源无番号不造影片。
func TestAdditionalSourcePersistence(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "sources.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	q := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	for _, source := range []string{"javdb", "netflav", "javlibrary", "avbase", "jable", "supjav"} {
		req := ports.CollectionRequest{Source: source, Kind: "search", Query: "TEST", Page: 1}
		b := ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "one", Title: "样例", URL: "https://example.test/item", Tags: []string{"标签"}}}}
		if _, e = q.SaveCollection(ctx, req, b); e != nil {
			t.Fatalf("%s: %v", source, e)
		}
		if counts, e := q.SaveCollection(ctx, req, b); e != nil || counts.Existing != 1 || counts.MediaInserted != 0 {
			t.Fatalf("%s: %+v %v", source, counts, e)
		}
	}
	var n int
	if e = s.SQLDB().QueryRow(`SELECT COUNT(*) FROM media`).Scan(&n); e != nil || n != 0 {
		t.Fatalf("media=%d err=%v", n, e)
	}
	for _, source := range []string{"thisav", "avgle", "javbus"} {
		if _, e = q.SaveCollection(ctx, ports.CollectionRequest{Source: source}, ports.CollectionBatch{}); e == nil {
			t.Fatalf("excluded source accepted: %s", source)
		}
	}
}

// TestSourceRankNamespace 验证跨站榜单独立发布，不覆盖旧 JavDB 榜且不改变请求周期。
func TestSourceRankNamespace(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "ranks.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	q := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	for _, tc := range []struct{ source, period, key, code string }{{"javdb", "daily", "daily", "TEST-001"}, {"javdb", "weekly", "weekly", "TEST-002"}, {"javlibrary", "wanted", "javlibrary:wanted", "TEST-003"}} {
		req := ports.CollectionRequest{Source: tc.source, Kind: "rank", Period: tc.period, Page: 1}
		run, e := q.CreateRun(ctx, req)
		if e != nil {
			t.Fatal(e)
		}
		if run.Request.Period != tc.period {
			t.Fatal("request period changed")
		}
		w, e := q.Claim(ctx, "page", time.Now().Add(time.Second))
		if e != nil || w == nil {
			t.Fatal(e)
		}
		if e = q.SavePage(ctx, *w, ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: tc.code, Code: tc.code, Title: "样例", URL: "https://example.test/item"}}}); e != nil {
			t.Fatal(e)
		}
		v, e := q.Claim(ctx, "video", time.Now().Add(time.Second))
		if e != nil || v == nil {
			t.Fatal(e)
		}
		if e = q.SaveVideo(ctx, *v, false); e != nil {
			t.Fatal(e)
		}
		var code string
		if e = s.SQLDB().QueryRow(`SELECT code FROM rank_entries WHERE rank_type=?`, tc.key).Scan(&code); e != nil || code != tc.code {
			t.Fatalf("rank %s code=%s err=%v", tc.key, code, e)
		}
	}
	var n int
	s.SQLDB().QueryRow(`SELECT COUNT(*) FROM rank_entries`).Scan(&n)
	if n != 3 {
		t.Fatal(n)
	}
}
