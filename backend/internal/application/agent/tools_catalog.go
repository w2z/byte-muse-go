package agent

import (
	"context"
	"fmt"
	"strings"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

const (
	// mediaSearchLimit 是 Agent 单次影片搜索返回的最大条数。
	mediaSearchLimit = 20
)

// mediaBrief 把影片压缩成渠道对话需要的精简字段。
// 缺失值保持 null，便于模型区分“没有数据”与“空字符串”。
func mediaBrief(item domain.Media) map[string]any {
	return map[string]any{
		"code":                item.Code,
		"title":               item.Title,
		"translated_title":    item.TranslatedTitle,
		"release_date":        item.ReleaseDate,
		"poster_url":          item.PosterURL,
		"banner_url":          item.BannerURL,
		"preview_url":         item.PreviewURL,
		"duration_minutes":    item.DurationMinutes,
		"subscription_status": string(item.SubscriptionStatus),
		"library_status":      string(item.LibraryStatus),
		"display_status":      string(item.DisplayStatus),
	}
}

// mediaListResult 统一构造影片列表结果，搜索、榜单、上新和推荐共用同一套精简字段。
func mediaListResult(page application.Page[domain.Media]) map[string]any {
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, mediaBrief(item))
	}
	return pageResult(toolPage{page.Page, page.PageSize}, page.Total, items)
}

// pageResult 保留过滤后的完整总数，越界页仍返回空数组及原页码。
func pageResult(p toolPage, total int, items []map[string]any) map[string]any {
	return map[string]any{"page": p.number, "page_size": p.size, "has_more": p.number*p.size < total, "total": total, "returned": len(items), "items": items}
}

// pageWindow 仅用于上游一次性返回完整候选集的列表。
func pageWindow[T any](items []T, p toolPage) []T {
	start := min((p.number-1)*p.size, len(items))
	return items[start:min(start+p.size, len(items))]
}

// searchTool 保留渠道的外站搜索，MCP 同名工具只读本地目录，避免空页触发外站请求。
type searchTool struct {
	builtinTool
	local Tool
}

func localSearchTool(deps Deps) Tool {
	return builtinTool{
		name:        "search_media",
		description: "按名称或番号分页搜索本地影片，可组合影片类型、媒体库和订阅状态；不回退外站。",
		parameters: listSchema(listPages, map[string]any{
			"keyword":      stringProperty("名称或番号关键词；省略或空字符串表示不过滤"),
			"video_type":   videoTypeProperty(),
			"library":      libraryProperty(),
			"subscription": stringProperty("有效订阅状态；空字符串不限", "", "active", "none"),
		}),
		run: func(ctx context.Context, args map[string]any) (any, error) {
			return catalogMedia(ctx, deps, args, stringArg(args, "keyword"))
		},
	}
}

// lookupToolMedia 按番号精确定位本地影片；查询服务缺失或未命中时返回面向用户的说明文本。
func lookupToolMedia(ctx context.Context, deps Deps, code string) (domain.Media, string) {
	if deps.Queries == nil {
		return domain.Media{}, "影片查询服务不可用"
	}
	item, ok := lookupMedia(ctx, deps, code)
	if !ok {
		return domain.Media{}, "未找到番号: " + code
	}
	return item, ""
}

// searchCode 按番号返回一部影片的完整精简资料。
func searchCode(ctx context.Context, deps Deps, raw string) (any, error) {
	code := domain.NormalizeCode(raw)
	if code == "" {
		return "请提供番号，例如 SSIS-001", nil
	}
	if item, found := lookupMedia(ctx, deps, code); found {
		return mediaBrief(item), nil
	}
	item, failure := resolveMedia(ctx, deps, code)
	if failure != "" {
		if deps.Torrents != nil {
			result, err := searchTorrents(ctx, deps, code, torrentPages.read(nil))
			if err != nil {
				return map[string]any{"media_status": failure, "resource_status": "资源站搜索失败，请稍后重试"}, nil
			}
			return map[string]any{"media_status": failure, "resources": result, "note": "资源候选不代表影片资料已入库；部分站点可能不可用"}, nil
		}
		return failure, nil
	}
	if deps.Torrents != nil {
		result, err := searchTorrents(ctx, deps, code, torrentPages.read(nil))
		if err != nil {
			result = "资源站搜索失败，请稍后重试"
		}
		return map[string]any{"media": mediaBrief(item), "resources": result, "note": "部分资源站可能不可用"}, nil
	}
	return mediaBrief(item), nil
}

