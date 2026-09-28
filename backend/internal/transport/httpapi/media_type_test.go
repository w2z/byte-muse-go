package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/database"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestMediaTypeFilter 验证真实存储、HTTP 参数、分页总数、组合筛选及未知类型拒绝。
func TestMediaTypeFilter(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "types.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i, typ := range []any{"censored", "uncensored", "uncensored_cracked", "leaked", nil, "censored"} {
		_, err = store.SQLDB().Exec(`INSERT INTO media(id,code,title,video_type,subscription_status,library_status,created_at,updated_at) VALUES(?,?,?,?,'none','unknown','2026-09-28T00:00:00Z','2026-09-28T00:00:00Z')`, fmt.Sprint(i), fmt.Sprintf("TEST-%d", i), "测试影片", typ)
		if err != nil {
			t.Fatal(err)
		}
	}
	handler := listMedia(application.NewCatalogService(store.Media()))
	for _, tc := range []struct {
		query               string
		total, size, status int
		typ                 any
	}{
		{"video_type=censored&page_size=1&page=2", 2, 1, 200, "censored"},
		{"video_type=uncensored", 1, 1, 200, "uncensored"},
		{"video_type=uncensored_cracked", 1, 1, 200, "uncensored_cracked"},
		{"video_type=leaked&search=TEST-3&subscription=none&library=unknown", 1, 1, 200, "leaked"},
		{"video_type=unknown", 1, 1, 200, nil},
		{"", 6, 6, 200, nil},
		{"video_type=censored&search=TEST-3", 0, 0, 200, nil},
		{"video_type=invalid", 0, 0, 400, nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequest("GET", "/api/v1/media?"+tc.query, nil))
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body)
			}
			if tc.status != 200 {
				return
			}
			var result struct {
				Total int              `json:"total"`
				Items []map[string]any `json:"items"`
			}
			if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Total != tc.total || len(result.Items) != tc.size {
				t.Fatalf("result %+v", result)
			}
			if tc.query != "" {
				for _, item := range result.Items {
					value, present := item["video_type"]
					if !present || value != tc.typ {
						t.Fatalf("video_type: %#v", item)
					}
				}
			}
		})
	}
	if _, err = store.SQLDB().Exec(`UPDATE media SET video_type='invalid' WHERE id='0'`); err == nil {
		t.Fatal("database accepted invalid type")
	}
}
