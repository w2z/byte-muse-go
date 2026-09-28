package application_test

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/ports"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

type pagesCollector struct {
	total int
	loop  bool
	fail  int
}

type errorTranslator struct{}

type signalTranslator struct{ called chan struct{} }

func (t signalTranslator) Translate(context.Context, application.TranslationRequest) (string, error) {
	close(t.called)
	return "后台译文", nil
}

func TestQueueBackgroundLifecycleProcessesAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, e := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "workers.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	translated := make(chan struct{})
	service := application.NewQueuedCollectionService(pagesCollector{total: 1}, database.NewCollectionRepository(s.SQLDB(), database.DialectSQLite), signalTranslator{called: translated})
	run, e := service.Enqueue(ctx, ports.CollectionRequest{Source: "javdb", Kind: "search", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); service.Work(ctx) }()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	select {
	case <-translated:
	case <-timeout.C:
		t.Fatal("background translation not reached")
	}
	for {
		status, e := service.RunStatus(ctx, run.ID)
		if e != nil {
			t.Fatal(e)
		}
		if status.Status == "completed" {
			break
		}
		select {
		case <-timeout.C:
			t.Fatal("completion not committed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers did not stop")
	}
	var value string
	if e = s.SQLDB().QueryRow(`SELECT translated_title FROM media`).Scan(&value); e != nil || value != "后台译文" {
		t.Fatalf("%s %v", value, e)
	}
}

func (errorTranslator) Translate(context.Context, application.TranslationRequest) (string, error) {
	return "", fmt.Errorf("upstream secret must not be logged")
}

func TestQueuedTranslationFailureKeepsVideoAndStopsAfterThreeAttempts(t *testing.T) {
	ctx := context.Background()
	s, e := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "translation.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	service := application.NewQueuedCollectionService(pagesCollector{total: 1}, database.NewCollectionRepository(s.SQLDB(), database.DialectSQLite), errorTranslator{})
	run, e := service.Enqueue(ctx, ports.CollectionRequest{Source: "javdb", Kind: "search", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"page", "video"} {
		if _, e = service.ProcessOne(ctx, kind, time.Now()); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 3; i++ {
		if _, e = service.ProcessOne(ctx, "translation", time.Now().Add(time.Minute)); e != nil {
			t.Fatal(e)
		}
	}
	got, e := service.RunStatus(ctx, run.ID)
	if e != nil || got.Status != "partial_failed" || got.Saved != 1 || got.TranslationFailed != 1 || got.Error != "translation_failed" {
		t.Fatalf("%+v %v", got, e)
	}
	if ok, e := service.ProcessOne(ctx, "translation", time.Now().Add(time.Hour)); e != nil || ok {
		t.Fatalf("retry limit ignored %v %v", ok, e)
	}
	var n int
	if e = s.SQLDB().QueryRow(`SELECT count(*) FROM media`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("%d %v", n, e)
	}
}

func (p pagesCollector) Sources() []ports.CollectionSource { return nil }
func (p pagesCollector) Collect(_ context.Context, r ports.CollectionRequest) (ports.CollectionBatch, error) {
	if r.Page == p.fail {
		return ports.CollectionBatch{}, fmt.Errorf("upstream unavailable")
	}
	n := r.Page
	if p.loop {
		n = 1
	}
	return ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: fmt.Sprint(n), Code: fmt.Sprintf("TEST-%03d", n), Title: "测试影片", URL: fmt.Sprintf("https://javdb.com/v/%d", n)}}, HasMore: r.Page < p.total}, nil
}

func TestQueuedRankAllPagesAndAtomicPublication(t *testing.T) {
	ctx := context.Background()
	s, e := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "rank.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SQLDB().Exec(`INSERT INTO rank_entries(rank_type,position,code) VALUES('daily',1,'OLD-001')`); e != nil {
		t.Fatal(e)
	}
	service := application.NewQueuedCollectionService(pagesCollector{total: 102}, database.NewCollectionRepository(s.SQLDB(), database.DialectSQLite), nil)
	run, e := service.Enqueue(ctx, ports.CollectionRequest{Source: "javdb", Kind: "rank", Period: "daily"})
	if e != nil {
		t.Fatal(e)
	}
	for page := 1; page <= 102; page++ {
		if ok, e := service.ProcessOne(ctx, "page", time.Now()); e != nil || !ok {
			t.Fatalf("page %d: %v %v", page, ok, e)
		}
		if page < 102 {
			if ok, e := service.ProcessOne(ctx, "video", time.Now()); e != nil || !ok {
				t.Fatal(e)
			}
			var code string
			if e = s.SQLDB().QueryRow(`SELECT code FROM rank_entries WHERE rank_type='daily' AND position=1`).Scan(&code); e != nil || code != "OLD-001" {
				t.Fatalf("published incomplete rank: %s %v", code, e)
			}
		}
	}
	if ok, e := service.ProcessOne(ctx, "video", time.Now()); e != nil || !ok {
		t.Fatal(e)
	}
	got, e := service.RunStatus(ctx, run.ID)
	if e != nil || got.Pages != 102 || got.Saved != 102 || !got.RankPublished || got.Status != "completed" {
		t.Fatalf("%+v %v", got, e)
	}
	var n int
	if e = s.SQLDB().QueryRow(`SELECT count(*) FROM rank_entries WHERE rank_type='daily'`).Scan(&n); e != nil || n != 102 {
		t.Fatalf("%d %v", n, e)
	}
	for _, table := range []string{"subscriptions", "download_tasks"} {
		if e = s.SQLDB().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
			t.Fatalf("unexpected side effect %s: %d %v", table, n, e)
		}
	}
}

func TestQueuedPageFailurePreservesSavedVideosAndOldRank(t *testing.T) {
	for _, loop := range []bool{false, true} {
		t.Run(fmt.Sprint(loop), func(t *testing.T) {
			ctx := context.Background()
			s, e := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "rank.db")})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			if e = s.Migrate(ctx); e != nil {
				t.Fatal(e)
			}
			if _, e = s.SQLDB().Exec(`INSERT INTO rank_entries(rank_type,position,code) VALUES('daily',1,'OLD-001')`); e != nil {
				t.Fatal(e)
			}
			p := pagesCollector{total: 3, loop: loop}
			if !loop {
				p.fail = 2
			}
			service := application.NewQueuedCollectionService(p, database.NewCollectionRepository(s.SQLDB(), database.DialectSQLite), nil)
			run, e := service.Enqueue(ctx, ports.CollectionRequest{Source: "javdb", Kind: "rank", Period: "daily"})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = service.ProcessOne(ctx, "page", time.Now()); e != nil {
				t.Fatal(e)
			}
			if _, e = service.ProcessOne(ctx, "video", time.Now()); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 3; i++ {
				if _, e = service.ProcessOne(ctx, "page", time.Now().Add(time.Minute)); e != nil {
					t.Fatal(e)
				}
			}
			got, e := service.RunStatus(ctx, run.ID)
			if e != nil || got.Status != "failed" || got.Saved != 1 || got.RankPublished {
				t.Fatalf("%+v %v", got, e)
			}
			var code string
			if e = s.SQLDB().QueryRow(`SELECT code FROM rank_entries WHERE rank_type='daily'`).Scan(&code); e != nil || code != "OLD-001" {
				t.Fatalf("%s %v", code, e)
			}
		})
	}
}
