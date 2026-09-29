package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"testing"
)

// TestNewSourcesRejectUnknownPages 防止验证码或结构改版被记录为成功空结果。
func TestNewSourcesRejectUnknownPages(t *testing.T) {
	for _, source := range []string{"javlibrary", "avbase", "javbus", "jable", "supjav"} {
		for _, kind := range []string{"search", "detail"} {
			query := "sample"
			if source == "javlibrary" {
				query = "javsample"
			}
			if source == "supjav" {
				query = "123"
			}
			_, e := collectUnreleasedFixture(context.Background(), &pageFetcher{body: `<html>unexpected page</html>`}, ports.CollectionRequest{Source: source, Kind: kind, Query: query, Page: 1})
			if !errors.Is(e, ErrParse) {
				t.Fatalf("%s %s %v", source, kind, e)
			}
		}
	}
}

// TestSupJavEmptyAndExcludedSources 验证明确无结果可成功，而排除站点不能入队。
func TestSupJavEmptyAndExcludedSources(t *testing.T) {
	f := &pageFetcher{body: `<body class="search search-no-results"><div class="content">Search Result For: TEST(0)</div></body>`}
	b, e := collectUnreleasedFixture(context.Background(), f, ports.CollectionRequest{Source: "supjav", Kind: "search", Query: "TEST", Page: 1})
	if e != nil || len(b.Items) != 0 || b.HasMore {
		t.Fatalf("%+v %v", b, e)
	}
	for _, source := range []string{"thisav", "avgle"} {
		if e = NewRegistry(nil).Validate(ports.CollectionRequest{Source: source, Kind: "search", Query: "TEST", Page: 1}); !errors.Is(e, ErrUnsupported) {
			t.Fatalf("%s %v", source, e)
		}
	}
}

// TestExtraSearchLists 验证三站列表卡片、翻页和无番号的保守关联。
func TestExtraSearchLists(t *testing.T) {
	for _, tc := range []struct{ source, body, code string }{
		{"jable", `<div id="list_videos_videos_list_search_result"><div class="video-img-box"><div class="detail"><h6 class="title"><a href="https://jable.tv/videos/sample/">TEST-001 样例</a></h6></div></div></div><ul class="pagination"><a data-parameters="q:TEST;from:02" href="/search/2/">2</a></ul>`, ""},
		{"supjav", `<div class="post"><h3><a rel="bookmark" href="https://supjav.com/123.html">TEST-001 样例</a></h3></div><div class="pagination"><span class="next-page"><a href="/page/2?s=TEST">2</a></span></div>`, ""},
		{"javbus", `<div id="waterfall"><a class="movie-box" href="https://www.javbus.com/TEST-001"><img title="样例"><div class="photo-info"><date>TEST-001</date><date>2026-09-28</date></div></a></div><a id="next" href="/search/TEST/2">下一页</a>`, "TEST-001"},
	} {
		b, e := collectUnreleasedFixture(context.Background(), &pageFetcher{body: tc.body}, ports.CollectionRequest{Source: tc.source, Kind: "search", Query: "TEST", Page: 1})
		if e != nil || len(b.Items) != 1 || !b.HasMore || b.Items[0].Code != tc.code {
			t.Fatalf("%s %+v %v", tc.source, b, e)
		}
	}
}

// TestStreamMetadata 只采集公开资料，无显式番号不猜测标题，不把上传时间写入发行日期。
func TestStreamMetadata(t *testing.T) {
	for _, tc := range []struct{ source, query, body string }{
		{"jable", "sample", `<link rel="canonical" href="https://jable.tv/videos/sample/"><meta property="og:image" content="https://img.example/1.jpg"><a href="/tags/menu/">导航</a><div id="site-content"><div class="header-left"><h4>TEST-001 样例</h4></div><p class="tags"><a href="https://jable.tv/tags/test/">测试标签</a></p><a class="model" href="https://jable.tv/models/a/"><img title="演员甲" src="https://img.example/a.jpg"></a></div>`},
		{"supjav", "123", `<link rel="canonical" href="https://supjav.com/123.html"><div class="archive-title"><h1>TEST-001 样例</h1></div><a href="/tag/menu">导航</a><div class="tags"><a href="https://supjav.com/tag/test">测试标签</a></div>`},
	} {
		b, e := collectUnreleasedFixture(context.Background(), &pageFetcher{body: tc.body}, ports.CollectionRequest{Source: tc.source, Kind: "detail", Query: tc.query, Page: 1})
		if e != nil || len(b.Items) != 1 {
			t.Fatalf("%s %+v %v", tc.source, b, e)
		}
		if b.Items[0].Code != "" || b.Items[0].ReleaseDate != "" || len(b.Items[0].Tags) != 1 || b.Items[0].Tags[0] != "测试标签" {
			t.Fatalf("%s %+v", tc.source, b)
		}
	}
}

// collectUnreleasedFixture 仅供离线解析回归，不向运行时注册未通过实网验收的来源。
func collectUnreleasedFixture(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	switch req.Source {
	case "javlibrary":
		return collectJavLibrary(ctx, f, req)
	case "avbase":
		return collectAVBase(ctx, f, req)
	case "javbus":
		if req.Kind == "search" {
			return collectJavBusSearch(ctx, f, req)
		}
		return collectJavBusDetail(ctx, f, req)
	case "jable":
		if req.Kind == "search" {
			return collectJableSearch(ctx, f, req)
		}
		return collectStreamMetadata(ctx, f, req)
	case "supjav":
		if req.Kind == "search" {
			return collectSupJavSearch(ctx, f, req)
		}
		return collectStreamMetadata(ctx, f, req)
	}
	return ports.CollectionBatch{}, ErrUnsupported
}
