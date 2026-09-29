package database

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/ports"
)

// fakeTagResolver 模拟字典与翻译结果，用于验证入库路径确实使用统一后的权威名。
type fakeTagResolver struct{ mapping map[string]string }

func (f fakeTagResolver) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := map[string]string{}
	for _, name := range names {
		if value, ok := f.mapping[name]; ok {
			resolved[name] = value
			continue
		}
		resolved[name] = name
	}
	return resolved, nil
}

// TestCollectionAppliesTagResolver 验证同步与队列两条入库路径都写入权威标签名，且来源快照保持原样。
func TestCollectionAppliesTagResolver(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "tag-resolver.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	r := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	r.SetTagResolver(fakeTagResolver{mapping: map[string]string{"Big Tits": "巨乳", "出軌": "出轨"}})
	req := ports.CollectionRequest{Source: "netflav", Kind: "search", Page: 1}
	item := ports.CollectedMedia{SourceID: "tags", Code: "TEST-001", Title: "来源标题", URL: "https://netflav.com/video?id=tags", Tags: []string{"Big Tits", " 巨乳 ", "出軌"}}
	if _, e = r.SaveCollection(ctx, req, ports.CollectionBatch{Items: []ports.CollectedMedia{item}}); e != nil {
		t.Fatal(e)
	}
	var genres string
	if e = s.SQLDB().QueryRow("SELECT genres FROM legacy_media_metadata").Scan(&genres); e != nil || genres != "巨乳,出轨" {
		t.Fatalf("genres=%q err=%v", genres, e)
	}
	var payload string
	if e = s.SQLDB().QueryRow("SELECT payload_json FROM collection_records").Scan(&payload); e != nil {
		t.Fatal(e)
	}
	var saved ports.CollectedMedia
	if e = json.Unmarshal([]byte(payload), &saved); e != nil || len(saved.Tags) != 3 || saved.Tags[0] != "Big Tits" {
		t.Fatalf("snapshot=%v err=%v", saved.Tags, e)
	}
	item.Tags = []string{"出軌", "巨乳", "Amateur"}
	if _, e = r.SaveCollection(ctx, req, ports.CollectionBatch{Items: []ports.CollectedMedia{item}}); e != nil {
		t.Fatal(e)
	}
	if e = s.SQLDB().QueryRow("SELECT genres FROM legacy_media_metadata").Scan(&genres); e != nil || genres != "巨乳,出轨,Amateur" {
		t.Fatalf("merged=%q err=%v", genres, e)
	}
	if _, e = r.CreateRun(ctx, req); e != nil {
		t.Fatal(e)
	}
	page, e := r.Claim(ctx, "page", time.Now())
	if e != nil || page == nil {
		t.Fatalf("page=%v err=%v", page, e)
	}
	if e = r.SavePage(ctx, *page, ports.CollectionBatch{Items: []ports.CollectedMedia{item}}); e != nil {
		t.Fatal(e)
	}
	work, e := r.Claim(ctx, "video", time.Now())
	if e != nil || work == nil {
		t.Fatalf("video=%v err=%v", work, e)
	}
	if e = r.SaveVideo(ctx, *work, false); e != nil {
		t.Fatal(e)
	}
	if e = s.SQLDB().QueryRow("SELECT genres FROM legacy_media_metadata").Scan(&genres); e != nil || genres != "巨乳,出轨,Amateur" {
		t.Fatalf("queue genres=%q err=%v", genres, e)
	}
}

