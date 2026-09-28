package database

import (
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestMediaTypeUpgradeAndCollection 确认旧库无损升级、空类型及逐影片采集保存规则。
func TestMediaTypeUpgradeAndCollection(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "upgrade.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	for _, m := range MigrationPlan(DialectSQLite) {
		if m.Version <= 14 {
			if err = applyMigration(ctx, store.SQLDB(), DialectSQLite, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = store.SQLDB().Exec(`INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES('old','OLD-001','旧影片','active','present','2026-09-28T00:00:00Z','2026-09-28T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = store.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var typ sql.NullString
	var subscription, library string
	if err = store.SQLDB().QueryRow(`SELECT video_type,subscription_status,library_status FROM media WHERE id='old'`).Scan(&typ, &subscription, &library); err != nil || typ.Valid || subscription != "active" || library != "present" {
		t.Fatalf("historical state changed: %v %s %s %v", typ, subscription, library, err)
	}
	repo := NewCollectionRepository(store.SQLDB(), DialectSQLite)
	old, err := store.Media().Get(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	known := "leaked"
	old.VideoType = &known
	mediaRepo := &sqlMediaRepository{dialect: DialectSQLite, exec: store.SQLDB()}
	if err = mediaRepo.upsert(ctx, old); err != nil {
		t.Fatal(err)
	}
	loaded, err := mediaRepo.Get(ctx, "old")
	if err != nil || loaded.VideoType == nil || *loaded.VideoType != known {
		t.Fatalf("upsert lost type: %+v %v", loaded.VideoType, err)
	}
	for _, value := range []string{"censored", "uncensored", "uncensored_cracked", "leaked", "invalid"} {
		var item ports.CollectedMedia
		if err = json.Unmarshal([]byte(`{"source_id":"`+value+`","url":"https://example.test/video","code":"`+value+`","title":"测试影片","video_type":"`+value+`"}`), &item); err != nil {
			t.Fatal(err)
		}
		_, err = repo.SaveCollection(ctx, ports.CollectionRequest{Source: "netflav", Kind: "search"}, ports.CollectionBatch{Items: []ports.CollectedMedia{item}})
		if value == "invalid" {
			if err == nil {
				t.Fatal("invalid type accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SQLDB().QueryRow(`SELECT video_type FROM media WHERE LOWER(code)=?`, value).Scan(&typ); err != nil || !typ.Valid || typ.String != value {
			t.Fatalf("type %s: %v %v", value, typ, err)
		}
		// 重复来源不覆盖已存在的影片分类。
		itemJSON := `"video_type":"censored"`
		if err = json.Unmarshal([]byte(`{`+itemJSON+`}`), &item); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.SaveCollection(ctx, ports.CollectionRequest{Source: "netflav", Kind: "search"}, ports.CollectionBatch{Items: []ports.CollectedMedia{item}}); err != nil {
			t.Fatal(err)
		}
		if err = store.SQLDB().QueryRow(`SELECT video_type FROM media WHERE LOWER(code)=?`, value).Scan(&typ); err != nil || typ.String != value {
			t.Fatalf("type overwritten: %v %v", typ, err)
		}
	}
}
