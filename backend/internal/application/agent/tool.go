// Package agent 实现外部消息渠道（Telegram / 企业微信）共用的对话 Agent：
// 斜杠命令分流、OpenAI function calling 编排，以及面向业务能力的工具集。
package agent

import (
	"context"
	"encoding/json"
	"strings"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
	"bytemuse/backend/internal/scheduler"
)

// toolResultMaxChars 是单个工具回填给模型的字符上限。
// 超出后只回传预览并要求模型收窄条件，避免上下文被单次查询撑爆。
const toolResultMaxChars = 16 * 1024

// Tool 是一项 Agent 可调用的能力。
// 参数名与 JSON Schema 必须与 OpenAI function calling 契约一致；
// 实现不得直接访问数据库，只能调用 Deps 中已有的应用服务。
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Run(ctx context.Context, args map[string]any) (any, error)
}

// Deps 汇总工具可用的业务能力，全部来自既有应用服务，保证与 HTTP 入口复用同一套业务规则。
type Deps struct {
	Catalog               *application.CatalogService
	Queries               *application.CatalogQueryService
	Actors                *application.ActorService
	Tags                  *application.TagService
	Subscriptions         *application.SubscriptionService
	SubscriptionDownloads *application.SubscriptionDownloadService
	Downloads             *application.DownloadService
	Dashboard             *application.DashboardService
	Scheduler             *scheduler.Manager
	Logs                  *logging.Logger
	// Torrents 复用订阅下载链路的资源搜索器，Agent 不另建搜种实现。
	Torrents application.ResourceSearcher
	// Version 是当前运行版本，仅用于版本类回答。
	Version string
	// Channels 按渠道名解析发送器，供 send_message 等主动回复类工具使用。
	Channels ChannelResolver
	// Notifier 提供渠道通知开关，用于按渠道决定 Agent 对话是否回复。
	Notifier application.Notifier
}

// ChannelResolver 按渠道名解析发送器；与 ports.ChannelResolver 共用同一份定义，不重复声明。
type ChannelResolver = ports.ChannelResolver

// Registry 是工具的唯一目录；编排器、斜杠命令和未来的 MCP 暴露共用同一份定义。
type Registry struct {
	tools map[string]Tool
	order []string
}

// NewRegistry 按传入顺序登记工具；重名视为编码错误，后者不覆盖前者。
func NewRegistry(tools ...Tool) *Registry {
	registry := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := tool.Name()
		if name == "" {
			continue
		}
		if _, exists := registry.tools[name]; exists {
			continue
		}
		registry.tools[name] = tool
		registry.order = append(registry.order, name)
	}
	return registry
}

// Get 返回指定工具。
func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	tool, ok := r.tools[name]
	return tool, ok
}

// List 按登记顺序返回全部工具。
func (r *Registry) List() []Tool {
	if r == nil {
		return nil
	}
	items := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		items = append(items, r.tools[name])
	}
	return items
}

// OpenAITools 生成 chat/completions 的 tools 字段。
func (r *Registry) OpenAITools() []map[string]any {
	tools := r.List()
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name(),
				"description": tool.Description(),
				"parameters":  tool.Parameters(),
			},
		})
	}
	return out
}

// Call 执行工具并把结果序列化为回填模型的字符串；任何失败都转成可读文本，不中断整轮对话。
func (r *Registry) Call(ctx context.Context, name string, args map[string]any) string {
	tool, ok := r.Get(name)
	if !ok {
		return "未知工具: " + name
	}
	if args == nil {
		args = map[string]any{}
	}
	logging.Info(logging.CategoryAgent, "Agent 工具调用", "tool", name)
	result, err := tool.Run(ctx, args)
	if err != nil {
		logging.Error(logging.CategoryAgent, "Agent 工具执行失败", "tool", name, "error", err.Error())
		return "工具执行失败: " + err.Error()
	}
	return formatToolResult(result, name)
}

// formatToolResult 统一序列化工具结果并施加长度上限。
func formatToolResult(result any, name string) string {
	if text, ok := result.(string); ok {
		return truncateToolResult(text, name)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return truncateToolResult(err.Error(), name)
	}
	return truncateToolResult(string(raw), name)
}

func truncateToolResult(text, name string) string {
	if len(text) <= toolResultMaxChars {
		return text
	}
	preview := map[string]any{
		"tool_result_truncated": true,
		"tool_name":             name,
		"total_chars":           len(text),
		"content_preview":       text[:toolResultMaxChars],
		"message":               "工具返回内容过长，已截断为预览。请用更精确的条件继续查询。",
	}
	raw, err := json.Marshal(preview)
	if err != nil {
		return text[:toolResultMaxChars]
	}
	return string(raw)
}

// stringArg 读取字符串参数；兼容模型偶发传入数字或布尔值的情况。
func stringArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strings.TrimSpace(json.Number(formatFloat(typed)).String())
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// intArg 读取整数参数并夹在 [min, max] 内；缺省或非法时返回 fallback。
func intArg(args map[string]any, key string, fallback, min, max int) int {
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	number := fallback
	switch typed := value.(type) {
	case float64:
		number = int(typed)
	case int:
		number = typed
	case string:
		parsed, err := json.Number(strings.TrimSpace(typed)).Int64()
		if err != nil {
			return fallback
		}
		number = int(parsed)
	default:
		return fallback
	}
	if number < min {
		return min
	}
	if number > max {
		return max
	}
	return number
}

func formatFloat(value float64) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "0"
	}
	return string(raw)
}
