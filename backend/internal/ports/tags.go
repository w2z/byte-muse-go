package ports

import "context"

// Tag 是从已存影片资料中聚合的标签，数量按不同影片去重，不代表媒体库已入库数量。
type Tag struct {
	Name       string `json:"name"`
	MediaCount int    `json:"media_count"`
}

// TagRepository 只读查询已有标签，数据库负责筛选、聚合与分页，不触发外站采集。
type TagRepository interface {
	ListTags(context.Context, string, int, int) ([]Tag, int, error)
}