// TestNormalizeStoredTagsKeepsCuratedCategory 验证归一阶段自动补齐的「未分类」行不会覆盖对标站字典的人工分类。
// 解析阶段先登记权威名是 TagNameService 的真实行为，这里按同样顺序模拟，避免归一后分类退化成未分类。
func TestNormalizeStoredTagsKeepsCuratedCategory(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "tag-category.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SQLDB().Exec("INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES('m','m','m','none','unknown','2026-09-01','2026-09-01')"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SQLDB().Exec("INSERT INTO legacy_media_metadata(media_id,code,genres,legacy_status,legacy_mode) VALUES('m','m','已婚婦女,已婚妇女,Married Woman','','')"); e != nil {
		t.Fatal(e)
	}
	translate := map[string]string{"已婚婦女": "已婚妇女", "已婚妇女": "已婚妇女", "Married Woman": "已婚妇女"}
	resolve := func(_ context.Context, names []string) (map[string]string, error) {
		mapping := map[string]string{}
		for _, name := range names {
			final := name
			if value, ok := translate[name]; ok {
				final = value
			}
			if _, e := s.SQLDB().Exec("INSERT INTO tag_catalog(name,category) VALUES(?,'未分类') ON CONFLICT(name) DO NOTHING", final); e != nil {
				return nil, e
			}
			mapping[name] = final
		}
		return mapping, nil
	}
	result, e := NormalizeStoredTags(ctx, s, DialectSQLite, resolve)
	if e != nil {
		t.Fatal(e)
	}
	var genres, category string
	if e = s.SQLDB().QueryRow("SELECT genres FROM legacy_media_metadata WHERE media_id='m'").Scan(&genres); e != nil || genres != "已婚妇女" {
		t.Fatalf("genres=%q err=%v", genres, e)
	}
	if e = s.SQLDB().QueryRow("SELECT category FROM tag_catalog WHERE name='已婚妇女'").Scan(&category); e != nil || category != "角色" {
		t.Fatalf("category=%q err=%v", category, e)
	}
	var legacy int
	if e = s.SQLDB().QueryRow("SELECT COUNT(*) FROM tag_catalog WHERE name='已婚婦女'").Scan(&legacy); e != nil || legacy != 0 {
		t.Fatalf("繁体字典行未删除: %d err=%v", legacy, e)
	}
	if result.CatalogAdded != 1 || result.CatalogMerged != 1 || result.GenreRows != 1 {
		t.Fatalf("result=%+v", result)
	}
}

