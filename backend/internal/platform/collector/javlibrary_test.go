package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"testing"
)

// TestLibraryPages 保证新版 html 身份、显式番号及分页不受标题变动影响。
func TestLibraryPages(t *testing.T) {
	f := &pageFetcher{body: `<div class="videothumblist"><div class="video"><a href="./javabc.html"><div class="id">TEST-001</div><img src="https://img.example/1.jpg"><div class="title">测试</div></a></div></div><div class="page_selector"><a href="?page=2">下一页</a></div>`}
	b, e := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javlibrary", Kind: "rank", Period: "wanted", Page: 1})
	if e != nil || len(b.Items) != 1 {
		t.Fatalf("%+v %v", b, e)
	}
	if b.Items[0].SourceID != "javabc" || b.Items[0].Code != "TEST-001" || !b.HasMore {
		t.Fatal(b)
	}
	f.body = `<div id="video_title"><a href="/cn/javabc.html">测试</a></div><div id="video_id"><span class="text">TEST-001</span></div><div id="video_date"><span class="text">2026-09-01</span></div><div id="video_length"><span class="text">90</span></div><div id="video_genres"><a>标签甲</a></div><div id="video_cast"><span class="star"><a>演员甲</a></span></div>`
	b, e = collectUnreleasedFixture(context.Background(), f, ports.CollectionRequest{Source: "javlibrary", Kind: "detail", Query: "javabc", Page: 1})
	if e != nil || len(b.Items) != 1 {
		t.Fatalf("%+v %v", b, e)
	}
	if b.Items[0].DurationMinutes != 90 || len(b.Items[0].Tags) != 1 || len(b.Items[0].Actors) != 1 {
		t.Fatal(b)
	}
}
