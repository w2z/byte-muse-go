package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"testing"
)

// TestJavDBDetailMetadata 验证详情身份、显式番号和分区字段，避免把站点导航当标签。
func TestJavDBDetailMetadata(t *testing.T) {
	f := &pageFetcher{body: `<link rel="canonical" href="https://javdb.com/v/abc12"><h2 class="title"><strong>TEST-001</strong><strong class="current-title">测试标题</strong></h2><img class="video-cover" src="https://img.example/cover.jpg"><a href="/tags?c1=menu">导航分类</a><div class="panel-block"><strong>番號:</strong><span class="value"><a>TEST</a>-001</span></div><div class="panel-block"><strong>日期:</strong><span class="value">2026-09-01</span></div><div class="panel-block"><strong>時長:</strong><span class="value">120 分鐘</span></div><div class="panel-block"><strong>類別:</strong><span class="value"><a href="/tags?c1=1">测试标签</a></span></div><div class="panel-block"><strong>演員:</strong><span class="value"><a href="/actors/actor1">演员甲</a></span></div>`}
	b, err := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "abc12", Page: 1})
	if err != nil || len(b.Items) != 1 {
		t.Fatalf("items=%d err=%v", len(b.Items), err)
	}
	m := b.Items[0]
	if m.Code != "TEST-001" || m.SourceID != "abc12" || m.Title != "测试标题" || m.ReleaseDate != "2026-09-01" || m.DurationMinutes != 120 || len(m.Tags) != 1 || m.Tags[0] != "测试标签" || len(m.Actors) != 1 || m.Actors[0].Name != "演员甲" || b.HasMore {
		t.Fatalf("%+v", m)
	}
	if f.url != "https://javdb.com/v/abc12" {
		t.Fatal(f.url)
	}
	f.body = `<link rel="canonical" href="https://javdb.com/v/wrong"><h2><strong class="current-title">测试</strong></h2>`
	if _, err = NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "abc12", Page: 1}); !errors.Is(err, ErrParse) {
		t.Fatal(err)
	}
}
