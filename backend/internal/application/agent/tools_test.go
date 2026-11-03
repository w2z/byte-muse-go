package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// expectedToolNames 是渠道 Agent 内置工具的唯一清单，顺序即提示词展示顺序。
var expectedToolNames = []string{
	"search_code",
	"search_actor",
	"search_media",
	"query_media",
	"search_category_media",
	"get_rank",
	"get_release_today",
	"get_recommendations",
	"get_dashboard",
	"query_library",
	"search_torrents",
	"add_subscribe",
	"cancel_subscribe",
	"query_subscribes",
	"add_actor_subscribe",
	"cancel_actor_subscribe",
	"query_actor_subscribes",
	"add_tag_subscribe",
	"cancel_tag_subscribe",
	"query_tag_subscribes",
	"query_tags",
	"download_subscribe",
	"query_download_tasks",
	"query_logs",
	"query_schedulers",
	"run_cron_job",
	"query_health",
	"get_version",
	"send_message",
}

// TestBuiltinToolsExposeUniqueNames 验证工具清单完整且名称唯一。
func TestBuiltinToolsExposeUniqueNames(t *testing.T) {
	tools := BuiltinTools(Deps{})
	if len(tools) != len(expectedToolNames) {
		t.Fatalf("工具数量=%d，期望 %d", len(tools), len(expectedToolNames))
	}
	seen := make(map[string]bool, len(tools))
	for index, tool := range tools {
		if tool.Name() != expectedToolNames[index] {
			t.Fatalf("第 %d 个工具名=%q，期望 %q", index+1, tool.Name(), expectedToolNames[index])
		}
		if seen[tool.Name()] {
			t.Fatalf("工具名重复: %s", tool.Name())
		}
		seen[tool.Name()] = true
		if strings.TrimSpace(tool.Description()) == "" {
			t.Fatalf("工具 %s 缺少说明", tool.Name())
		}
	}
	registry := NewRegistry(tools...)
	if len(registry.List()) != len(expectedToolNames) {
		t.Fatalf("注册表条数=%d，期望 %d", len(registry.List()), len(expectedToolNames))
	}
	if len(registry.OpenAITools()) != len(expectedToolNames) {
		t.Fatalf("OpenAI 工具定义条数=%d，期望 %d", len(registry.OpenAITools()), len(expectedToolNames))
	}
}

// TestBuiltinToolParametersAreValidSchema 验证每个工具的参数都是合法 JSON Schema 且字段带中文说明。
func TestBuiltinToolParametersAreValidSchema(t *testing.T) {
	for _, tool := range BuiltinTools(Deps{}) {
		raw, err := json.Marshal(tool.Parameters())
		if err != nil {
			t.Fatalf("工具 %s 参数无法序列化: %v", tool.Name(), err)
		}
		var schema struct {
			Type       string                    `json:"type"`
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("工具 %s 参数不是合法 JSON: %v", tool.Name(), err)
		}
		if schema.Type != "object" {
			t.Fatalf("工具 %s 的 type=%q，期望 object", tool.Name(), schema.Type)
		}
		for name, property := range schema.Properties {
			description, _ := property["description"].(string)
			if strings.TrimSpace(description) == "" {
				t.Fatalf("工具 %s 的参数 %s 缺少中文说明", tool.Name(), name)
			}
			if property["type"] == nil {
				t.Fatalf("工具 %s 的参数 %s 缺少类型", tool.Name(), name)
			}
		}
		for _, name := range schema.Required {
			if _, ok := schema.Properties[name]; !ok {
				t.Fatalf("工具 %s 的必填参数 %s 未在 properties 中声明", tool.Name(), name)
			}
		}
	}
}

// TestBuiltinToolsTolerateEmptyDeps 验证依赖未注入时无参工具返回可读文案而不是崩溃。
func TestBuiltinToolsTolerateEmptyDeps(t *testing.T) {
	registry := NewRegistry(BuiltinTools(Deps{})...)
	ctx := context.Background()
	for _, name := range []string{
		"get_release_today", "get_dashboard", "query_subscribes", "query_actor_subscribes",
		"query_tag_subscribes", "download_subscribe", "query_schedulers", "query_health", "get_version",
	} {
		out := registry.Call(ctx, name, map[string]any{})
		if strings.TrimSpace(out) == "" || strings.Contains(out, "工具执行失败") {
			t.Fatalf("工具 %s 在空依赖下返回 %q", name, out)
		}
	}
	if out := registry.Call(ctx, "get_version", map[string]any{}); out != "dev" {
		t.Fatalf("版本回退=%q，期望 dev", out)
	}
}

