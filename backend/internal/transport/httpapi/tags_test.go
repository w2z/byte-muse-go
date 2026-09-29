package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/ports"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
		{"", 200, 309, 15, "标签甲", 2}, {"page_size=1&page=2", 200, 309, 1, "100%", 1},
		{"search=甲", 200, 1, 1, "标签甲", 2}, {"search=%25", 200, 1, 1, "100%", 1},
		{"search=不存在", 200, 0, 0, "", 0}, {"page=99", 200, 309, 0, "", 0}, {"page=0", 400, 0, 0, "", 0}, {"page_size=501", 400, 0, 0, "", 0},
		{"subscription=all&page_size=100", 200, 309, 100, "标签甲", 2},
		{"subscription=all&page_size=200", 200, 309, 200, "标签甲", 2},
		{"subscription=all&page_size=300", 200, 309, 300, "标签甲", 2},
		{"subscription=all&page_size=400", 200, 309, 309, "标签甲", 2},
		{"subscription=all&page_size=500", 200, 309, 309, "标签甲", 2},
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

// TestTagSubscriptionHTTP 覆盖真实鉴权、日期校验、重复保存、编辑、取消、分类和搜索。
func TestTagSubscriptionHTTP(t *testing.T) {
	ctx := context.Background()
	s, e := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "api.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	a, e := auth.New(auth.Config{Username: "test-admin", Password: "test-password", Secret: strings.Repeat("x", 32)})
	if e != nil {
		t.Fatal(e)
	}
	h := New(Dependencies{Auth: a, Tags: application.NewTagService(database.NewTagRepository(s.SQLDB(), database.DialectSQLite)), CatalogQueries: application.NewCatalogQueryService(database.NewCatalogQueryRepository(s.SQLDB(), database.DialectSQLite))})
	for _, method := range []string{"PUT", "DELETE"} {
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest(method, "/api/v1/tags/制服/subscription", nil))
		if out.Code != 401 {
			t.Fatalf("unauth=%d", out.Code)
		}
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"test-admin","password":"test-password"}`)))
	if login.Code != 200 {
		t.Fatal(login.Body)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"PUT", "/tags/制服/subscription", `{"limit_date":"2026-02-30"}`, 400},
		{"PUT", "/tags/制服/subscription", `{"limit_date":""}`, 400},
		{"PUT", "/tags/不存在/subscription", `{"limit_date":"2026-09-28"}`, 404},
		{"PUT", "/tags/制服/subscription", `{"limit_date":"2026-09-28"}`, 200},
		{"PUT", "/tags/制服/subscription", `{"limit_date":"2026-09-28"}`, 200},
		{"PUT", "/tags/制服/subscription", `{"limit_date":"2026-09-27"}`, 200},
		{"GET", "/tags?subscription=active&category=服装", "", 200},
		{"GET", "/tags?subscription=invalid", "", 400},
		{"GET", "/tags?category=invalid", "", 400},
		{"GET", "/complex/search?tag=制服", "", 200},
		{"DELETE", "/tags/制服/subscription", "", 200},
		{"DELETE", "/tags/制服/subscription", "", 200},
		{"GET", "/tags?subscription=active", "", 200},
	} {
		out := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, "/api/v1"+tc.path, strings.NewReader(tc.body))
		req.AddCookie(login.Result().Cookies()[0])
		h.ServeHTTP(out, req)
		if out.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, out.Code, out.Body)
		}
		if tc.method == "PUT" && tc.status == 200 {
			var v ports.Tag
			json.Unmarshal(out.Body.Bytes(), &v)
			if v.Category != "服装" || v.LimitDate == nil {
				t.Fatal(out.Body)
			}
		}
		if tc.path == "/tags?subscription=active&category=服装" {
			var v application.Page[ports.Tag]
			json.Unmarshal(out.Body.Bytes(), &v)
			if v.Total != 1 || v.Items[0].LimitDate == nil || *v.Items[0].LimitDate != "2026-09-27" {
				t.Fatal(out.Body)
			}
		}
		if tc.path == "/tags?subscription=active" {
			var v application.Page[ports.Tag]
			json.Unmarshal(out.Body.Bytes(), &v)
			if v.Total != 0 || len(v.Items) != 0 {
				t.Fatal(out.Body)
			}
		}
	}
}
