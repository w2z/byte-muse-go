package httpapi

import (
	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/database"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestCatalogViewFilters 验证三个入口在服务端组合筛选、分页及统计，推荐允许选择已订阅影片。
func TestCatalogViewFilters(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "views.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i, typ := range []any{"censored", "censored", "uncensored", nil} {
		id := fmt.Sprint(i)
		status := "none"
		if i < 2 {
			status = "active"
		}
		if _, err = store.SQLDB().Exec(`INSERT INTO media(id,code,title,video_type,subscription_status,library_status,release_date,created_at,updated_at) VALUES(?,?,?,?,?,'unknown',?,'2026-10-04T00:00:00Z','2026-10-04T00:00:00Z')`, id, "TEST-"+id, "测试", typ, status, time.Now().Format("2006-01-02")); err != nil {
			t.Fatal(err)
		}
		if _, err = store.SQLDB().Exec(`INSERT INTO legacy_media_metadata(media_id,code,genres,legacy_status,legacy_mode) VALUES(?,?,'共同标签',?,'strict')`, id, "TEST-"+id, map[bool]string{true: "SUBSCRIBE", false: "UN_SUBSCRIBE"}[i < 2]); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if _, err = store.SQLDB().Exec(`INSERT INTO subscriptions(id,media_id,status,mode,filter_json,idempotency_key,idempotency_hash,created_at,updated_at,version) VALUES(?,?,'active','strict','{}',?,?,'2026-10-04T00:00:00Z','2026-10-04T00:00:00Z',1)`, id, id, id, id); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = store.SQLDB().Exec(`INSERT INTO rank_entries(rank_type,position,code) VALUES('daily',?,?)`, i+1, "TEST-"+id); err != nil {
			t.Fatal(err)
		}
	}
	service := application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.DialectSQLite))
	// VR 沿用番号识别，验证大小写与分页前筛选；不改变元数据或订阅状态。
	for _, table := range []string{"media", "legacy_media_metadata", "rank_entries"} {
		if _, err = store.SQLDB().Exec("UPDATE " + table + " SET code='TEST-vr-0' WHERE code='TEST-0'"); err != nil {
			t.Fatal(err)
		}
		if _, err = store.SQLDB().Exec("UPDATE " + table + " SET code='TEST-VR-2' WHERE code='TEST-2'"); err != nil {
			t.Fatal(err)
		}
	}
	for name, handler := range map[string]http.HandlerFunc{"rank": listRank(service), "release": listReleaseToday(service), "recommend": listRecommendations(service)} {
		for _, tc := range []struct {
			query               string
			total, size, status int
		}{
			{"subscription=active&video_type=censored&page_size=1&page=2", 2, 1, 200},
			{"subscription=none&video_type=unknown", 1, 1, 200},
			{"subscription=active&video_type=uncensored", 0, 0, 200},
			{"", 4, 4, 200}, {"video_type=invalid", 0, 0, 400},
			{"vr=only&page_size=1&page=2", 2, 1, 200},
			{"vr=hide&page_size=1&page=2", 2, 1, 200},
			{"vr=only&subscription=active&video_type=censored", 1, 1, 200},
			{"vr=hide&subscription=none&video_type=unknown", 1, 1, 200},
			{"vr=only&video_type=unknown", 0, 0, 200},
			{"vr=invalid", 0, 0, 400},
		} {
			t.Run(name+tc.query, func(t *testing.T) {
				response := httptest.NewRecorder()
				handler(response, httptest.NewRequest("GET", "/?type=daily&"+tc.query, nil))
				if response.Code != tc.status {
					t.Fatalf("status=%d body=%s", response.Code, response.Body)
				}
				if tc.status != 200 {
					return
				}
				var result struct {
					Total int
					Items []struct {
						Code         string
						VideoType    *string `json:"video_type"`
						Subscription string  `json:"subscription_status"`
					}
				}
				if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Total != tc.total || len(result.Items) != tc.size {
					t.Fatalf("result=%s", response.Body)
				}
				for _, item := range result.Items {
					isVR := strings.Contains(strings.ToUpper(item.Code), "VR")
					if strings.Contains(tc.query, "vr=only") && !isVR || strings.Contains(tc.query, "vr=hide") && isVR {
						t.Fatalf("VR filter leaked item: %s", response.Body)
					}
				}
			})
		}
	}
}