// fakeDownloadRepository 是只服务测试的内存下载仓储，记录最近一次查询条件。
type fakeDownloadRepository struct {
	tasks []domain.DownloadTask
	last  ports.DownloadListQuery
}

// List 按状态过滤并模拟分页。
func (r *fakeDownloadRepository) List(_ context.Context, query ports.DownloadListQuery) (domain.DownloadPage, error) {
	r.last = query
	matched := make([]domain.DownloadTask, 0, len(r.tasks))
	for _, task := range r.tasks {
		if query.MediaID != "" && task.MediaID != query.MediaID {
			continue
		}
		if query.Status != "" && task.Status != query.Status {
			continue
		}
		matched = append(matched, task)
	}
	return domain.DownloadPage{Items: pageSlice(matched, query.Limit, query.Offset), Total: len(matched)}, nil
}

// TestQueryDownloadTasksMapsStatusAndKeyword 验证状态映射与内存关键词过滤。
func TestQueryDownloadTasksMapsStatusAndKeyword(t *testing.T) {
	code := "SSIS-001"
	repo := &fakeDownloadRepository{tasks: []domain.DownloadTask{
		{ID: "d1", Code: &code, Status: domain.DownloadStatusDownloading},
		{ID: "d2", Status: domain.DownloadStatusCompleted},
	}}
	registry := NewRegistry(BuiltinTools(Deps{Downloads: application.NewDownloadService(repo)})...)
	ctx := context.Background()

	out := registry.Call(ctx, "query_download_tasks", map[string]any{"status": "downloading"})
	if repo.last.Status != domain.DownloadStatusDownloading {
		t.Fatalf("状态过滤=%q，期望 downloading", repo.last.Status)
	}
	if !strings.Contains(out, `"status":"downloading"`) || !strings.Contains(out, `"returned":1`) {
		t.Fatalf("下载中查询结果=%s", out)
	}

	out = registry.Call(ctx, "query_download_tasks", map[string]any{"status": "未知状态"})
	if repo.last.Status != "" {
		t.Fatalf("非法状态应按 all 处理，实际 %q", repo.last.Status)
	}
	if !strings.Contains(out, `"status":"all"`) || !strings.Contains(out, `"returned":2`) {
		t.Fatalf("全部任务查询结果=%s", out)
	}

	out = registry.Call(ctx, "query_download_tasks", map[string]any{"query": "ssis 001"})
	if !strings.Contains(out, `"returned":1`) || !strings.Contains(out, `"note"`) {
		t.Fatalf("关键词过滤结果=%s", out)
	}
}

// fakeLogStore 是只服务测试的内存日志存储，记录最近一次查询条件。
type fakeLogStore struct {
	records []logging.Record
	last    logging.Query
}

// Append 在测试中不落盘。
func (s *fakeLogStore) Append(context.Context, logging.Record) error { return nil }

// Search 记录查询条件并返回预置日志。
func (s *fakeLogStore) Search(_ context.Context, query logging.Query) ([]logging.Record, int, error) {
	s.last = query
	return s.records, len(s.records), nil
}

// Clear 在测试中不删除数据。
func (s *fakeLogStore) Clear(context.Context, logging.Query) (int, error) { return 0, nil }

// DeleteBefore 在测试中不删除数据。
func (s *fakeLogStore) DeleteBefore(context.Context, time.Time) (int, error) { return 0, nil }

