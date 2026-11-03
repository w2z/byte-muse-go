package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/application/agent"
	"bytemuse/backend/internal/platform/database"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPHTTP 验证真实设置落库、协议握手、工具目录和执行，以及关闭和令牌轮换即时拒绝后续请求。
func TestMCPHTTP(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "mcp.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err := application.NewSettingsService(database.NewSettingsRepository(store.SQLDB(), database.DialectSQLite), "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "mcp-media", "TEST-001", "MCP test film", "none", "absent", now, now); err != nil {
		t.Fatal(err)
	}
	registry := agent.NewRegistry(agent.BuiltinTools(agent.Deps{
		Version:               "test-mcp",
		Queries:               application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.DialectSQLite)),
		Subscriptions:         application.NewSubscriptionService(store.Subscriptions()),
		SubscriptionDownloads: application.NewSubscriptionDownloadService(database.NewSubscriptionDownloadRepository(store.SQLDB(), database.DialectSQLite), nil, nil, nil),
	})...)
	h := New(Dependencies{MCP: NewMCPHandler(settings, registry, "test-mcp")})
	request := func(body, token, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "http://localhost/api/v1/mcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if w := request(list, "", ""); w.Code != 404 {
		t.Fatalf("默认未关闭: %d %s", w.Code, w.Body)
	}
	saved, err := settings.Update(ctx, map[string]string{"MCP_ENABLE": "true"})
	if err != nil {
		t.Fatal(err)
	}
	token := saved.Values["MCP_TOKEN"]
	for _, bad := range []string{"", "wrong"} {
		if w := request(list, bad, ""); w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("未拒绝错误令牌: %d", w.Code)
		}
	}
	if w := request(list, token, "https://untrusted.example"); w.Code != 403 {
		t.Fatalf("未拒绝跨域: %d", w.Code)
	}
	w := request(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "test-mcp") {
		t.Fatalf("握手失败: %d %s", w.Code, w.Body)
	}
	w = request(list, token, "http://localhost")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "add_subscribe") || strings.Contains(w.Body.String(), `"name":"send_message"`) {
		t.Fatalf("目录错误: %d %s", w.Code, w.Body)
	}
	w = request(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_version","arguments":{}}}`, token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "test-mcp") {
		t.Fatalf("执行失败: %d %s", w.Code, w.Body)
	}
	for _, args := range []string{`{}`, `{"codes":123}`} {
		w = request(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add_subscribe","arguments":`+args+`}}`, token, "")
		var result struct {
			Result struct {
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Result.IsError {
			t.Fatalf("参数未验证: %s", w.Body)
		}
	}
	// 官方客户端通过真实 HTTP 验证当前协议协商，实际查询、重复订阅和取消复用同一个业务仓储。
	server := httptest.NewServer(h)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-integration-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: mcpTestBearerTransport{token: token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) == 0 {
		t.Fatal("官方客户端未取得工具目录", err)
	}
	for _, args := range []map[string]any{{"tag": "MCP-test-tag"}, {"tag": "MCP-test-tag", "video_type": ""}} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_category_media", Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("按标签查询不接受空影片类型: result=%v err=%v", result, err)
		}
	}
	for _, tc := range []struct {
		method, accept, contentType string
		want                        int
	}{
		{"POST", "application/json", "application/json", 400},
		{"POST", "application/json, text/event-stream", "text/plain", 415},
		{"GET", "application/json, text/event-stream", "application/json", 405},
		{"DELETE", "application/json, text/event-stream", "application/json", 405},
	} {
		r := httptest.NewRequest(tc.method, "http://localhost/api/v1/mcp", strings.NewReader(list))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Accept", tc.accept)
		r.Header.Set("Content-Type", tc.contentType)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != tc.want {
			t.Fatalf("%s Accept=%s Content-Type=%s: status=%d body=%s", tc.method, tc.accept, tc.contentType, out.Code, out.Body)
		}
	}
	for i := 0; i < 2; i++ {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "add_subscribe", Arguments: map[string]any{"codes": "TEST-001"}})
		if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "订阅") {
			t.Fatalf("订阅失败: %v %v", result, err)
		}
	}
	var subscriptions, scans int
	if err := store.SQLDB().QueryRow("SELECT COUNT(*) FROM subscriptions WHERE media_id='mcp-media' AND status='active'").Scan(&subscriptions); err != nil || subscriptions != 1 {
		t.Fatalf("重复订阅=%d err=%v", subscriptions, err)
	}
	if err := store.SQLDB().QueryRow("SELECT COUNT(*) FROM subscription_scans").Scan(&scans); err != nil || scans != 1 {
		t.Fatalf("重复下载登记=%d err=%v", scans, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "query_subscribes"})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "TEST-001") {
		t.Fatalf("查询失败: %v %v", result, err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "cancel_subscribe", Arguments: map[string]any{"code": "TEST-001"}})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "已取消") {
		t.Fatalf("取消失败: %v %v", result, err)
	}
	if err := store.SQLDB().QueryRow("SELECT COUNT(*) FROM subscriptions WHERE status='active'").Scan(&subscriptions); err != nil || subscriptions != 0 {
		t.Fatalf("取消未落库=%d err=%v", subscriptions, err)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "unknown_tool"}); err == nil {
		t.Fatal("未知工具未返回协议错误")
	}
	if _, err := settings.Update(ctx, map[string]string{"MCP_TOKEN": strings.Repeat("z", 40)}); err != nil {
		t.Fatal(err)
	}
	if w := request(list, token, ""); w.Code != 401 {
		t.Fatalf("旧令牌未失效: %d", w.Code)
	}
	if _, err := settings.Update(ctx, map[string]string{"MCP_ENABLE": "false"}); err != nil {
		t.Fatal(err)
	}
	if w := request(list, strings.Repeat("z", 40), ""); w.Code != 404 {
		t.Fatalf("关闭未生效: %d", w.Code)
	}
	// MCP 令牌仅用于 MCP，不可读取后台全部设置。
	r := httptest.NewRequest("GET", "/api/v1/system/settings", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != http.StatusUnauthorized {
		t.Fatalf("令牌越权: %d", out.Code)
	}
}

// mcpTestBearerTransport 只给隔离测试服务器注入认证头，避免在 URL 或日志暴露凭据。
type mcpTestBearerTransport struct{ token string }

func (transport mcpTestBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(cloned)
}
