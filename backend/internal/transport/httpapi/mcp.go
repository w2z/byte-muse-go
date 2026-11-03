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
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPServer 为本次请求构造不可变的授权工具目录，未授权工具不会登记为可调用方法。
func newMCPServer(registry *agent.Registry, allowed map[string]bool, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ByteMuse", Version: version}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	for _, tool := range registry.MCPTools() {
		if !allowed[tool.Name()] {
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
	return server
}

// mcpPermissionsKey 只携带本次已鉴权请求的权限快照，避免无会话请求之间共享可变工具目录。
type mcpPermissionsKey struct{}

// NewMCPHandler 对每个请求重新读取令牌与白名单；只登记被明确授权的工具，目录与调用同时收紧。
func NewMCPHandler(settings *application.SettingsService, registry *agent.Registry, version string) http.Handler {
	transport := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		allowed, _ := r.Context().Value(mcpPermissionsKey{}).(map[string]bool)
		return newMCPServer(registry, allowed, version)
	}, &mcp.StreamableHTTPOptions{
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
		names, err := application.MCPAllowedTools(current.Values["MCP_ALLOWED_TOOLS"])
		if err != nil {
			names = nil
		}
		allowed := make(map[string]bool, len(names))
		for _, name := range names {
			allowed[name] = true
		}
		transport.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpPermissionsKey{}, allowed)))
	})
}

// mcpToolDescriptor 来自唯一工具目录，不包含令牌或用户数据，供管理员逐项勾选授权。
type mcpToolDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// settingsWithMCPTools 在设置响应附带可授权目录，避免前端另行维护工具名称与业务说明。
func settingsWithMCPTools(settings domain.SystemSettings, registry *agent.Registry) any {
	items := []mcpToolDescriptor{}
	for _, tool := range registry.MCPTools() {
		items = append(items, mcpToolDescriptor{Name: tool.Name(), Description: tool.Description()})
	}
	return struct {
		domain.SystemSettings
		MCPTools []mcpToolDescriptor `json:"mcp_tools"`
	}{settings, items}
}
