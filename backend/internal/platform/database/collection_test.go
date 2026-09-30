package database

import (
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCollectionQueueTagsRollback 验证标签与快照同事务，单影片失败不污染已提交数据。
func TestCollectionQueueTagsRollback(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "rollback.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	req := ports.CollectionRequest{Source: "netflav", Kind: "search", Query: "TEST", Page: 1}
	item := ports.CollectedMedia{SourceID: "sample", Code: "TEST-001", Title: "样例", URL: "https://netflav.com/video?id=sample", Tags: []string{"原标签"}}
	if _, err = r.SaveCollection(ctx, req, ports.CollectionBatch{Items: []ports.CollectedMedia{item}}); err != nil {
		t.Fatal(err)
	}
	item.Tags = []string{"新增标签"}
	item.Actors = []ports.CollectedActor{{Name: " "}} // 标签合并后触发校验失败。
	run, err := r.CreateRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.Claim(ctx, "page", time.Now())
	if err != nil || page == nil {
		t.Fatalf("page: %v", err)
	}
	if err = r.SavePage(ctx, *page, ports.CollectionBatch{Items: []ports.CollectedMedia{item}}); err != nil {
		t.Fatal(err)
	}
	work, err := r.Claim(ctx, "video", time.Now())
	if err != nil || work == nil {
		t.Fatalf("video: %v", err)
	}
	if err = r.SaveVideo(ctx, *work, false); err == nil {
		t.Fatal("invalid actor accepted")
	}
	var genres, payload string
	if err = s.SQLDB().QueryRow(`SELECT genres FROM legacy_media_metadata`).Scan(&genres); err != nil || genres != "原标签" {
		t.Fatalf("partial tags: %q %v", genres, err)
	}
	if err = s.SQLDB().QueryRow(`SELECT payload_json FROM collection_records`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var saved ports.CollectedMedia
	if err = json.Unmarshal([]byte(payload), &saved); err != nil || len(saved.Tags) != 1 || saved.Tags[0] != "原标签" {
		t.Fatalf("partial snapshot: %v", err)
	}
	got, err := r.GetRun(ctx, run.ID)
	if err != nil || got.Saved != 0 {
		t.Fatalf("failed item counted: %+v %v", got, err)
	}
	// 同一租约中的失败事务未消费任务，修复样例后必须能完整提交标签与成功计数。
	item.Actors = nil
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	work.Payload = string(encoded)
	if err = r.SaveVideo(ctx, *work, false); err != nil {
		t.Fatal(err)
	}
	if err = s.SQLDB().QueryRow(`SELECT genres FROM legacy_media_metadata`).Scan(&genres); err != nil || genres != "原标签,新增标签" {
		t.Fatalf("queue tags: %q %v", genres, err)
	}
	got, err = r.GetRun(ctx, run.ID)
	if err != nil || got.Saved != 1 {
		t.Fatalf("successful item missing: %+v %v", got, err)
	}
}

// TestLiveCollectionTagsSample 仅显式启用时采一页，读取最多三部详情验证标签队列入库。
// 真实数据仅写入自动清理的临时库，不读取或更新在用影片库，也不采集后续页。
func TestLiveCollectionTagsSample(t *testing.T) {
	if os.Getenv("BYTEMUSE_LIVE_COLLECTION") != "1" {
		t.Skip("需要显式启用真实站点核验")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := ports.CollectionRequest{Source: "netflav", Kind: "search", Query: "TEST", Page: 1}
	registry := collector.NewRegistry(collector.NewClient(nil))
	batch, err := registry.Collect(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	sample := ports.CollectionBatch{}
	for i, item := range batch.Items {
		if i == 3 {
			break
		}
		detail, err := registry.Collect(ctx, ports.CollectionRequest{Source: "netflav", Kind: "detail", Query: item.SourceID, Page: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Items) != 1 {
			t.Fatal("详情结果数量异常")
		}
		if detail.Items[0].Code != "" && len(detail.Items[0].Tags) > 0 {
			sample.Items = append(sample.Items, detail.Items[0])
		}
	}
	if len(sample.Items) == 0 {
		t.Fatal("当前页无可验证的标签样例")
	}
	s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "live-tags.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	storedGenres := map[string]string{}
	for round := 0; round < 2; round++ {
		run, err := r.CreateRun(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		page, err := r.Claim(ctx, "page", time.Now())
		if err != nil || page == nil {
			t.Fatalf("page: %v", err)
		}
		if err = r.SavePage(ctx, *page, sample); err != nil {
			t.Fatal(err)
		}
		for range sample.Items {
			work, err := r.Claim(ctx, "video", time.Now())
			if err != nil || work == nil {
				t.Fatalf("video: %v", err)
			}
			if err = r.SaveVideo(ctx, *work, false); err != nil {
				t.Fatal(err)
			}
		}
		got, err := r.GetRun(ctx, run.ID)
		if err != nil || got.Saved != len(sample.Items) {
			t.Fatalf("run: %+v %v", got, err)
		}
		for _, item := range sample.Items {
			var genres string
			if err = s.SQLDB().QueryRow(`SELECT genres FROM legacy_media_metadata WHERE UPPER(code)=UPPER(?)`, item.Code).Scan(&genres); err != nil {
				t.Fatal(err)
			}
			if round == 1 && genres != storedGenres[item.Code] {
				t.Fatal("重复采集改变标签")
			}
			storedGenres[item.Code] = genres
		}
	}
	var mediaCount, taggedCount int
	if err = s.SQLDB().QueryRow(`SELECT count(*) FROM media`).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	if err = s.SQLDB().QueryRow(`SELECT count(*) FROM legacy_media_metadata WHERE genres IS NOT NULL AND TRIM(genres)<>''`).Scan(&taggedCount); err != nil {
		t.Fatal(err)
	}
	if mediaCount != len(sample.Items) || taggedCount != len(sample.Items) {
		t.Fatalf("media=%d tagged=%d sample=%d", mediaCount, taggedCount, len(sample.Items))
	}
	t.Logf("search_pages=1 detail_limit=3 sampled=%d tagged=%d rounds=2 production_writes=0", mediaCount, taggedCount)
}

// TestCollectionMergesTags 保护已有标签及业务字段，验证新建、补充、空结果和重复入库。
func TestCollectionMergesTags(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
		metadata bool
		old      any
		tags     []string
		want     string
	}{
		{name: "new_normalized", tags: []string{" 标签甲 ", "", "标签甲", "标签乙"}, want: "标签甲,标签乙"},
		{name: "append_preserves_manual", existing: true, metadata: true, old: " 手工标签 ,标签甲", tags: []string{"标签甲", " 标签乙 ", "标签乙"}, want: " 手工标签 ,标签甲,标签乙"},
		{name: "null", existing: true, metadata: true, tags: []string{"标签甲"}, want: "标签甲"},
		{name: "missing_metadata", existing: true, tags: []string{"标签甲"}, want: "标签甲"},
		{name: "empty_preserves", existing: true, metadata: true, old: " 手工标签 ,标签甲", tags: []string{" ", ""}, want: " 手工标签 ,标签甲"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "tags.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if tc.existing {
				_, err = s.SQLDB().Exec(`INSERT INTO media(id,code,title,translated_title,subscription_status,library_status,created_at,updated_at) VALUES('custom-id','test-001','人工标题','人工译文','active','present','2026-09-01','2026-09-01')`)
				if err != nil {
					t.Fatal(err)
				}
				if tc.metadata {
					_, err = s.SQLDB().Exec(`INSERT INTO legacy_media_metadata(media_id,code,genres,casts,legacy_status,legacy_mode) VALUES('custom-id','test-001',?,'人工演员','CANCEL','STRICT')`, tc.old)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			r := NewCollectionRepository(s.SQLDB(), DialectSQLite)
			req := ports.CollectionRequest{Source: "netflav", Kind: "search", Page: 1}
			batch := ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "tags", Code: "TEST-001", Title: "来源标题", URL: "https://netflav.com/video?id=tags", Tags: tc.tags}}}
			for i := 0; i < 2; i++ {
				counts, err := r.SaveCollection(ctx, req, batch)
				if err != nil {
					t.Fatal(err)
				}
				if (tc.existing || i > 0) && counts.MediaInserted != 0 {
					t.Fatalf("duplicate media: %+v", counts)
				}
				var genres string
				if err = s.SQLDB().QueryRow(`SELECT genres FROM legacy_media_metadata`).Scan(&genres); err != nil || genres != tc.want {
					t.Fatalf("genres=%q want=%q err=%v", genres, tc.want, err)
				}
			}
			var payload string
			if err = s.SQLDB().QueryRow(`SELECT payload_json FROM collection_records`).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			var saved ports.CollectedMedia
			if err = json.Unmarshal([]byte(payload), &saved); err != nil {
				t.Fatal(err)
			}
			if len(saved.Tags) != len(tc.tags) {
				t.Fatal("source snapshot tags changed")
			}
			for i := range tc.tags {
				if saved.Tags[i] != tc.tags[i] {
					t.Fatal("source snapshot tags changed")
				}
			}
			if tc.existing {
				var title, translated, status, library, updated string
				if err = s.SQLDB().QueryRow(`SELECT title,translated_title,subscription_status,library_status,updated_at FROM media WHERE id='custom-id'`).Scan(&title, &translated, &status, &library, &updated); err != nil {
					t.Fatal(err)
				}
				if title != "人工标题" || translated != "人工译文" || status != "active" || library != "present" || updated != "2026-09-01" {
					t.Fatal("unrelated media fields changed")
				}
				if tc.metadata {
					var casts, legacyStatus, mode string
					if err = s.SQLDB().QueryRow(`SELECT casts,legacy_status,legacy_mode FROM legacy_media_metadata`).Scan(&casts, &legacyStatus, &mode); err != nil {
						t.Fatal(err)
					}
					if casts != "人工演员" || legacyStatus != "CANCEL" || mode != "STRICT" {
						t.Fatal("unrelated legacy fields changed")
					}
				}
			}
		})
	}
}

func TestCollectionWithoutCodeDoesNotInventMediaAndUnknownDurationIsNull(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "test.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	r := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	req := ports.CollectionRequest{Source: "netflav", Kind: "search", Page: 1}
	b := ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "unknown", URL: "https://netflav.com/video?id=unknown", Title: "未知番号"}, {SourceID: "known", URL: "https://netflav.com/video?id=known", Title: "已知番号", Code: "TEST-001"}}}
	c, e := r.SaveCollection(ctx, req, b)
	if e != nil || c.Inserted != 2 || c.MediaInserted != 1 {
		t.Fatalf("%+v %v", c, e)
	}
	var duration sql.NullInt64
	if e = s.SQLDB().QueryRow(`SELECT duration_minutes FROM media`).Scan(&duration); e != nil || duration.Valid {
		t.Fatalf("unknown duration stored as known: %+v %v", duration, e)
	}
}

