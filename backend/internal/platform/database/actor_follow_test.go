package database

import (
	"context"
	"path/filepath"
	"testing"
)

// TestActorFollow 验证演员追新的截止日、VR、演员数上限、重复执行与退订隔离。
func TestActorFollow(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "actors.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ id, code, casts, date, library string }{
		{"a", "AAA-001", " 演员甲 ,演员甲", "2026-09-28", "unknown"},
		{"b", "BBB-002", "演员甲,演员乙,演员丙,演员丁", "2026-09-28", "unknown"},
		{"c", "CCC-003", "演员甲", "2026-09-27", "unknown"},
		{"d", "DDD-004", "演员甲", "2026-09-28", "unknown"},
		{"e", "VR-005", "演员甲", "2026-09-28", "unknown"},
		{"f", "FFF-006", "演员甲", "2026-09-28", "present"},
		{"g", "GGG-007", "演员乙", "2026-09-28", "unknown"},
	} {
		_, e = s.SQLDB().Exec("INSERT INTO media(id,code,title,release_date,subscription_status,library_status,created_at,updated_at) VALUES(?,?,?,?,'none',?,'2026-09-28T00:00:00Z','2026-09-28T00:00:00Z')", v.id, v.code, v.id, nullIfEmpty(v.date), v.library)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.SQLDB().Exec("INSERT INTO legacy_media_metadata(media_id,code,casts,legacy_status,legacy_mode) VALUES(?,?,?,'','')", v.id, v.code, v.casts)
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"演员甲", "演员乙"} {
		if _, e = s.SQLDB().Exec("INSERT INTO actors(name) VALUES(?)", name); e != nil {
			t.Fatal(e)
		}
	}
	r := NewActorRepository(s.SQLDB(), DialectSQLite)
	if _, e = r.SaveSubscription(ctx, "演员甲", "2026-09-27"); e != nil {
		t.Fatal(e)
	}
	names, e := r.ActiveNames(ctx)
	if e != nil || len(names) != 1 || names[0] != "演员甲" {
		t.Fatalf("active names=%v %v", names, e)
	}
	n, e := r.Follow(ctx, "演员甲", 3)
	if e != nil || n != 2 {
		t.Fatalf("follow=%d %v", n, e)
	}
	n, e = r.Follow(ctx, "", 3)
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
	n, e = r.Follow(ctx, "", 3)
	if e != nil || n != 0 {
		t.Fatalf("restored canceled media=%d %v", n, e)
	}
	// 放宽演员数上限后只订阅此前被上限挡下的影片，已处理影片不会恢复。
	n, e = r.Follow(ctx, "", -1)
	if e != nil || n != 1 {
		t.Fatalf("widened limit=%d %v", n, e)
	}
	var activeA int
	if e = s.SQLDB().QueryRow("SELECT COUNT(*) FROM subscriptions WHERE media_id='a' AND status='active'").Scan(&activeA); e != nil || activeA != 0 {
		t.Fatalf("restored canceled media: %d %v", activeA, e)
	}
	var matches, subs int
	if e = s.SQLDB().QueryRow("SELECT COUNT(*) FROM actor_subscription_matches").Scan(&matches); e != nil || matches != 4 {
		t.Fatalf("ledger=%d %v", matches, e)
	}
	if e = s.SQLDB().QueryRow("SELECT COUNT(*) FROM subscriptions WHERE status='active'").Scan(&subs); e != nil || subs != 2 {
		t.Fatalf("active subscriptions=%d %v", subs, e)
	}
	if _, e = r.CancelSubscription(ctx, "演员甲"); e != nil {
		t.Fatal(e)
	}
	if names, e = r.ActiveNames(ctx); e != nil || len(names) != 0 {
		t.Fatalf("active after cancel=%v %v", names, e)
	}
}

// TestActorMigrationUpgrade 不改历史影片、文本和状态，且重复升级保留用户规则与台账。
func TestActorMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "actor-upgrade.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = ensureMigrationTable(ctx, s.SQLDB(), DialectSQLite); e != nil {
		t.Fatal(e)
	}
	for _, m := range MigrationPlan(DialectSQLite) {
		if m.Version < 22 {
			if e = applyMigration(ctx, s.SQLDB(), DialectSQLite, m); e != nil {
				t.Fatal(e)
			}
		}
	}
	_, e = s.SQLDB().Exec("INSERT INTO media(id,code,title,release_date,subscription_status,library_status,created_at,updated_at) VALUES('old','OLD-1','历史','2026-09-28','none','unknown','2026-09-28T00:00:00Z','2026-09-28T00:00:00Z')")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SQLDB().Exec("INSERT INTO legacy_media_metadata(media_id,code,casts,legacy_status,legacy_mode) VALUES('old','OLD-1',' 演员甲 ','CANCEL','STRICT')")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SQLDB().Exec("INSERT INTO actors(name,limit_date) VALUES('演员甲','2026-09-27')")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	r := NewActorRepository(s.SQLDB(), DialectSQLite)
	n, e := r.Follow(ctx, "", 3)
	if e != nil || n != 1 {
		t.Fatalf("upgrade follow=%d %v", n, e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var casts, state string
	if e = s.SQLDB().QueryRow("SELECT casts,legacy_status FROM legacy_media_metadata WHERE media_id='old'").Scan(&casts, &state); e != nil || casts != " 演员甲 " || state != "CANCEL" {
		t.Fatalf("historical data changed: %q %q %v", casts, state, e)
	}
	var limit string
	if e = s.SQLDB().QueryRow("SELECT limit_date FROM actors WHERE name='演员甲'").Scan(&limit); e != nil || limit != "2026-09-27" {
		t.Fatalf("rule lost: %q %v", limit, e)
	}
	if n, e = r.Follow(ctx, "", 3); e != nil || n != 0 {
		t.Fatalf("ledger not idempotent=%d %v", n, e)
	}
}