// TestQueryLogsValidatesLevelAndLines 验证日志级别白名单与条数上限。
func TestQueryLogsValidatesLevelAndLines(t *testing.T) {
	store := &fakeLogStore{records: []logging.Record{{
		Time:     time.Now().UTC(),
		Level:    logging.LevelError,
		Category: logging.CategoryDownload,
		Message:  "下载任务查询失败",
	}}}
	logger := logging.New(io.Discard)
	logger.SetStore(store)
	registry := NewRegistry(BuiltinTools(Deps{Logs: logger})...)
	ctx := context.Background()

	out := registry.Call(ctx, "query_logs", map[string]any{"level": "fatal", "lines": 500})
	if store.last.Level != "" {
		t.Fatalf("非法级别不应按级别过滤，实际 %q", store.last.Level)
	}
	if store.last.PageSize != 200 {
		t.Fatalf("条数上限=%d，期望 200", store.last.PageSize)
	}
	if !strings.Contains(out, `"message":"下载任务查询失败"`) {
		t.Fatalf("日志结果=%s", out)
	}

	registry.Call(ctx, "query_logs", map[string]any{"level": "error"})
	if store.last.Level != logging.LevelError {
		t.Fatalf("合法级别应透传，实际 %q", store.last.Level)
	}
	if store.last.PageSize != 80 {
		t.Fatalf("默认条数=%d，期望 80", store.last.PageSize)
	}
}

// mediaListRecorder 验证影片列表工具透传网页所用查询条件，不引入另一套仓储规则。
type mediaListRecorder struct {
	last   ports.MediaListQuery
	result domain.MediaPage
}

func (r *mediaListRecorder) List(_ context.Context, query ports.MediaListQuery) (domain.MediaPage, error) {
	r.last = query
	return r.result, nil
}

func (*mediaListRecorder) Get(context.Context, string) (domain.Media, error) {
	return domain.Media{}, ports.ErrMediaNotFound
}

// TestQueryMediaListsAndFilters 验证无关键词列表、分页与全部筛选均复用 CatalogService。
func TestQueryMediaListsAndFilters(t *testing.T) {
	repo := &mediaListRecorder{result: domain.MediaPage{Items: []domain.Media{{ID: "m1", Code: "TEST-001", Title: "测试影片"}}, Total: 23}}
	registry := NewRegistry(BuiltinTools(Deps{Catalog: application.NewCatalogService(repo)})...)
	tool, ok := registry.Get("query_media")
	if !ok {
		t.Fatal("缺少影片列表工具 query_media")
	}
	result, err := tool.Run(context.Background(), map[string]any{"page": 2, "page_size": 3, "search": " TEST ", "subscription": "none", "download": "unknown", "library": "absent", "video_type": "unknown", "vr": "hide"})
	if err != nil {
		t.Fatal(err)
	}
	want := ports.MediaListQuery{Limit: 3, Offset: 3, Search: "TEST", SubscriptionStatus: "none", DownloadStatus: "unknown", LibraryStatus: "absent", VideoType: "unknown", VR: "hide"}
	if repo.last != want {
		t.Fatalf("查询条件=%+v want=%+v", repo.last, want)
	}
	data := result.(map[string]any)
	if data["page"] != 2 || data["page_size"] != 3 || data["total"] != 23 || data["has_more"] != true || data["returned"] != 1 {
		t.Fatalf("分页结果=%v", data)
	}
	items := data["items"].([]map[string]any)
	if items[0]["id"] != "m1" || items[0]["code"] != "TEST-001" {
		t.Fatalf("影片结果=%v", items)
	}
	repo.result = domain.MediaPage{}
	result, err = tool.Run(context.Background(), nil)
	if err != nil || repo.last.Limit != 20 || repo.last.Offset != 0 || repo.last.Search != "" {
		t.Fatalf("空条件无法查询: %v %v %+v", result, err, repo.last)
	}
	data = result.(map[string]any)
	if data["total"] != 0 || data["has_more"] != false || len(data["items"].([]map[string]any)) != 0 {
		t.Fatalf("空列表=%v", data)
	}
	for _, args := range []map[string]any{{"video_type": "bad"}, {"vr": "bad"}} {
		if _, err := tool.Run(context.Background(), args); err == nil {
			t.Fatalf("非法筛选未拒绝: %v", args)
		}
	}
	out := NewRegistry(BuiltinTools(Deps{})...).Call(context.Background(), "query_media", nil)
	if !strings.Contains(out, "影片列表服务不可用") {
		t.Fatalf("空服务=%s", out)
	}
}
