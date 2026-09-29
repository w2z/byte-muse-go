package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"testing"
)

// TestJavBusDetail 验证明确识别码及演员分类分离，不把演员作为影片标签。
func TestJavBusDetail(t *testing.T) {
	f := &pageFetcher{body: `<h3>TEST-001 样例</h3><a class="bigImage" href="/pics/cover/test.jpg"></a><div class="info"><p><span class="header">識別碼:</span><span>TEST-001</span></p><p><span class="header">發行日期:</span>2026-09-28</p><p><span class="header">長度:</span>90分鐘</p><span class="genre"><a href="https://www.javbus.com/genre/1">标签甲</a></span><span class="genre"><a href="https://www.javbus.com/star/a">演员甲</a></span></div>`}
	b, e := collectUnreleasedFixture(context.Background(), f, ports.CollectionRequest{Source: "javbus", Kind: "detail", Query: "TEST-001", Page: 1})
	if e != nil || len(b.Items) != 1 {
		t.Fatalf("%+v %v", b, e)
	}
	m := b.Items[0]
	if m.Code != "TEST-001" || m.ReleaseDate != "2026-09-28" || m.DurationMinutes != 90 || len(m.Actors) != 1 || len(m.Tags) != 1 {
		t.Fatal(m)
	}
}
