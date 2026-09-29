package database

import (
	"bytemuse/backend/internal/ports"
	"context"
	"path/filepath"
	"testing"
)

func TestActorCatalogImportAndHotIsolation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "actors.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewActorCatalogRepository(store.SQLDB(), DialectSQLite)
	hot := ports.ActorProfile{Name: "热门", Photo: "https://example.org/h.jpg"}
	catalog := ports.ActorProfile{Name: "目录演员", Photo: "https://example.org/c.jpg", Aliases: []string{"Alias"}}
	for _, p := range []ports.ActorProfile{hot, catalog} {
		if added, e := repo.SaveActorProfile(ctx, p); e != nil || !added {
			t.Fatalf("%v %v", added, e)
		}
	}
	if err = repo.PublishHotActors(ctx, []ports.ActorProfile{hot}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SQLDB().ExecContext(ctx, "UPDATE actors SET limit_date='2026-09-01' WHERE name='目录演员'"); err != nil {
		t.Fatal(err)
	}
	catalog.Photo = "https://example.org/replacement.jpg"
	if added, e := repo.SaveActorProfile(ctx, catalog); e != nil || added {
		t.Fatalf("duplicate %v %v", added, e)
	}
	list := NewActorRepository(store.SQLDB(), DialectSQLite)
	items, total, e := list.List(ctx, ports.ActorListQuery{Limit: 15, Subscription: "all", Keywords: "Alias"})
	if e != nil || total != 1 || items[0].Photo == nil || *items[0].Photo != "https://example.org/c.jpg" || items[0].LimitDate == nil {
		t.Fatalf("%+v %d %v", items, total, e)
	}
	items, total, e = list.List(ctx, ports.ActorListQuery{Limit: 15, Subscription: "hot"})
	if e != nil || total != 1 || items[0].Name != "热门" {
		t.Fatalf("%+v %d %v", items, total, e)
	}
	if e = repo.PublishHotActors(ctx, nil); e == nil {
		t.Fatal("empty rank accepted")
	}
	if e = repo.PublishHotActors(ctx, []ports.ActorProfile{catalog}); e != nil {
		t.Fatal(e)
	}
	items, total, e = list.List(ctx, ports.ActorListQuery{Limit: 15, Subscription: "hot", Keywords: "Alias"})
	if e != nil || total != 1 || items[0].Name != "目录演员" {
		t.Fatalf("%+v %d %v", items, total, e)
	}
	_, total, e = list.List(ctx, ports.ActorListQuery{Limit: 15, Subscription: "all"})
	if e != nil || total != 2 {
		t.Fatalf("all %d %v", total, e)
	}
}
