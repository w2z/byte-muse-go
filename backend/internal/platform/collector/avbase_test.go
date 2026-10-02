package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"strings"
	"testing"
)

// TestAVBaseActorAndDate 验证演员和发行日请求不混用关键词查询参数。
func TestAVBaseActorAndDate(t *testing.T) {
	for _, tc := range []struct{ kind, query, page, url string }{{"actor", "Actor", "/talents/[name]", "https://www.avbase.net/talents/Actor?page=1"}, {"date", "2026-09-28", "/works/date/[date]", "https://www.avbase.net/works/date/2026-09-28?page=1"}} {
		f := &pageFetcher{body: `<script id="__NEXT_DATA__">{"page":"` + tc.page + `","props":{"pageProps":{"page":1,"total":0,"works":[]}}}</script>`}
		b, e := collectUnreleasedFixture(context.Background(), f, ports.CollectionRequest{Source: "avbase", Kind: tc.kind, Query: tc.query, Page: 1})
		if e != nil || len(b.Items) != 0 || f.url != tc.url {
			t.Fatalf("%s %s %+v %v", tc.kind, f.url, b, e)
		}
	}
}

// TestRegistryAllowsAVBaseActor 锁定演员作品采集为已验收能力，避免被误判为未开放操作。
func TestRegistryAllowsAVBaseActor(t *testing.T) {
	f := &pageFetcher{body: `<script id="__NEXT_DATA__">{"page":"/talents/[name]","props":{"pageProps":{"page":1,"total":0,"works":[]}}}</script>`}
	if _, e := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "avbase", Kind: "actor", Query: "演员甲", Page: 1}); e != nil || !strings.Contains(f.url, "/talents/") {
		t.Fatalf("avbase actor=%v %s", e, f.url)
	}
}

func TestRegistryAllowsAVBaseDate(t *testing.T) {
	f := &pageFetcher{body: `<script id="__NEXT_DATA__">{"page":"/works/date/[date]","props":{"pageProps":{"page":1,"total":0,"works":[]}}}</script>`}
	if _, err := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "avbase", Kind: "date", Query: "2026-10-02", Page: 1}); err != nil {
		t.Fatalf("AVBase 日期采集应可用: %v", err)
	}
}

// TestAVBaseMetadata 验证 SSR 类型、显式 work_id、演员和日本本地发行日期。
func TestAVBaseMetadata(t *testing.T) {
	f := &pageFetcher{body: `<script id="__NEXT_DATA__">{"page":"/works","props":{"pageProps":{"page":1,"total":1,"works":[{"work_id":"TEST-001","title":"样例","min_date":"Mon Sep 28 2026 09:00:00 GMT+0900 (Japan Standard Time)","actors":[{"name":"演员甲","image_url":"https://img.example/a.jpg"}],"tags":[{"name":"标签甲"}],"products":[{"image_url":"https://img.example/c.jpg"}]}]}}}</script>`}
	b, e := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "avbase", Kind: "search", Query: "TEST", Page: 1})
	if e != nil || len(b.Items) != 1 {
		t.Fatalf("%+v %v", b, e)
	}
	m := b.Items[0]
	if m.Code != "TEST-001" || m.ReleaseDate != "2026-09-28" || len(m.Actors) != 1 || len(m.Tags) != 1 || m.PosterURL == "" {
		t.Fatal(m)
	}
	if f.url != "https://www.avbase.net/works?page=1&q=TEST" {
		t.Fatal(f.url)
	}
}