// searchActor 按名称查询演员及其订阅截止日期。
func searchActor(ctx context.Context, deps Deps, raw, subscription string, p toolPage) (any, error) {
	if deps.Actors == nil {
		return "演员服务不可用", nil
	}
	name := strings.TrimSpace(raw)
	page, err := deps.Actors.List(ctx, p.number, p.size, subscription, name)
	if err != nil {
		return nil, fmt.Errorf("查询演员失败: %w", err)
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{"name": item.Name, "photo": item.Photo, "limit_date": item.LimitDate})
	}
	return pageResult(p, page.Total, items), nil
}

// queryMedia 复用网页影片列表的分页与过滤；不联网补资料或创建订阅/下载，保留服务已有的按需翻译。
func queryMedia(ctx context.Context, deps Deps, args map[string]any) (any, error) {
	return catalogMedia(ctx, deps, args, stringArg(args, "search"))
}

// catalogMedia 将不同入口的关键词映射到网页目录的同一组过滤条件。
func catalogMedia(ctx context.Context, deps Deps, args map[string]any, keyword string) (any, error) {
	if deps.Catalog == nil {
		return "影片列表服务不可用", nil
	}
	p := listPages.read(args)
	page, err := deps.Catalog.List(ctx, p.number, p.size, ports.MediaListQuery{
		Search:             keyword,
		SubscriptionStatus: stringArg(args, "subscription"),
		DownloadStatus:     stringArg(args, "download"),
		LibraryStatus:      stringArg(args, "library"),
		VideoType:          stringArg(args, "video_type"),
		VR:                 stringArg(args, "vr"),
	})
	if err != nil {
		return nil, fmt.Errorf("查询影片列表失败: %w", err)
	}
	result := mediaListResult(page)
	items := result["items"].([]map[string]any)
	for i, item := range page.Items {
		items[i]["id"], items[i]["video_type"], items[i]["download_status"] = item.ID, item.VideoType, item.DownloadStatus
	}
	return result, nil
}

// searchMedia 优先搜索本地影片；未命中时搜索已配置的资源来源。
func searchMedia(ctx context.Context, deps Deps, raw string) (any, error) {
	keyword := strings.TrimSpace(raw)
	if keyword == "" {
		return "请提供搜索关键词", nil
	}
	if deps.Queries == nil {
		return "影片查询服务不可用", nil
	}
	page, err := deps.Queries.Search(ctx, keyword, 1, mediaSearchLimit)
	if err != nil {
		return nil, fmt.Errorf("搜索影片失败: %w", err)
	}
	if len(page.Items) == 0 {
		var external []application.ExternalMedia
		incomplete := true
		if deps.Codes != nil {
			external, incomplete = deps.Codes.SearchExternal(ctx, keyword)
		}
		if deps.Torrents != nil {
			result, err := searchTorrents(ctx, deps, keyword, torrentPages.read(nil))
			if err != nil {
				result = "资源站搜索失败，请稍后重试"
			}
			return map[string]any{"keyword": keyword, "media_status": "本地未找到影片", "external_media": external, "external_incomplete": incomplete, "resources": result, "note": "外站影片和资源候选均未入库；只查询影片站第一页，资源站可能部分失败"}, nil
		}
		if deps.Codes != nil {
			return map[string]any{"keyword": keyword, "external_media": external, "external_incomplete": incomplete}, nil
		}
		return "未找到与「" + keyword + "」匹配的影片", nil
	}
	result := mediaListResult(page)
	result["keyword"] = keyword
	return result, nil
}

// searchCategoryMedia 复用标签或影片类型的权威查询；不写入订阅或下载。
func searchCategoryMedia(ctx context.Context, deps Deps, args map[string]any) (any, error) {
	tag, videoType, keyword := stringArg(args, "tag"), stringArg(args, "video_type"), stringArg(args, "keyword")
	if (tag != "" && videoType != "") || (keyword != "" && (tag != "" || videoType != "")) || (tag == "" && videoType == "" && keyword == "") {
		return nil, fmt.Errorf("tag（标签）、video_type（影片类型）、keyword（名称或番号）必须且只能指定一种")
	}
	p := listPages.read(args)
	var page application.Page[domain.Media]
	var err error
	switch {
	case tag != "":
		if deps.Tags == nil {
			return "标签服务不可用", nil
		}
		page, err = deps.Tags.SearchMedia(ctx, tag, p.number, p.size)
	default:
		if deps.Catalog == nil {
			return "影片查询服务不可用", nil
		}
		page, err = deps.Catalog.List(ctx, p.number, p.size, ports.MediaListQuery{VideoType: videoType, Search: keyword})
	}
	if err != nil {
		return nil, fmt.Errorf("查询分类影片失败: %w", err)
	}
	result := mediaListResult(page)
	result["tag"] = tag
	result["video_type"] = videoType
	if keyword != "" {
		result["keyword"] = keyword
	}
	return result, nil
}

