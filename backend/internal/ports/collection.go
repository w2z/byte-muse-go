package ports

import (
	"context"
)

// ValidCollectionSource 是采集和存储共用的来源白名单，不表示网络实时可达。
func ValidCollectionSource(source string) bool {
	for _, s := range CollectionSources() {
		if s.ID == source {
			return true
		}
	}
	return false
}

// CollectionSources 只发布已启用来源；新增站点须通过后台实网核验后才加入。
// 保留既有 JavDB 能力；解析器存在不等于已启用，每次返回独立值。
func CollectionSources() []CollectionSource {
	return []CollectionSource{
		{ID: "javdb", Kinds: []string{"search", "detail", "rank", "actor"}},
		{ID: "netflav", Kinds: []string{"search", "detail"}},
		{ID: "javlibrary", Kinds: []string{"rank"}},
		{ID: "avbase", Kinds: []string{"search", "actor", "date"}},
		{ID: "jable", Kinds: []string{"search"}},
		{ID: "supjav", Kinds: []string{"search"}},
	}
}

// CollectionRankPeriods 定义已接入影片榜单，来源之间不共享周期语义。
func CollectionRankPeriods(source string) []string {
	switch source {
	case "javdb":
		return []string{"daily", "weekly", "monthly"}
	case "javlibrary":
		return []string{"wanted"}
	default:
		return nil
	}
}

// CollectionRankKey 保留旧 JavDB 键名；其他站点使用命名空间防止覆盖旧榜。
func CollectionRankKey(req CollectionRequest) string {
	if req.Kind != "rank" || req.Source == "javdb" {
		return req.Period
	}
	return req.Source + ":" + req.Period
}

// CollectionRequest 指定单一数据源的一页查询；采集与订阅、下载相互独立。
type CollectionRequest struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Query  string `json:"query,omitempty"`
	Period string `json:"period,omitempty"`
	Page   int    `json:"page"`
}

// CollectedActor 是站点提供的演员资料，不包含订阅意图。
type CollectedActor struct {
	SourceID string   `json:"source_id,omitempty"` // 来源站点内演员 ID；仅与该批次 source 联合使用。
	Name     string   `json:"name"`
	Photo    string   `json:"photo,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
}

// CollectedMedia 保留站点身份；无可靠番号时不得用标题猜测关联。
type CollectedMedia struct {
	SourceID        string           `json:"source_id"`
	URL             string           `json:"url"`
	Code            string           `json:"code,omitempty"`
	Title           string           `json:"title"`
	VideoType       string           `json:"video_type,omitempty"` // 明确的影片类型；缺失时不猜测。
	PosterURL       string           `json:"poster_url,omitempty"`
	ReleaseDate     string           `json:"release_date,omitempty"`
	DurationMinutes int              `json:"duration_minutes,omitempty"`
	Actors          []CollectedActor `json:"actors"`
	Tags            []string         `json:"tags"`
}

// CollectionBatch 是经过站点解析的单页结果；空列表必须由有效页面确认。
type CollectionBatch struct {
	Items   []CollectedMedia `json:"items"`
	Actors  []CollectedActor `json:"actors"`
	HasMore bool             `json:"has_more"`
}

// CollectionSource 描述数据源实际支持的能力。
type CollectionSource struct {
	ID    string   `json:"id"`
	Kinds []string `json:"kinds"`
}

// Collector 只负责外部读取和解析，不修改业务数据库。
type Collector interface {
	Sources() []CollectionSource
	Collect(context.Context, CollectionRequest) (CollectionBatch, error)
}

// CollectionCounts 统计事务提交后的结果，Existing 表示已有来源记录。
type CollectionCounts struct {
	Fetched        int `json:"fetched"`
	New            int `json:"new"`
	Existing       int `json:"existing"`
	Inserted       int `json:"inserted"`
	MediaInserted  int `json:"media_inserted"`
	ActorsInserted int `json:"actors_inserted"`
}

// CollectionRepository 原子保存来源记录，为新番号及新演员建档，并仅向命中影片追加去重标签。
type CollectionRepository interface {
	SaveCollection(context.Context, CollectionRequest, CollectionBatch) (CollectionCounts, error)
}
