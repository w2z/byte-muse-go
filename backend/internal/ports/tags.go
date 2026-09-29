package ports

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
)

// ErrTagNotFound 表示标签既不在源站字典中，也未出现在影片资料中。
var ErrTagNotFound = errors.New("tag not found")

// MaxTagPageSize 是标签目录的分页上限，供 HTTP、业务层和仓储共同校验。
const MaxTagPageSize = 500

// Tag 合并分类字典、影片标签与订阅规则；影片数量按不同影片去重。
type Tag struct {
	Name       string  `json:"name"`
	MediaCount int     `json:"media_count"`
	Category   string  `json:"category"`
	LimitDate  *string `json:"limit_date"`
}

// TagListQuery 在数据库中组合名称、类型与订阅状态过滤。
type TagListQuery struct {
	Search, Category, Subscription string
	Limit, Offset                  int
}

// TagRepository 管理分类目录与追新规则；不触发外站采集或下载。
type TagRepository interface {
	ListTags(context.Context, TagListQuery) ([]Tag, int, error)
	SaveSubscription(context.Context, string, string) (Tag, error)
	CancelSubscription(context.Context, string) (Tag, error)
	Follow(context.Context, string) (int, error)
	SearchMedia(context.Context, string, int, int) (domain.MediaPage, error)
}
