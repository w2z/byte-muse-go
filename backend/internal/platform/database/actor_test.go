package database

import (
	"context"
	"path/filepath"
	"testing"

	"bytemuse/backend/internal/ports"
)

func TestActorListSupportsHotRankAndKeywordFilter(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "actors.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []struct{ name, limit string }{{"未订阅热门", ""}, {"已订阅热门", "2026-12-31"}, {"其他演员", ""}} {
		_, err := store.SQLDB().ExecContext(ctx, `INSERT INTO actors (name, limit_date) VALUES (?, ?)`, actor.name, nullIfEmpty(actor.limit))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SQLDB().ExecContext(ctx, `INSERT INTO rank_entries (rank_type, position, code) VALUES (?, ?, ?), (?, ?, ?)`, "actors", 1, "未订阅热门", "actors", 2, "已订阅热门"); err != nil {
		t.Fatal(err)
	}
	items, total, err := NewActorRepository(store.SQLDB(), DialectSQLite).List(ctx, ports.ActorListQuery{Limit: 20, Subscription: "hot", Keywords: "热门"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 || items[0].Name != "未订阅热门" || items[1].Name != "已订阅热门" {
		t.Fatalf("hot actors = total %d items %#v", total, items)
	}
}