// TestNormalizeStoredTags 验证字典、影片标签、追新规则与台账按同一映射归一，重复执行不再改写。
func TestNormalizeStoredTags(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "tag-names.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	for _, row := range []struct{ id, genres string }{
		{"a", "出軌, Big Tits ,出軌"},
		{"b", "Amateur,巨乳"},
	} {
		if _, e = s.SQLDB().Exec("INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES(?,?,?,'none','unknown','2026-09-01','2026-09-01')", row.id, row.id, row.id); e != nil {
			t.Fatal(e)
		}
		if _, e = s.SQLDB().Exec("INSERT INTO legacy_media_metadata(media_id,code,genres,legacy_status,legacy_mode) VALUES(?,?,?,'','')", row.id, row.id, row.genres); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.SQLDB().Exec("INSERT INTO tag_subscriptions(name,limit_date,updated_at) VALUES('出軌','2026-09-01','2026-09-01T00:00:00Z')"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SQLDB().Exec("INSERT INTO tag_subscription_matches(tag_name,media_id,processed_at) VALUES('出軌','a','2026-09-02T00:00:00Z')"); e != nil {
		t.Fatal(e)
	}
	resolve := func(_ context.Context, names []string) (map[string]string, error) {
		mapping := map[string]string{
			"出軌": "出轨", "業餘": "业余", "Big Tits": "巨乳", "Amateur": "业余",
		}
		for _, name := range names {
			if _, ok := mapping[name]; !ok {
				mapping[name] = name
			}
		}
		return mapping, nil
	}
	result, e := NormalizeStoredTags(ctx, s, DialectSQLite, resolve)
	if e != nil {
		t.Fatal(e)
	}
	// 字典预置了繁体「出軌」「業餘」，归一后应保留其分类而不是落到未分类。
	if result.GenreRows != 2 || result.Rules != 1 || result.Matches != 1 || result.CatalogMerged != 2 || result.CatalogAdded != 2 {
		t.Fatalf("result=%+v", result)
	}
	for _, tc := range []struct{ id, want string }{{"a", "出轨,巨乳"}, {"b", "业余,巨乳"}} {
		var genres string
		if e = s.SQLDB().QueryRow("SELECT genres FROM legacy_media_metadata WHERE media_id=?", tc.id).Scan(&genres); e != nil || genres != tc.want {
			t.Fatalf("genres=%q want=%q err=%v", genres, tc.want, e)
		}
	}
	var name string
	if e = s.SQLDB().QueryRow("SELECT name FROM tag_subscriptions").Scan(&name); e != nil || name != "出轨" {
		t.Fatalf("subscription=%q err=%v", name, e)
	}
	if e = s.SQLDB().QueryRow("SELECT tag_name FROM tag_subscription_matches WHERE media_id='a'").Scan(&name); e != nil || name != "出轨" {
		t.Fatalf("match=%q err=%v", name, e)
	}
	for _, tc := range []struct{ name, category string }{{"出軌", ""}, {"業餘", ""}, {"出轨", "主题"}, {"业余", "类别"}} {
		var category string
		e = s.SQLDB().QueryRow("SELECT category FROM tag_catalog WHERE name=?", tc.name).Scan(&category)
		if tc.category == "" {
			if e == nil {
				t.Fatalf("旧字典行 %q 未删除", tc.name)
			}
			continue
		}
		if e != nil || category != tc.category {
			t.Fatalf("catalog %q category=%q err=%v", tc.name, category, e)
		}
	}
	var catalogCount int
	if e = s.SQLDB().QueryRow("SELECT COUNT(*) FROM tag_catalog").Scan(&catalogCount); e != nil {
		t.Fatal(e)
	}
	again, e := NormalizeStoredTags(ctx, s, DialectSQLite, resolve)
	if e != nil {
		t.Fatal(e)
	}
	if again.GenreRows != 0 || again.Rules != 0 || again.Matches != 0 || again.CatalogAdded != 0 {
		t.Fatalf("重复执行仍有写入: %+v", again)
	}
	var catalogAgain int
	if e = s.SQLDB().QueryRow("SELECT COUNT(*) FROM tag_catalog").Scan(&catalogAgain); e != nil || catalogAgain != catalogCount {
		t.Fatalf("catalog=%d want=%d err=%v", catalogAgain, catalogCount, e)
	}
}

// TestNormalizeStoredTagsMergesSubscriptions 验证合并到同一权威名时保留更早的有效起始日。
func TestNormalizeStoredTagsMergesSubscriptions(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "tag-rules.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	for _, row := range []struct{ name, date string }{{"制服", "2026-09-10"}, {"制服誘惑", "2026-09-01"}} {
		if _, e = s.SQLDB().Exec("INSERT INTO tag_subscriptions(name,limit_date,updated_at) VALUES(?,?,'2026-09-01T00:00:00Z')", row.name, row.date); e != nil {
			t.Fatal(e)
		}
	}
	resolve := func(_ context.Context, names []string) (map[string]string, error) {
		mapping := map[string]string{}
		for _, name := range names {
			if name == "制服誘惑" {
				mapping[name] = "制服"
				continue
			}
			mapping[name] = name
		}
		return mapping, nil
	}
	if _, e = NormalizeStoredTags(ctx, s, DialectSQLite, resolve); e != nil {
		t.Fatal(e)
	}
	var count int
	var date string
	if e = s.SQLDB().QueryRow("SELECT COUNT(*) FROM tag_subscriptions").Scan(&count); e != nil || count != 1 {
		t.Fatalf("rules=%d err=%v", count, e)
	}
	if e = s.SQLDB().QueryRow("SELECT limit_date FROM tag_subscriptions").Scan(&date); e != nil || date != "2026-09-01" {
		t.Fatalf("date=%q err=%v", date, e)
	}
}
