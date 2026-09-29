package agent

import (
	"context"
	"strings"
	"time"
)

// BuiltinTools 返回渠道 Agent 的全部内置工具，顺序即提示词中的展示顺序。
// 工具只包装 Deps 中已有的应用服务，不直接访问数据库，也不复制订阅、下载等业务规则；
// 工具名与 prompt.go 的提示词约定一一对应，改动名称必须同步提示词。
func BuiltinTools(deps Deps) []Tool {
	return []Tool{
		builtinTool{
			name:        "search_code",
			description: "按番号精确查询一部影片的本地资料，含标题、发行日期、封面、订阅与媒体库状态。",
			parameters: objectSchema(map[string]any{
				"code": stringProperty("影片番号，例如 SSIS-001"),
			}, "code"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchCode(ctx, deps, stringArg(args, "code"))
			},
		},
		builtinTool{
			name:        "search_actor",
			description: "按名称查询演员及其订阅截止日期；名称为空时列出前 20 位演员。",
			parameters: objectSchema(map[string]any{
				"name": stringProperty("演员名称或名称片段，可为空"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchActor(ctx, deps, stringArg(args, "name"))
			},
		},
		builtinTool{
			name:        "search_media",
			description: "按关键词搜索本地影片库，返回最多 20 条精简影片信息。",
			parameters: objectSchema(map[string]any{
				"keyword": stringProperty("搜索关键词，可以是番号、原标题片段或演员名"),
			}, "keyword"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchMedia(ctx, deps, stringArg(args, "keyword"))
			},
		},
		builtinTool{
			name:        "get_rank",
			description: "查询榜单影片，返回最多 20 条；type 支持 daily、weekly、monthly。",
			parameters: objectSchema(map[string]any{
				"type": stringProperty("榜单周期：daily 日榜、weekly 周榜、monthly 月榜；缺省或非法值按 monthly 处理", "daily", "weekly", "monthly"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return getRank(ctx, deps, stringArg(args, "type"))
			},
		},
		builtinTool{
			name:        "get_release_today",
			description: "查询今天发行的影片，返回最多 20 条。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return getReleaseToday(ctx, deps)
			},
		},
		builtinTool{
			name:        "get_recommendations",
			description: "根据已有订阅与媒体库画像推荐未订阅影片，默认 10 条，最多 20 条。",
			parameters: objectSchema(map[string]any{
				"limit": integerProperty("返回条数，默认 10，取值范围 1-20"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return getRecommendations(ctx, deps, intArg(args, "limit", 10, 1, 20))
			},
		},
		builtinTool{
			name:        "get_dashboard",
			description: "查询运营概览：影片总数、有效订阅数和已完成下载数。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return getDashboard(ctx, deps)
			},
		},
		builtinTool{
			name:        "query_library",
			description: "判断某个番号是否已经在媒体库中。",
			parameters: objectSchema(map[string]any{
				"code": stringProperty("影片番号，例如 SSIS-001"),
			}, "code"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryLibrary(ctx, deps, stringArg(args, "code"))
			},
		},
		builtinTool{
			name:        "search_torrents",
			description: "在已配置的资源站搜索种子候选，返回最多 10 条，含站点、大小、做种数与免费标记。",
			parameters: objectSchema(map[string]any{
				"query": stringProperty("番号或搜索关键词"),
			}, "query"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchTorrents(ctx, deps, stringArg(args, "query"))
			},
		},
		builtinTool{
			name:        "add_subscribe",
			description: "按番号创建影片订阅；多个番号用逗号分隔，逐个受理并返回每一条结果。",
			parameters: objectSchema(map[string]any{
				"codes": stringProperty("一个或多个番号，用英文逗号或中文逗号分隔，例如 SSIS-001,SSIS-002"),
			}, "codes"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return addSubscribe(ctx, deps, stringArg(args, "codes"))
			},
		},
		builtinTool{
			name:        "cancel_subscribe",
			description: "取消某个番号的有效影片订阅；没有有效订阅时如实说明。",
			parameters: objectSchema(map[string]any{
				"code": stringProperty("要取消订阅的影片番号，例如 SSIS-001"),
			}, "code"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return cancelSubscribe(ctx, deps, stringArg(args, "code"))
			},
		},
		builtinTool{
			name:        "query_subscribes",
			description: "列出当前有效影片订阅，最多 50 条。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return querySubscribes(ctx, deps)
			},
		},
		builtinTool{
			name:        "add_actor_subscribe",
			description: "订阅一位演员的后续作品；limit_date 为空时从当天开始追新。",
			parameters: objectSchema(map[string]any{
				"name":       stringProperty("演员名称"),
				"limit_date": stringProperty("追新起始日期，格式 YYYY-MM-DD；为空表示当天"),
			}, "name"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return addActorSubscribe(ctx, deps, stringArg(args, "name"), stringArg(args, "limit_date"))
			},
		},
		builtinTool{
			name:        "cancel_actor_subscribe",
			description: "取消演员订阅，仅清除追新起始日期，不删除演员资料。",
			parameters: objectSchema(map[string]any{
				"name": stringProperty("演员名称"),
			}, "name"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return cancelActorSubscribe(ctx, deps, stringArg(args, "name"))
			},
		},
		builtinTool{
			name:        "query_actor_subscribes",
			description: "列出当前已订阅的演员，最多 50 条。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return queryActorSubscribes(ctx, deps)
			},
		},
		builtinTool{
			name:        "add_tag_subscribe",
			description: "订阅一个标签并立即追新本地影片；limit_date 为空时从当天开始。订阅前先用 query_tags 确认标签名。",
			parameters: objectSchema(map[string]any{
				"name":       stringProperty("标签名称，必须与标签目录中的名称完全一致"),
				"limit_date": stringProperty("追新起始日期，格式 YYYY-MM-DD；为空表示当天"),
			}, "name"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return addTagSubscribe(ctx, deps, stringArg(args, "name"), stringArg(args, "limit_date"))
			},
		},
		builtinTool{
			name:        "cancel_tag_subscribe",
			description: "取消标签追新，不取消该标签下已有的影片订阅。",
			parameters: objectSchema(map[string]any{
				"name": stringProperty("标签名称"),
			}, "name"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return cancelTagSubscribe(ctx, deps, stringArg(args, "name"))
			},
		},
		builtinTool{
			name:        "query_tag_subscribes",
			description: "列出当前已订阅的标签，最多 50 条。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return queryTagSubscribes(ctx, deps)
			},
		},
		builtinTool{
			name:        "query_tags",
			description: "按关键词查询标签目录，按影片数量降序返回前 30 个标签，用于订阅前确认真实标签名。",
			parameters: objectSchema(map[string]any{
				"keyword": stringProperty("标签名称关键词，可为空表示不过滤"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryTags(ctx, deps, stringArg(args, "keyword"))
			},
		},
		builtinTool{
			name:        "download_subscribe",
			description: "立即为所有有效订阅触发一次下载受理，只报告受理条数。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return downloadSubscribe(ctx, deps)
			},
		},
		builtinTool{
			name:        "query_download_tasks",
			description: "查询下载任务，最多返回 20 条；status 支持 all、downloading、completed，其他值按 all 处理。",
			parameters: objectSchema(map[string]any{
				"query":  stringProperty("按番号或影片标题过滤的关键词，可为空"),
				"status": stringProperty("任务状态：all 全部、downloading 下载中、completed 已完成", "all", "downloading", "completed"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryDownloadTasks(ctx, deps, stringArg(args, "query"), stringArg(args, "status"))
			},
		},
		builtinTool{
			name:        "query_logs",
			description: "查询后台日志，用于排查下载失败、采集异常；lines 默认 80，最多 200。",
			parameters: objectSchema(map[string]any{
				"keyword": stringProperty("日志关键词，可为空"),
				"level":   stringProperty("日志级别", "debug", "info", "warning", "error"),
				"lines":   integerProperty("返回条数，默认 80，取值范围 1-200"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryLogs(ctx, deps, stringArg(args, "keyword"), stringArg(args, "level"), intArg(args, "lines", 80, 1, 200))
			},
		},
		builtinTool{
			name:        "query_schedulers",
			description: "列出全部定时任务及其 cron 表达式、最近执行时间和运行状态。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return querySchedulers(ctx, deps)
			},
		},
		builtinTool{
			name:        "run_cron_job",
			description: "按名称立即触发一个定时任务；任务名必须与 query_schedulers 返回的名称一致。",
			parameters: objectSchema(map[string]any{
				"name": stringProperty("定时任务名称"),
			}, "name"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return runCronJob(ctx, deps, stringArg(args, "name"))
			},
		},
		builtinTool{
			name:        "query_health",
			description: "查看版本、渠道与能力配置摘要，并附带最近 5 条错误日志，用于快速自检。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return queryHealth(ctx, deps)
			},
		},
		builtinTool{
			name:        "get_version",
			description: "查询当前 ByteMuse 运行版本。",
			parameters:  objectSchema(map[string]any{}),
			run: func(ctx context.Context, _ map[string]any) (any, error) {
				return getVersion(ctx, deps)
			},
		},
		builtinTool{
			name:        "send_message",
			description: "在当前对话渠道追加发送一条纯文本消息；只能发文字，不能发送图片或文件。",
			parameters: objectSchema(map[string]any{
				"message": stringProperty("要发送给用户的纯文本内容"),
			}, "message"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return sendMessage(ctx, deps, stringArg(args, "message"))
			},
		},
	}
}

// builtinTool 是内置工具的统一实现：名称、说明、参数结构与执行函数一次声明。
// 全部内置工具共用同一套 Tool 方法，避免为每个工具重复实现接口。
type builtinTool struct {
	name        string
	description string
	parameters  map[string]any
	run         func(ctx context.Context, args map[string]any) (any, error)
}

// Name 返回工具名，必须与 OpenAI function calling 契约一致。
func (t builtinTool) Name() string { return t.name }

// Description 返回给模型阅读的中文说明。
func (t builtinTool) Description() string { return t.description }

// Parameters 返回 JSON Schema 参数定义。
func (t builtinTool) Parameters() map[string]any { return t.parameters }

// Run 执行工具；业务性失败返回说明文本，只有真正的执行异常才返回 error。
func (t builtinTool) Run(ctx context.Context, args map[string]any) (any, error) {
	return t.run(ctx, args)
}

// objectSchema 构造对象型 JSON Schema；无必填项时仍显式返回空数组，避免模型误判。
func objectSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// stringProperty 构造字符串参数定义，可选枚举用于限定取值。
func stringProperty(description string, enum ...string) map[string]any {
	property := map[string]any{"type": "string", "description": description}
	if len(enum) > 0 {
		property["enum"] = enum
	}
	return property
}

// integerProperty 构造整数参数定义。
func integerProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

// normalizeLimitDate 归一化追新起始日期；为空时按当天计算，与影片发行日期的本地口径一致。
func normalizeLimitDate(raw string) string {
	date := strings.TrimSpace(raw)
	if date == "" {
		return time.Now().Format("2006-01-02")
	}
	return date
}
