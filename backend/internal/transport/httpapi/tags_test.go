package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/ports"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestTagsCatalog 验证真实库的标签去重、影片计数、字面搜索、分页和空值处理。
func TestTagsCatalog(t *testing.T) {
	ctx := context.Background()
	s, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "tags.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i, genres := range []any{" 标签甲 ,标签甲,标签乙,100%", "标签甲,标签丙", nil, " , ,"} {
		id := fmt.Sprint(i)
		if _, err = s.SQLDB().Exec("INSERT INTO media(id,code,title,subscription_status,library_status,created_at,updated_at) VALUES(?,?,'样例','none','unknown','2026-09-28','2026-09-28')", id, "TEST-"+id); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SQLDB().Exec("INSERT INTO legacy_media_metadata(media_id,code,genres,legacy_status,legacy_mode) VALUES(?,?,?,'UN_SUBSCRIBE','STRICT')", id, "TEST-"+id, genres); err != nil {
			t.Fatal(err)
		}
	}
	handler := listTags(application.NewTagService(database.NewTagRepository(s.SQLDB(), database.DialectSQLite)))
	for _, tc := range []struct {
		query               string
		status, total, size int
		name                string
		count               int
	}{
		{"", 200, 4, 4, "标签甲", 2}, {"page_size=1&page=2", 200, 4, 1, "100%", 1},
		{"search=甲", 200, 1, 1, "标签甲", 2}, {"search=%25", 200, 1, 1, "100%", 1},
		{"search=不存在", 200, 0, 0, "", 0}, {"page=99", 200, 4, 0, "", 0}, {"page=0", 400, 0, 0, "", 0}, {"page_size=201", 400, 0, 0, "", 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			out := httptest.NewRecorder()
			handler(out, httptest.NewRequest("GET", "/api/v1/tags?"+tc.query, nil))
			if out.Code != tc.status {
				t.Fatalf("status=%d body=%s", out.Code, out.Body)
			}
			if tc.status != 200 {
				return
			}
			var got application.Page[ports.Tag]
			if err = json.Unmarshal(out.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Total != tc.total || len(got.Items) != tc.size || got.Items == nil {
				t.Fatalf("result=%+v", got)
			}
			if tc.size > 0 && (got.Items[0].Name != tc.name || got.Items[0].MediaCount != tc.count) {
				t.Fatalf("item=%+v", got.Items[0])
			}
		})
	}
	out := httptest.NewRecorder()
	New(Dependencies{}).ServeHTTP(out, httptest.NewRequest("GET", "/api/v1/tags", nil))
	if out.Code != 401 {
		t.Fatalf("unauthenticated status=%d", out.Code)
	}
}
