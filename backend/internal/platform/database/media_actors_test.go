package database

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMediaActorProjectionAndExactSearch(t *testing.T) {
	s := openCatalogQueryTestStore(t)
	defer s.Close()
	ctx := context.Background()
	for _, id := range []string{"one", "two", "similar", "empty"} {
		insertCatalogTestMedia(t, s, id, id, "演员甲出现在标题")
	}
	for _, q := range []string{
		`INSERT INTO actors(name) VALUES('演员甲'),('演员乙'),('冲突别名')`,
		`INSERT INTO actor_aliases(actor_name,alias) VALUES('演员甲','别名'),('演员甲','共用'),('演员乙','共用'),('演员甲','冲突别名')`,
		`INSERT INTO legacy_media_metadata(media_id,code,casts,legacy_status,legacy_mode) VALUES('one','one',' 演员甲,别名,未知,共用,冲突别名,演员甲 ','NONE','strict'),('two','two','别名','NONE','strict'),('similar','similar','演员甲乙','NONE','strict')`,
	} {
		if _, e := s.SQLDB().Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	r := NewCatalogQueryRepository(s.SQLDB(), DialectSQLite)
	p, e := r.Search(ctx, "one", 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(p.Items[0])
	var got map[string]json.RawMessage
	json.Unmarshal(b, &got)
	want := `[{"name":"演员甲","actor_name":"演员甲"},{"name":"别名","actor_name":"演员甲"},{"name":"未知","actor_name":null},{"name":"共用","actor_name":null},{"name":"冲突别名","actor_name":"冲突别名"}]`
	if string(got["actors"]) != want {
		t.Fatalf("actors=%s", got["actors"])
	}
	for _, name := range []string{"演员甲", "别名"} {
		first, err := r.SearchActor(ctx, name, 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		second, err := r.SearchActor(ctx, name, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if first.Total != 2 || second.Total != 2 || len(first.Items) != 1 || len(second.Items) != 1 || first.Items[0].ID == second.Items[0].ID {
			t.Fatalf("actor pages: %#v %#v", first, second)
		}
	}
	for _, name := range []string{"共用", "未知", "演员", ""} {
		p, err := r.SearchActor(ctx, name, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 0 {
			t.Fatalf("unexpected match %s: %#v", name, p)
		}
	}
	p, e = r.Search(ctx, "empty", 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	if p.Items[0].Actors == nil || len(p.Items[0].Actors) != 0 {
		t.Fatalf("empty actors=%#v", p.Items[0].Actors)
	}
}
