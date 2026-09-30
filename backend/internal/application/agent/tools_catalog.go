package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

const (
	// mediaSearchLimit 是 Agent 单次影片搜索返回的最大条数。
	mediaSearchLimit = 20
	// tagQueryLimit 是标签目录查询的单页上限，不超过 ports.MaxTagPageSize。
	tagQueryLimit = 200
	// tagResultLimit 是标签查询回填模型的条数上限。
	tagResultLimit = 30
	// torrentResultLimit 是资源搜索回填模型的条数上限。
	torrentResultLimit = 10
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
	return map[string]any{"total": page.Total, "returned": len(items), "items": items}
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
	item, failure := lookupToolMedia(ctx, deps, code)
	if failure != "" {
		return failure, nil
	}
	return mediaBrief(item), nil
}

// searchActor 按名称查询演员及其订阅截止日期。
func searchActor(ctx context.Context, deps Deps, raw string) (any, error) {
	if deps.Actors == nil {
		return "演员服务不可用", nil
	}
	name := strings.TrimSpace(raw)
	page, err := deps.Actors.List(ctx, 1, mediaSearchLimit, "", name)
	if err != nil {
		return nil, fmt.Errorf("查询演员失败: %w", err)
	}
	if len(page.Items) == 0 {
		if name == "" {
			return "演员目录为空", nil
		}
		return "未找到演员: " + name, nil
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{"name": item.Name, "photo": item.Photo, "limit_date": item.LimitDate})
	}
	return map[string]any{"total": page.Total, "returned": len(items), "items": items}, nil
}

// searchMedia 按关键词搜索本地影片库。
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
		return "未找到与「" + keyword + "」匹配的影片", nil
	}
	result := mediaListResult(page)
	result["keyword"] = keyword
	return result, nil
}

// getRank 查询榜单；周期取值由 normalizeRankType 收敛。
func getRank(ctx context.Context, deps Deps, raw string) (any, error) {
	rankType := normalizeRankType(raw)
	if deps.Queries == nil {
		return "影片查询服务不可用", nil
	}
	page, err := deps.Queries.Rank(ctx, rankType, 1, mediaSearchLimit)
	if err != nil {
		return nil, fmt.Errorf("查询榜单失败: %w", err)
	}
	if len(page.Items) == 0 {
		return "榜单暂无数据（" + rankType + "）", nil
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
func getReleaseToday(ctx context.Context, deps Deps) (any, error) {
	if deps.Queries == nil {
		return "影片查询服务不可用", nil
	}
	page, err := deps.Queries.ReleaseToday(ctx, 1, mediaSearchLimit)
	if err != nil {
		return nil, fmt.Errorf("查询今日上新失败: %w", err)
	}
	if len(page.Items) == 0 {
		return "今天没有发行记录", nil
	}
	return mediaListResult(page), nil
}

// getRecommendations 返回未订阅的推荐影片。
func getRecommendations(ctx context.Context, deps Deps, limit int) (any, error) {
	if deps.Queries == nil {
		return "影片查询服务不可用", nil
	}
	page, err := deps.Queries.Recommend(ctx, 1, limit)
	if err != nil {
		return nil, fmt.Errorf("查询推荐失败: %w", err)
	}
	if len(page.Items) == 0 {
		return "暂无可推荐的影片", nil
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
func searchTorrents(ctx context.Context, deps Deps, raw string) (any, error) {
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
	if len(resources) == 0 {
		return "未找到资源: " + query, nil
	}
	items := make([]map[string]any, 0, min(len(resources), torrentResultLimit))
	for _, item := range resources {
		if len(items) == torrentResultLimit {
			break
		}
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
	return map[string]any{"query": query, "total": len(resources), "returned": len(items), "items": items}, nil
}

// queryTags 查询标签目录并按影片数量降序返回前 30 个，供订阅前确认真实标签名。
func queryTags(ctx context.Context, deps Deps, raw string) (any, error) {
	if deps.Tags == nil {
		return "标签服务不可用", nil
	}
	keyword := strings.TrimSpace(raw)
	page, err := deps.Tags.List(ctx, 1, tagQueryLimit, keyword)
	if err != nil {
		return nil, fmt.Errorf("查询标签失败: %w", err)
	}
	if len(page.Items) == 0 {
		if keyword == "" {
			return "标签目录为空", nil
		}
		return "未找到标签: " + keyword, nil
	}
	ranked := make([]ports.Tag, len(page.Items))
	copy(ranked, page.Items)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].MediaCount > ranked[j].MediaCount })
	if len(ranked) > tagResultLimit {
		ranked = ranked[:tagResultLimit]
	}
	items := make([]map[string]any, 0, len(ranked))
	for _, item := range ranked {
		items = append(items, map[string]any{
			"name":        item.Name,
			"media_count": item.MediaCount,
			"category":    item.Category,
			"limit_date":  item.LimitDate,
		})
	}
	return map[string]any{"total": page.Total, "returned": len(items), "items": items}, nil
}
