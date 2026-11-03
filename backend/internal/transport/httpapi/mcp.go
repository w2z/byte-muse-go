package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/application/agent"
	"bytemuse/backend/internal/logging"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewMCPHandler 将唯一工具目录适配为无会话 Streamable HTTP 服务；每个请求重新读取开关与专用令牌。
// 它不接受后台 Cookie，不暴露依赖聊天上下文的 send_message；业务操作仍复用原应用服务。
func NewMCPHandler(settings *application.SettingsService, registry *agent.Registry, version string) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "ByteMuse", Version: version}, nil)
	for _, tool := range registry.List() {
		if tool.Name() == "send_message" {
			continue
		}
		mcp.AddTool[map[string]any, any](server, &mcp.Tool{
			Name: tool.Name(), Description: tool.Description(), InputSchema: tool.Parameters(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			logging.Info(logging.CategoryAgent, "MCP 工具调用", "tool", tool.Name())
			result, err := tool.Run(ctx, args)
			if err != nil {
				return nil, nil, err
			}
			text, ok := result.(string)
			if !ok {
				raw, err := json.Marshal(result)
				if err != nil {
					return nil, nil, err
				}
				text = string(raw)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
		})
	}
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1024 * 1024, PropagateRequestCancellation: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if settings == nil || registry == nil {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "MCP 服务未就绪")
			return
		}
		current, err := settings.Get(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "MCP 设置读取失败")
			return
		}
		if current.Values["MCP_ENABLE"] != "true" {
			http.NotFound(w, r)
			return
		}
		// 拒绝跨源浏览器请求；显式外网地址可适配保留内部 Host 的反向代理。
		if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			external, _ := url.Parse(current.Values["EXTERNAL_DOMAIN"])
			valid := err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == "" && parsed.Host != ""
			if !valid || (!strings.EqualFold(parsed.Host, r.Host) && (external == nil || !strings.EqualFold(origin, external.Scheme+"://"+external.Host))) {
				writeError(w, http.StatusForbidden, "forbidden_origin", "MCP 不允许此请求来源")
				return
			}
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		provided := ""
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			provided = parts[1]
		}
		want, got := sha256.Sum256([]byte(current.Values["MCP_TOKEN"])), sha256.Sum256([]byte(provided))
		if provided == "" || current.Values["MCP_TOKEN"] == "" || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ByteMuse MCP"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "MCP 访问令牌无效")
			return
		}
		transport.ServeHTTP(w, r)
	})
}