func TestCollectionAtomicIdempotentAndPreservesExisting(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "test.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	r := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	req := ports.CollectionRequest{Source: "javdb", Kind: "rank", Period: "daily", Page: 1}
	b := ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "a", URL: "https://javdb.com/v/a", Code: "TEST-001", Title: "原始标题", Actors: []ports.CollectedActor{{Name: "演员甲"}}}}}
	counts, e := r.SaveCollection(ctx, req, b)
	if e != nil || counts.Inserted != 1 || counts.MediaInserted != 1 || counts.ActorsInserted != 1 {
		t.Fatalf("%+v %v", counts, e)
	}
	if _, e = s.SQLDB().Exec(`UPDATE media SET translated_title='人工翻译',subscription_status='active',library_status='present'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SQLDB().Exec(`UPDATE actors SET limit_date='2026-09-01'`); e != nil {
		t.Fatal(e)
	}
	b.Items[0].Title = "新来源标题"
	counts, e = r.SaveCollection(ctx, req, b)
	if e != nil || counts.Existing != 1 || counts.Inserted != 0 || counts.MediaInserted != 0 || counts.ActorsInserted != 0 {
		t.Fatalf("%+v %v", counts, e)
	}
	var title, translated, status, library, limit string
	s.SQLDB().QueryRow(`SELECT title,translated_title,subscription_status,library_status FROM media`).Scan(&title, &translated, &status, &library)
	s.SQLDB().QueryRow(`SELECT limit_date FROM actors`).Scan(&limit)
	if title != "原始标题" || translated != "人工翻译" || status != "active" || library != "present" || limit != "2026-09-01" {
		t.Fatalf("historical state changed %s %s %s %s %s", title, translated, status, library, limit)
	}
	if _, e = r.SaveCollection(ctx, req, ports.CollectionBatch{}); e != nil {
		t.Fatal(e)
	}
	var n int
	s.SQLDB().QueryRow(`SELECT count(*) FROM rank_entries`).Scan(&n)
	if n != 0 {
		t.Fatal("metadata write unexpectedly published rank")
	}
	b.Items = append(b.Items, ports.CollectedMedia{SourceID: "b", URL: "https://javdb.com/v/b", Code: "TEST-002", Title: "新影片"}, ports.CollectedMedia{SourceID: "bad"})
	if _, e = r.SaveCollection(ctx, req, b); e == nil {
		t.Fatal("invalid batch accepted")
	}
	s.SQLDB().QueryRow(`SELECT count(*) FROM media`).Scan(&n)
	if n != 1 {
		t.Fatal("partial write")
	}
}

func TestCollectionPersistsAppActorPhotoAndAliasWithoutChangingSubscription(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "actor-app.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	_, err = r.SaveCollection(ctx, ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "movie-1", Page: 1}, ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "movie-1", URL: "https://javdb.com/v/movie-1", Code: "TEST-001", Title: "测试影片", Actors: []ports.CollectedActor{{Name: "演员甲"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SQLDB().ExecContext(ctx, "UPDATE actors SET limit_date='2026-09-01' WHERE name='演员甲'"); err != nil {
		t.Fatal(err)
	}
	_, err = r.SaveCollection(ctx, ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "movie-1", Page: 1}, ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "movie-1", URL: "https://javdb.com/v/movie-1", Code: "TEST-001", Title: "测试影片", Actors: []ports.CollectedActor{{Name: "演员甲", Photo: "https://img.example/a.jpg", Aliases: []string{"演員甲"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var photo, limit string
	if err = s.SQLDB().QueryRowContext(ctx, "SELECT photo,limit_date FROM actors WHERE name='演员甲'").Scan(&photo, &limit); err != nil {
		t.Fatal(err)
	}
	if photo != "https://img.example/a.jpg" || limit != "2026-09-01" {
		t.Fatalf("photo=%q limit=%q", photo, limit)
	}
	var aliases int
	if err = s.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM actor_aliases WHERE actor_name='演员甲' AND alias='演員甲'").Scan(&aliases); err != nil || aliases != 1 {
		t.Fatalf("aliases=%d err=%v", aliases, err)
	}
}

// TestLiveJavDBActorIngestion 在隔离临时库验证真实详情采集、演员保存与幂等；不写用户业务库。
func TestLiveJavDBActorIngestion(t *testing.T) {
	if os.Getenv("BYTEMUSE_LIVE_JAVDB") != "1" {
		t.Skip("需要显式实网验证")
	}
	ctx := context.Background()
	c, err := collector.NewClientWithProxy(os.Getenv("BYTEMUSE_COLLECTION_PROXY"))
	if err != nil {
		t.Fatal(err)
	}
	req := ports.CollectionRequest{Source: "javdb", Kind: "detail", Query: "DRJeGM", Page: 1}
	batch, err := collector.NewRegistry(c).Collect(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Items) != 1 || len(batch.Items[0].Actors) == 0 {
		t.Fatal("no actors")
	}
	s, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "live-actors.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewCollectionRepository(s.SQLDB(), DialectSQLite)
	for i := 0; i < 2; i++ {
		if _, err = repo.SaveCollection(ctx, req, batch); err != nil {
			t.Fatal(err)
		}
	}
	var count, photos, subscriptions, downloads int
	s.SQLDB().QueryRow("SELECT COUNT(*),COUNT(photo) FROM actors").Scan(&count, &photos)
	s.SQLDB().QueryRow("SELECT COUNT(*) FROM actors WHERE limit_date IS NOT NULL").Scan(&subscriptions)
	s.SQLDB().QueryRow("SELECT COUNT(*) FROM download_tasks").Scan(&downloads)
	if count == 0 || photos == 0 || subscriptions != 0 || downloads != 0 {
		t.Fatalf("actors=%d photos=%d subscriptions=%d downloads=%d", count, photos, subscriptions, downloads)
	}
	t.Logf("actors=%d photos=%d subscriptions=%d downloads=%d", count, photos, subscriptions, downloads)
}

func TestCollectionMigrationUpgradesTwelveWithoutChangingHistory(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "test.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = ensureMigrationTable(ctx, s.SQLDB(), DialectSQLite); e != nil {
		t.Fatal(e)
	}
	for _, m := range MigrationPlan(DialectSQLite) {
		if m.Version <= 12 {
			if e = applyMigration(ctx, s.SQLDB(), DialectSQLite, m); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = s.SQLDB().Exec(`INSERT INTO actors(name,limit_date) VALUES('已有演员','2026-09-01')`); e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.SQLDB().QueryRow(`SELECT count(*) FROM collection_records`).Scan(&n); e != nil || n != 0 {
		t.Fatalf("%d %v", n, e)
	}
	var date string
	if e = s.SQLDB().QueryRow(`SELECT limit_date FROM actors WHERE name='已有演员'`).Scan(&date); e != nil || date != "2026-09-01" {
		t.Fatalf("%s %v", date, e)
	}
}
