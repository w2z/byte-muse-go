package agent

import (
	"context"
	"fmt"
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
			description: "按番号精确查询影片资料；本地缺失时联网采集并保存精确匹配资料，不自动订阅或下载。返回标题、发行日期、封面、订阅与媒体库状态。",
			parameters: objectSchema(map[string]any{
				"code": stringProperty("影片番号，例如 SSIS-001"),
			}, "code"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchCode(ctx, deps, stringArg(args, "code"))
			},
		},
		builtinTool{
			name:        "search_actor",
			description: "按名称分页查询演员及其订阅截止日期；名称为空时浏览全部演员。",
			parameters: listSchema(listPages, map[string]any{
				"name": stringProperty("演员名称或名称片段，可为空"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchActor(ctx, deps, stringArg(args, "name"), "", listPages.read(args))
			},
		},
		searchTool{builtinTool: builtinTool{
			name:        "search_media",
			description: "按关键词优先搜索本地影片；未命中时搜索已配置的 PT、BT 资源。",
			parameters: objectSchema(map[string]any{
				"keyword": stringProperty("搜索关键词，可以是番号、原标题片段或演员名"),
			}, "keyword"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchMedia(ctx, deps, stringArg(args, "keyword"))
			},
		}, local: localSearchTool(deps)},
		builtinTool{
			name:        "query_media",
			description: "分页浏览本地影片；支持关键词与状态组合筛选，沿用网页按需翻译，不主动采集、订阅或下载。",
			parameters: listSchema(listPages, map[string]any{
				"search":       stringProperty("番号、原标题或译名关键词；省略或空字符串表示不过滤"),
				"subscription": stringProperty("订阅状态；空字符串不限，none 无有效订阅、active 有效订阅、canceled 已取消", "", "none", "active", "canceled"),
				"download":     stringProperty("下载状态；空字符串不限，unknown 表示无下载任务或最近任务状态未知，其余按最近下载任务筛选", "", "queued", "searching", "submitted", "downloading", "completed", "failed", "unknown"),
				"library":      libraryProperty(),
				"video_type":   videoTypeProperty(),
				"vr":           stringProperty("VR 筛选；空字符串不限，hide 隐藏 VR，only 仅 VR，与网页一样按番号识别", "", "hide", "only"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryMedia(ctx, deps, args)
			},
		},
		builtinTool{
			name:        "search_category_media",
			description: "分页查询影片：标签精确名称、影片类型、名称或番号关键词三选一。",
			parameters: listSchema(listPages, map[string]any{
				"keyword":    stringProperty("名称或番号关键词；与 tag、video_type 互斥"),
				"tag":        stringProperty("影片标签的准确名称；按影片类型查询时留空"),
				"video_type": videoTypeProperty(),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchCategoryMedia(ctx, deps, args)
			},
		},
		builtinTool{
			name:        "get_rank",
			description: "分页查询榜单影片；type 支持 daily、weekly、monthly。",
			parameters: listSchema(listPages, map[string]any{
				"type": stringProperty("榜单周期：daily 日榜、weekly 周榜、monthly 月榜；缺省或非法值按 monthly 处理", "daily", "weekly", "monthly"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return getRank(ctx, deps, stringArg(args, "type"), listPages.read(args))
			},
		},
		builtinTool{
			name:        "get_release_today",
			description: "分页查询今天发行的影片。",
			parameters:  listSchema(listPages, nil),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return getReleaseToday(ctx, deps, listPages.read(args))
			},
		},
		builtinTool{
			name:        "get_recommendations",
			description: "分页推荐未订阅影片；page_size 优先于旧参数 limit。",
			parameters: listSchema(recommendPages, map[string]any{
				"limit": integerProperty("返回条数，默认 10，取值范围 1-20"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return getRecommendations(ctx, deps, recommendPages.read(args))
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
			description: "分页浏览本次资源站搜索返回的候选，含站点、大小、做种数与免费标记。",
			parameters: listSchema(torrentPages, map[string]any{
				"query": stringProperty("番号或搜索关键词"),
			}, "query"),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchTorrents(ctx, deps, stringArg(args, "query"), torrentPages.read(args))
			},
		},
		builtinTool{
			name:        "add_subscribe",
			description: "按番号创建影片订阅并登记资源搜索与下载受理（不表示下载完成）；多个番号用逗号分隔，逐个受理并返回每一条结果。",
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
			description: "分页列出当前有效影片订阅。",
			parameters:  listSchema(listPages, nil),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return querySubscribes(ctx, deps, listPages.read(args))
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
			description: "按名称分页列出已订阅的演员。",
			parameters:  listSchema(listPages, map[string]any{"keywords": stringProperty("演员名称关键词，省略时不限")}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return searchActor(ctx, deps, stringArg(args, "keywords"), "active", listPages.read(args))
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
			description: "分页列出已订阅的标签，可按标签名和分类筛选。",
			parameters:  listSchema(tagPages, map[string]any{"search": stringProperty("标签名称关键词，省略时不限"), "category": tagCategoryProperty()}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryTags(ctx, deps, stringArg(args, "search"), "active", stringArg(args, "category"), tagPages.read(args))
			},
		},
		builtinTool{
			name:        "query_tags",
			description: "按关键词分页查询标签目录，按影片数量降序排列。",
			parameters: listSchema(tagPages, map[string]any{
				"category": tagCategoryProperty(),
				"keyword":  stringProperty("标签名称关键词，可为空表示不过滤"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryTags(ctx, deps, stringArg(args, "keyword"), "", stringArg(args, "category"), tagPages.read(args))
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
			description: "按状态、番号或影片标题分页查询下载任务。",
			parameters: listSchema(listPages, map[string]any{
				"query":  stringProperty("按番号或影片标题过滤的关键词，可为空"),
				"status": stringProperty("任务状态：all 全部、downloading 下载中、completed 已完成", "all", "downloading", "completed"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryDownloadTasks(ctx, deps, stringArg(args, "query"), stringArg(args, "status"), listPages.read(args))
			},
		},
		builtinTool{
			name:        "query_logs",
			description: "分页查询后台日志；page_size 优先于旧参数 lines。",
			parameters: listSchema(logPages, map[string]any{
				"keyword": stringProperty("日志关键词，可为空"),
				"level":   stringProperty("日志级别", "debug", "info", "warning", "error"),
				"lines":   integerProperty("返回条数，默认 80，取值范围 1-200"),
			}),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return queryLogs(ctx, deps, stringArg(args, "keyword"), stringArg(args, "level"), logPages.read(args))
			},
		},
		builtinTool{
			name:        "query_schedulers",
			description: "分页列出定时任务及其 cron 表达式、最近执行时间和运行状态。",
			parameters:  listSchema(listPages, nil),
			run: func(ctx context.Context, args map[string]any) (any, error) {
				return querySchedulers(ctx, deps, listPages.read(args))
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
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

// pagePolicy 同时定义工具目录和执行层的分页范围；保留已公开的旧条数参数。
type pagePolicy struct {
	defaultSize, maxSize int
	alias                string
	aliasMax             int
}

var (
	listPages      = pagePolicy{defaultSize: 20, maxSize: 100}
	tagPages       = pagePolicy{defaultSize: 20, maxSize: 200}
	recommendPages = pagePolicy{10, 100, "limit", 20}
	torrentPages   = pagePolicy{defaultSize: 10, maxSize: 100}
	logPages       = pagePolicy{80, 200, "lines", 200}
)

type toolPage struct{ number, size int }

func (policy pagePolicy) read(args map[string]any) toolPage {
	size := policy.defaultSize
	if policy.alias != "" {
		size = intArg(args, policy.alias, size, 1, policy.aliasMax)
	}
	return toolPage{intArg(args, "page", 1, 1, 1000000), intArg(args, "page_size", size, 1, policy.maxSize)}
}

// listSchema 在每个列表工具的业务字段上添加一致的分页说明与约束。
func listSchema(policy pagePolicy, properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	properties["page"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000, "default": 1, "description": "页码，从 1 开始"}
	size := map[string]any{"type": "integer", "minimum": 1, "maximum": policy.maxSize, "description": fmt.Sprintf("每页条数；默认 %d，上限 %d", policy.defaultSize, policy.maxSize)}
	// SDK 会注入 schema 默认值；有旧参数时由执行层决定缺省条数。
	if policy.alias == "" {
		size["default"] = policy.defaultSize
	}
	properties["page_size"] = size
	return objectSchema(properties, required...)
}

func tagCategoryProperty() map[string]any {
	return stringProperty("标签分类；省略或空字符串表示不限", "", "主题", "角色", "服装", "体型", "行为", "玩法", "类别", "未分类")
}

// stringProperty 构造字符串参数定义，可选枚举用于限定取值。
func stringProperty(description string, enum ...string) map[string]any {
	property := map[string]any{"type": "string", "description": description}
	if len(enum) > 0 {
		property["enum"] = enum
	}
	return property
}

func videoTypeProperty() map[string]any {
	return stringProperty("影片类型；空字符串不限，unknown 未分类，其余为有码、无码、无码破解或流出", "", "censored", "uncensored", "uncensored_cracked", "leaked", "unknown")
}
func libraryProperty() map[string]any {
	return stringProperty("媒体库状态；空字符串不限，unknown 未知、absent 未入库、present 已入库", "", "unknown", "absent", "present")
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
