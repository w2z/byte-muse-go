package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/ports"
)

// codeCollector 隔离外站网络，真实服务和 SQLite 仓储仍完整执行。
type codeCollector struct {
	collect func(context.Context, ports.CollectionRequest) (ports.CollectionBatch, error)
}

// TestCodeResolverPaginationAndRollback 验证下一页精确匹配、未查完、无关结果和持久化失败的区别。
func TestCodeResolverPaginationAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"next_page", ""}, {"unrelated", "未找到番号"}, {"incomplete", "未完成"}, {"invalid_record", "入库失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "codes.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			queries := application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.DialectSQLite))
			remote := codeCollector{collect: func(_ context.Context, req ports.CollectionRequest) (ports.CollectionBatch, error) {
				item := ports.CollectedMedia{SourceID: "right", Code: "TEST-001", Title: "Exact", URL: "https://example.test/right"}
				switch tc.name {
				case "next_page":
					if req.Page == 1 {
						return ports.CollectionBatch{Items: []ports.CollectedMedia{{Code: "TEST-0012"}}, HasMore: true}, nil
					}
				case "unrelated":
					item.Code = "TEST-0012"
				case "incomplete":
					return ports.CollectionBatch{HasMore: true}, nil
				case "invalid_record":
					item.Actors = []ports.CollectedActor{{Name: " "}}
				}
				return ports.CollectionBatch{Items: []ports.CollectedMedia{item}}, nil
			}}
			resolver := application.NewCodeResolver(queries, remote, database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite))
			media, err := resolver.Resolve(ctx, "TEST-001")
			wantCount := 0
			if tc.want == "" {
				if err != nil || media.Code != "TEST-001" {
					t.Fatalf("media=%+v err=%v", media, err)
				}
				wantCount = 1
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
			for _, table := range []string{"media", "collection_records"} {
				var count int
				if err = store.SQLDB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != wantCount {
					t.Fatalf("%s count=%d err=%v", table, count, err)
				}
			}
		})
	}
}

// TestCodeResolverConcurrentRequests 验证同一番号并发只写入一份资料，已取消的等待方不触发第二次采集。
func TestCodeResolverConcurrentRequests(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "codes.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	queries := application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.DialectSQLite))
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	remote := codeCollector{collect: func(context.Context, ports.CollectionRequest) (ports.CollectionBatch, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "right", Code: "TEST-001", Title: "Exact", URL: "https://example.test/right"}}}, nil
	}}
	resolver := application.NewCodeResolver(queries, remote, database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite))
	var wg sync.WaitGroup
	errorsCh := make(chan error, 12)
	wg.Add(1)
	go func() { defer wg.Done(); _, e := resolver.Resolve(ctx, "TEST-001"); errorsCh <- e }()
	<-started
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = resolver.Resolve(canceled, "TEST-001"); err == nil {
		t.Error("已取消的等待方应返回错误")
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := resolver.Resolve(ctx, "test 001"); errorsCh <- e }()
	}
	close(release)
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls=%d", calls.Load())
	}
	var count int
	if err = store.SQLDB().QueryRow("SELECT COUNT(*) FROM media").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

// TestCodeResolverLocalFailureDoesNotCollect 防止数据库异常被误当成不存在后联网或写入。
func TestCodeResolverLocalFailureDoesNotCollect(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "codes.db")})
	if err != nil {
		t.Fatal(err)
	}
	queries := application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.DialectSQLite))
	store.Close()
	remote := codeCollector{collect: func(context.Context, ports.CollectionRequest) (ports.CollectionBatch, error) {
		t.Error("本地查询失败不应联网")
		return ports.CollectionBatch{}, nil
	}}
	resolver := application.NewCodeResolver(queries, remote, database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite))
	if _, err = resolver.Resolve(ctx, "TEST-001"); err == nil || !strings.Contains(err.Error(), "本地影片查询失败") {
		t.Fatalf("err=%v", err)
	}
}

func (c codeCollector) Sources() []ports.CollectionSource {
	return []ports.CollectionSource{{ID: "javdb", Kinds: []string{"search"}}, {ID: "netflav", Kinds: []string{"search"}}}
}

func (c codeCollector) Collect(ctx context.Context, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	return c.collect(ctx, req)
}

func TestCodeResolverPersistsOnlyExactMatch(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "codes.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	queries := application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.DialectSQLite))
	calls := 0
	remote := codeCollector{collect: func(_ context.Context, req ports.CollectionRequest) (ports.CollectionBatch, error) {
		calls++
		if req.Query != "TEST-001" {
			t.Fatalf("query=%q", req.Query)
		}
		if req.Source == "javdb" {
			return ports.CollectionBatch{}, errors.New("upstream unavailable")
		}
		return ports.CollectionBatch{Items: []ports.CollectedMedia{
			{SourceID: "wrong", Code: "TEST-0012", Title: "Other", URL: "https://example.test/wrong"},
			{SourceID: "right", Code: "test 001", Title: "Exact", URL: "https://example.test/right"},
		}}, nil
	}}
	resolver := application.NewCodeResolver(queries, remote, database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite))
	media, err := resolver.Resolve(ctx, "test 001")
	if err != nil || media.Code != "TEST-001" || media.Title != "Exact" || media.ID == "" {
		t.Fatalf("media=%+v err=%v", media, err)
	}
	if _, err = resolver.Resolve(ctx, "TEST-001"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("local hit must skip network: calls=%d", calls)
	}
	for _, table := range []string{"media", "collection_records"} {
		var count int
		if err = store.SQLDB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

func TestCodeResolverDistinguishesEmptyAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		want    string
	}{
		{"empty", nil, "未找到番号"},
		{"network", errors.New("secret upstream details"), "远程查询失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "codes.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			queries := application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.DialectSQLite))
			remote := codeCollector{collect: func(context.Context, ports.CollectionRequest) (ports.CollectionBatch, error) {
				return ports.CollectionBatch{}, tc.failure
			}}
			resolver := application.NewCodeResolver(queries, remote, database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite))
			_, err = resolver.Resolve(ctx, "TEST-999")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err=%v", err)
			}
			var count int
			if err = store.SQLDB().QueryRow("SELECT COUNT(*) FROM media").Scan(&count); err != nil || count != 0 {
				t.Fatalf("count=%d err=%v", count, err)
			}
		})
	}
}
