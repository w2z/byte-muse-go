package collector

import (
	"bytemuse/backend/internal/platform/javdbapp"
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

type appDetailFetcher struct {
	movie     javdbapp.Movie
	err       error
	htmlCalls int
}

func (f *appDetailFetcher) Get(context.Context, string) ([]byte, error) {
	f.htmlCalls++
	return nil, ErrUnavailable
}
func (f *appDetailFetcher) MovieDetail(context.Context, string) (javdbapp.Movie, error) {
	return f.movie, f.err
}

func TestJavDBAppDetailDoesNotRequireHTML(t *testing.T) {
	f := &appDetailFetcher{movie: javdbapp.Movie{ID: "movie1", Number: "TEST-001", Title: "测试影片", Actors: []javdbapp.Actor{{ID: "actor1", Name: "演员甲", NameZHT: "演員甲", AvatarURL: "https://img.example/a.jpg"}}}}
	b, err := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "movie1", Page: 1})
	if err != nil || len(b.Items) != 1 {
		t.Fatalf("%+v %v", b, err)
	}
	if f.htmlCalls != 0 {
		t.Fatal("App success still fetched HTML")
	}
	a := b.Items[0].Actors[0]
	if a.Name != "演员甲" || a.Photo != "https://img.example/a.jpg" || len(a.Aliases) != 1 || a.Aliases[0] != "演員甲" {
		t.Fatalf("%+v", a)
	}
}

func TestJavDBAppCancellationDoesNotFallBack(t *testing.T) {
	f := &appDetailFetcher{err: context.Canceled}
	_, err := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "movie1", Page: 1})
	if !errors.Is(err, context.Canceled) || f.htmlCalls != 0 {
		t.Fatalf("err=%v html=%d", err, f.htmlCalls)
	}
}

func TestJavDBAppRateLimitDoesNotFallBack(t *testing.T) {
	f := &appDetailFetcher{err: javdbapp.ErrRateLimited}
	_, err := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "m1", Page: 1})
	if !errors.Is(err, ErrRateLimited) || f.htmlCalls != 0 {
		t.Fatalf("err=%v html=%d", err, f.htmlCalls)
	}
}

func TestJavDBAppFailureFallsBackToHTML(t *testing.T) {
	f := &appDetailFetcher{err: errors.New("upstream unavailable")}
	_, err := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "m1", Page: 1})
	if !errors.Is(err, ErrUnavailable) || f.htmlCalls != 1 {
		t.Fatalf("err=%v html=%d", err, f.htmlCalls)
	}
}

type appActorFetcher struct {
	pageFetcher
	actorID string
	page    int
}

func (f *appActorFetcher) ActorMovies(_ context.Context, id string, page int) ([]javdbapp.Movie, error) {
	f.actorID = id
	f.page = page
	return []javdbapp.Movie{{ID: "m1", Number: "TEST-001", Title: "测试影片"}}, nil
}
func TestJavDBActorCollectionUsesSourceID(t *testing.T) {
	f := &appActorFetcher{}
	batch, err := NewRegistry(f).Collect(context.Background(), ports.CollectionRequest{Source: "javdb", Kind: "actor", Query: "actor1", Page: 2})
	if err != nil || len(batch.Items) != 1 || f.actorID != "actor1" || f.page != 2 {
		t.Fatalf("%+v %+v %v", batch, f, err)
	}
	if err = NewRegistry(f).Validate(ports.CollectionRequest{Source: "javdb", Kind: "actor", Query: "演员名", Page: 1}); err == nil {
		t.Fatal("actor name accepted as source ID")
	}
}
