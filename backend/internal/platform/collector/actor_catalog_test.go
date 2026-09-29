package collector

import (
	"strings"
	"testing"
)

func TestGfriendsPriorityAliasesAndVariants(t *testing.T) {
	raw := []byte(`{"Content":{"z-low":{"Alias.jpg":"AI-Fix-演员甲.jpg?t=1","演员甲-1.jpg":"演员甲-1.jpg"},"0-high":{"演员甲.jpg":"演员甲.jpg?t=2","Actor B.jpg":"演员乙.jpg"}}}`)
	items, err := parseGfriends(raw)
	if err != nil || len(items) != 2 {
		t.Fatalf("%+v %v", items, err)
	}
	for _, p := range items {
		if p.Name == "演员甲" {
			if !strings.Contains(p.Photo, "0-high/") || !strings.HasSuffix(p.Photo, "?t=2") || len(p.Aliases) != 1 || p.Aliases[0] != "Alias" {
				t.Fatal(p)
			}
		}
	}
	for _, raw := range []string{`{}`, `{"Content":{}}`, `{"Content":{"a":{"x.jpg":"../x.jpg"}}}`} {
		if _, err := parseGfriends([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestGfriendsTrimsActorNamesAndAliases(t *testing.T) {
	items, err := parseGfriends([]byte(`{"Content":{"x":{" alias.jpg":" 演员甲 .jpg"}}}`))
	if err != nil || len(items) != 1 || items[0].Name != "演员甲" || len(items[0].Aliases) != 1 || items[0].Aliases[0] != "alias" {
		t.Fatalf("%+v %v", items, err)
	}
}

func TestHotActorsOnlyMonthlySection(t *testing.T) {
	card := func(name string) string {
		return `<div class="actor-box"><a href="/actors/abc" title="` + name + `, Alias"><strong>` + name + `</strong><img class="avatar" src="https://example.org/a.jpg"></a></div>`
	}
	items, err := parseHotActors([]byte(`<h3>新人</h3><div>` + card("新人") + `</div><h3>月榜</h3><div>` + card("热门") + `</div><h3>全部</h3><div>` + card("普通") + `</div>`))
	if err != nil || len(items) != 1 || items[0].Name != "热门" || len(items[0].Aliases) != 1 {
		t.Fatalf("%+v %v", items, err)
	}
	if _, err = parseHotActors([]byte(`<h3>新人</h3><div>` + card("新人") + `</div>`)); err == nil {
		t.Fatal("missing ranking accepted")
	}
}
