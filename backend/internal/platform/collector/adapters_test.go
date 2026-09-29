package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestLiveCollection 仅显式启用时低频访问外站；不保存返回数据、不获取媒体文件。
func TestLiveCollection(t *testing.T) {
	if os.Getenv("BYTEMUSE_LIVE_COLLECTION") != "1" {
		t.Skip("需要显式启用真实站点核验")
	}
	registry := NewRegistry(NewClient(nil))
	for _, req := range []ports.CollectionRequest{
		{Source: "javdb", Kind: "search", Query: "TEST", Page: 1},
		{Source: "javdb", Kind: "rank", Period: "daily", Page: 1},
		{Source: "javdb", Kind: "detail", Query: "96rq0V", Page: 1},
		{Source: "netflav", Kind: "search", Query: "TEST", Page: 1},
	} {
		t.Run(req.Source+"_"+req.Kind, func(t *testing.T) {
			b, e := registry.Collect(context.Background(), req)
			if e != nil {
				t.Fatalf("source=%s kind=%s error=%v", req.Source, req.Kind, e)
			}
			t.Logf("items=%d has_more=%v", len(b.Items), b.HasMore)
			if req.Source == "netflav" && len(b.Items) > 0 {
				d, e := registry.Collect(context.Background(), ports.CollectionRequest{Source: "netflav", Kind: "detail", Query: b.Items[0].SourceID, Page: 1})
				if e != nil || len(d.Items) != 1 {
					t.Fatalf("detail items=%d err=%v", len(d.Items), e)
				}
				t.Logf("detail actors=%d release_known=%v", len(d.Items[0].Actors), d.Items[0].ReleaseDate != "")
			}
		})
	}
}

func TestDetailRejectsUnusedPage(t *testing.T) {
	_, e := NewRegistry(&pageFetcher{body: ``}).Collect(context.Background(), ports.CollectionRequest{Source: "netflav", Kind: "detail", Query: "abc", Page: 2})
	if !errors.Is(e, ErrInvalidRequest) {
		t.Fatal(e)
	}
}

type pageFetcher struct {
	body string
	url  string
}

// TestRegistryRejectsUnverifiedCapabilities 防止注册站点时意外开放未验收的操作和榜单。
func TestRegistryRejectsUnverifiedCapabilities(t *testing.T) {
	for _, req := range []ports.CollectionRequest{
		{Source: "javlibrary", Kind: "search", Query: "TEST", Page: 1},
		{Source: "javlibrary", Kind: "detail", Query: "javsample", Page: 1},
		{Source: "javlibrary", Kind: "rank", Period: "bestrated", Page: 1},
		{Source: "avbase", Kind: "date", Query: "2026-09-28", Page: 1},
		{Source: "avbase", Kind: "detail", Query: "TEST-001", Page: 1},
		{Source: "jable", Kind: "detail", Query: "sample", Page: 1},
		{Source: "supjav", Kind: "detail", Query: "123", Page: 1},
		{Source: "javbus", Kind: "search", Query: "TEST", Page: 1},
	} {
		f := &pageFetcher{}
		if _, err := NewRegistry(f).Collect(context.Background(), req); err == nil || f.url != "" {
			t.Fatalf("unverified request reached fetcher: %+v err=%v", req, err)
		}
	}
}

func TestRegistryAllowsRankPagesBeyondOneHundred(t *testing.T) {
	f := &pageFetcher{body: `<div class="movie-list"></div>`}
	_, e := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "rank", Period: "daily", Page: 101})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(f.url, "page=101") {
		t.Fatal(f.url)
	}
}

func TestNetflavValidEmptyResultAndWrongPage(t *testing.T) {
	f := &pageFetcher{body: `<script id="__NEXT_DATA__">{"page":"/search","props":{"initialState":{"search":{"docs":[],"page":1,"pages":0}}}}</script>`}
	req := ports.CollectionRequest{Source: "netflav", Kind: "search", Query: "notfound", Page: 1}
	b, e := NewRegistry(f).Collect(context.Background(), req)
	if e != nil || len(b.Items) != 0 || b.HasMore {
		t.Fatalf("%+v %v", b, e)
	}
	req.Page = 2
	if _, e = NewRegistry(f).Collect(context.Background(), req); !errors.Is(e, ErrParse) {
		t.Fatal(e)
	}
}

func (f *pageFetcher) Get(_ context.Context, u string) ([]byte, error) {
	f.url = u
	return []byte(f.body), nil
}

func TestJavDBListUsesExplicitCodeAndDate(t *testing.T) {
	f := &pageFetcher{body: `<div class="movie-list"><div class="item"><a href="/v/abc12" class="box" title="测试影片"><div class="video-title"><strong>TEST-001</strong>测试影片</div><div class="meta">2026-09-01</div></a></div></div><a class="pagination-next" href="?page=2">下一页</a>`}
	b, e := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "rank", Period: "weekly", Page: 1})
	if e != nil || len(b.Items) != 1 {
		t.Fatalf("%+v %v", b, e)
	}
	if b.Items[0].Code != "TEST-001" || b.Items[0].SourceID != "abc12" || b.Items[0].ReleaseDate != "2026-09-01" || !b.HasMore {
		t.Fatalf("%+v", b)
	}
	if f.url != "https://javdb.com/rankings/movies?p=weekly&page=1&t=censored" {
		t.Fatal(f.url)
	}
	if b.Items[0].VideoType != "censored" {
		t.Fatalf("rank type: %q", b.Items[0].VideoType)
	}
	b, e = NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "search", Query: "TEST", Page: 1})
	// 搜索结果没有类别字段，交回统一分类规则；常规番号按行业常态归为有码。
	if e != nil || b.Items[0].VideoType != "censored" {
		t.Fatalf("search classification: %+v %v", b, e)
	}
}

func TestNetflavStructuredDataAndActorAliases(t *testing.T) {
	f := &pageFetcher{body: `<script id="__NEXT_DATA__" type="application/json">{"page":"/search","props":{"initialState":{"search":{"docs":[{"videoId":"abc12","code":"TEST-001","title":"测试影片","actors":["jp:演员甲","演员甲","en:Actor A"],"sourceDate":"2026-01-01T00:00:00Z"}],"page":2,"pages":3}}}}</script>`}
	b, e := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "netflav", Kind: "search", Query: "TEST-001", Page: 2})
	if e != nil || len(b.Items) != 1 {
		t.Fatalf("%+v %v", b, e)
	}
	m := b.Items[0]
	if m.ReleaseDate != "" || len(m.Actors) != 1 || m.Actors[0].Name != "演员甲" || !b.HasMore {
		t.Fatalf("%+v", b)
	}
	if f.url != "https://netflav.com/search?keyword=TEST-001&page=2&type=title" {
		t.Fatal(f.url)
	}
}

func TestAdaptersRejectUnknownStructureAndWrongDetailIdentity(t *testing.T) {
	for _, source := range []string{"javdb", "netflav"} {
		_, e := NewRegistry(&pageFetcher{body: `<html>unexpected page</html>`}).Collect(context.Background(), ports.CollectionRequest{Source: source, Kind: "search", Query: "test", Page: 1})
		if !errors.Is(e, ErrParse) {
			t.Fatalf("%s %v", source, e)
		}
	}
	f := &pageFetcher{body: `<script id="__NEXT_DATA__">{"page":"/video","props":{"initialState":{"video":{"data":{"videoId":"other","title":"测试"}}}}}</script>`}
	_, e := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "netflav", Kind: "detail", Query: "expected", Page: 1})
	if !errors.Is(e, ErrParse) {
		t.Fatal(e)
	}
}