// getRank 查询榜单；周期取值由 normalizeRankType 收敛。
func getRank(ctx context.Context, deps Deps, raw string, p toolPage) (any, error) {
	rankType := normalizeRankType(raw)
	if deps.Queries == nil {
		return "影片查询服务不可用", nil
	}
	page, err := deps.Queries.Rank(ctx, rankType, p.number, p.size, ports.MediaListQuery{})
	if err != nil {
		return nil, fmt.Errorf("查询榜单失败: %w", err)
	}
	result := mediaListResult(page)
	result["type"] = rankType
	return result, nil
}

// normalizeRankType 收敛榜单周期；缺省或非法值统一回落到 monthly，与仓储默认口径一致。
func normalizeRankType(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "daily", "weekly", "monthly":
		return value
	default:
		return "monthly"
	}
}

// getReleaseToday 查询今天发行的影片。
func getReleaseToday(ctx context.Context, deps Deps, p toolPage) (any, error) {
	if deps.Queries == nil {
		return "影片查询服务不可用", nil
	}
	page, err := deps.Queries.ReleaseToday(ctx, p.number, p.size, ports.MediaListQuery{})
	if err != nil {
		return nil, fmt.Errorf("查询今日上新失败: %w", err)
	}
	return mediaListResult(page), nil
}

// getRecommendations 返回未订阅的推荐影片。
func getRecommendations(ctx context.Context, deps Deps, p toolPage) (any, error) {
	if deps.Queries == nil {
		return "影片查询服务不可用", nil
	}
	page, err := deps.Queries.Recommend(ctx, p.number, p.size, ports.MediaListQuery{SubscriptionStatus: "none"})
	if err != nil {
		return nil, fmt.Errorf("查询推荐失败: %w", err)
	}
	return mediaListResult(page), nil
}

// getDashboard 返回运营概览。
func getDashboard(ctx context.Context, deps Deps) (any, error) {
	if deps.Dashboard == nil {
		return "统计服务不可用", nil
	}
	summary, err := deps.Dashboard.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询运营概览失败: %w", err)
	}
	return summary, nil
}

// queryLibrary 判断番号是否已存在媒体库中。
func queryLibrary(ctx context.Context, deps Deps, raw string) (any, error) {
	code := domain.NormalizeCode(raw)
	if code == "" {
		return "请提供番号，例如 SSIS-001", nil
	}
	item, failure := lookupToolMedia(ctx, deps, code)
	if failure != "" {
		return failure, nil
	}
	return map[string]any{
		"code":       domain.NormalizeCode(item.Code),
		"in_library": item.LibraryStatus == domain.LibraryStatusPresent,
	}, nil
}

// searchTorrents 在已配置的资源站搜索种子候选，复用订阅下载链路的搜索器。
func searchTorrents(ctx context.Context, deps Deps, raw string, p toolPage) (any, error) {
	query := strings.TrimSpace(raw)
	if query == "" {
		return "请提供要搜索的番号或关键词", nil
	}
	if deps.Torrents == nil {
		return "未配置资源搜索", nil
	}
	resources, err := deps.Torrents.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("搜索资源失败: %w", err)
	}
	window := pageWindow(resources, p)
	items := make([]map[string]any, 0, len(window))
	for _, item := range window {
		items = append(items, map[string]any{
			"site":    item.Site,
			"title":   item.Title,
			"size_mb": item.SizeMB,
			"seeders": item.Seeders,
			"chinese": item.Chinese,
			"free":    item.Free,
			"uhd":     item.UHD,
			"uc":      item.UC,
			"kind":    item.Kind,
		})
	}
	result := pageResult(p, len(resources), items)
	result["query"] = query
	return result, nil
}

// queryTags 共用仓储的全局排序、分类与订阅过滤，避免仅在当前页内排序。
func queryTags(ctx context.Context, deps Deps, raw, subscription, category string, p toolPage) (any, error) {
	if deps.Tags == nil {
		return "标签服务不可用", nil
	}
	keyword := strings.TrimSpace(raw)
	page, err := deps.Tags.List(ctx, p.number, p.size, keyword, subscription, category)
	if err != nil {
		return nil, fmt.Errorf("查询标签失败: %w", err)
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{
			"name":        item.Name,
			"media_count": item.MediaCount,
			"category":    item.Category,
			"limit_date":  item.LimitDate,
		})
	}
	return pageResult(p, page.Total, items), nil
}
