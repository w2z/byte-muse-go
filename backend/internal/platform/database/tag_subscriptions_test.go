package database

import (
	"bytemuse/backend/internal/ports"
	"context"
	"path/filepath"
	"testing"
)

// TestTagSubscriptions 验证升级、精确匹配、追新日期、重复执行与退订隔离。
func TestTagSubscriptions(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "tags.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ id, genres, date, library string }{
		{"a", " 制服 ,制服,4K", "2026-09-28", "unknown"}, {"b", "制服誘惑", "2026-09-28", "unknown"},
		{"c", "制服", "2026-09-26", "unknown"}, {"d", "制服", "", "unknown"}, {"e", "制服", "2026-09-28", "present"},
	} {
		_, e = s.SQLDB().Exec("INSERT INTO media(id,code,title,release_date,subscription_status,library_status,created_at,updated_at) VALUES(?,?,?,?,'none',?,'2026-09-28T00:00:00Z','2026-09-28T00:00:00Z')", v.id, v.id, v.id, nullIfEmpty(v.date), v.library)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.SQLDB().Exec("INSERT INTO legacy_media_metadata(media_id,code,genres,legacy_status,legacy_mode) VALUES(?,?,?,'','')", v.id, v.id, v.genres)
		if e != nil {
			t.Fatal(e)
		}
	}
	r := NewTagRepository(s.SQLDB(), DialectSQLite)
	item, e := r.SaveSubscription(ctx, "制服", "2026-09-27")
	if e != nil {
		t.Fatal(e)
	}
	if item.Category != "服装" || item.LimitDate == nil {
		t.Fatalf("tag=%+v", item)
	}
	n, e := r.Follow(ctx, "制服")
	if e != nil || n != 1 {
		t.Fatalf("follow=%d %v", n, e)
	}
	n, e = r.Follow(ctx, "")
	if e != nil || n != 0 {
		t.Fatalf("repeat=%d %v", n, e)
	}
	var id string
	if e = s.SQLDB().QueryRow("SELECT id FROM subscriptions WHERE media_id='a'").Scan(&id); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Subscriptions().Cancel(ctx, id); e != nil {
		t.Fatal(e)
	}
	n, e = r.Follow(ctx, "")
	if e != nil || n != 0 {
		t.Fatalf("restored canceled media=%d %v", n, e)
	}
	// 另一个标签命中同一影片，也不能恢复手动取消的影片订阅。
	if _, e = r.SaveSubscription(ctx, "4K", "2026-09-27"); e != nil {
		t.Fatal(e)
	}
	n, e = r.Follow(ctx, "")
	if e != nil || n != 0 {
		t.Fatalf("other tag restored canceled media=%d %v", n, e)
	}
	if _, e = r.CancelSubscription(ctx, "4K"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.SaveSubscription(ctx, "制服", "2026-09-25"); e != nil {
		t.Fatal(e)
	}
	n, e = r.Follow(ctx, "制服")
	if e != nil || n != 1 {
		t.Fatalf("earlier date=%d %v", n, e)
	}
	if _, e = r.CancelSubscription(ctx, "制服"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.CancelSubscription(ctx, "制服"); e != nil {
		t.Fatal(e)
	}
	items, total, e := r.ListTags(ctx, ports.TagListQuery{Subscription: "active", Limit: 15})
	if e != nil || total != 0 || len(items) != 0 {
		t.Fatalf("active=%v %d %v", items, total, e)
	}
	var count int
	s.SQLDB().QueryRow("SELECT COUNT(*) FROM subscriptions").Scan(&count)
	if count != 1 {
		t.Fatalf("cancel removed media subscriptions: %d", count)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	results, e := r.SearchMedia(ctx, "制服", 15, 0)
	if e != nil || results.Total != 4 {
		t.Fatalf("exact search=%+v %v", results, e)
	}
}

// TestTagMigrationUpgrade 不改历史影片、文本和状态，且重复升级保留用户规则。
func TestTagMigrationUpgrade(t *testing.T) {
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
		if m.Version < 20 {
			if e = applyMigration(ctx, s.SQLDB(), DialectSQLite, m); e != nil {
				t.Fatal(e)
			}
		}
	}
	_, e = s.SQLDB().Exec("INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES('old','OLD-1','历史','none','unknown','2026-09-28T00:00:00Z','2026-09-28T00:00:00Z')")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SQLDB().Exec("INSERT INTO legacy_media_metadata(media_id,code,genres,legacy_status,legacy_mode) VALUES('old','OLD-1',' 制服 ,4K','CANCEL','STRICT')")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	r := NewTagRepository(s.SQLDB(), DialectSQLite)
	if _, e = r.SaveSubscription(ctx, "制服", "2026-09-28"); e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var genres, state string
	if e = s.SQLDB().QueryRow("SELECT genres,legacy_status FROM legacy_media_metadata WHERE media_id='old'").Scan(&genres, &state); e != nil || genres != " 制服 ,4K" || state != "CANCEL" {
		t.Fatalf("historical data changed: %q %q %v", genres, state, e)
	}
	var count int
	if e = s.SQLDB().QueryRow("SELECT COUNT(*) FROM subscriptions").Scan(&count); e != nil || count != 0 {
		t.Fatalf("migration subscribed media: %d %v", count, e)
	}
	items, total, e := r.ListTags(ctx, ports.TagListQuery{Subscription: "active", Category: "服装", Limit: 15})
	if e != nil || total != 1 || len(items) != 1 || items[0].LimitDate == nil || *items[0].LimitDate != "2026-09-28" {
		t.Fatalf("rule lost: %+v %v", items, e)
	}
}
