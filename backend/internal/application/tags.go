package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"strings"
	"time"
)

// ErrInvalidTagRule 表示非法分类、订阅状态或日期。
var ErrInvalidTagRule = errors.New("invalid tag rule")

// ErrTagFollowPending 表示规则已保存，追新因异常未完成，后续可安全重试。
var ErrTagFollowPending = errors.New("tag rule saved, follow pending")

// TagService 统一标签目录、订阅规则及追新入口。
type TagService struct{ repository ports.TagRepository }

// NewTagService 绑定标签仓储。
func NewTagService(repository ports.TagRepository) *TagService {
	return &TagService{repository: repository}
}

// List 验证分页与过滤条件，空结果返回空数组。
func (s *TagService) List(ctx context.Context, page, size int, search string, filters ...string) (Page[ports.Tag], error) {
	if page < 1 || size < 1 || size > ports.MaxTagPageSize {
		return Page[ports.Tag]{}, ErrInvalidPagination
	}
	q := ports.TagListQuery{Search: strings.TrimSpace(search), Limit: size, Offset: (page - 1) * size}
	if len(filters) > 0 {
		q.Subscription = filters[0]
	}
	if len(filters) > 1 {
		q.Category = filters[1]
	}
	if q.Subscription != "" && q.Subscription != "all" && q.Subscription != "active" && q.Subscription != "none" {
		return Page[ports.Tag]{}, ErrInvalidTagRule
	}
	valid := false
	for _, c := range []string{"", "主题", "角色", "服装", "体型", "行为", "玩法", "类别", "未分类"} {
		if q.Category == c {
			valid = true
		}
	}
	if !valid {
		return Page[ports.Tag]{}, ErrInvalidTagRule
	}
	items, total, e := s.repository.ListTags(ctx, q)
	return Page[ports.Tag]{Page: page, PageSize: size, Total: total, Items: nonNil(items)}, e
}

// SaveSubscription 先持久化日期，再匹配本地已有影片；部分追新失败保留规则供定时任务恢复。
func (s *TagService) SaveSubscription(ctx context.Context, name, date string) (ports.Tag, error) {
	name, date = strings.TrimSpace(name), strings.TrimSpace(date)
	parsed, e := time.Parse("2006-01-02", date)
	if e != nil || parsed.Year() < 1 {
		return ports.Tag{}, ErrInvalidTagRule
	}
	if name == "" {
		return ports.Tag{}, ports.ErrTagNotFound
	}
	item, e := s.repository.SaveSubscription(ctx, name, date)
	if e != nil {
		return item, e
	}
	if _, e = s.Follow(ctx, name); e != nil {
		return item, ErrTagFollowPending
	}
	return item, nil
}

// CancelSubscription 停止标签追新，不取消已有影片订阅。
func (s *TagService) CancelSubscription(ctx context.Context, name string) (ports.Tag, error) {
	return s.repository.CancelSubscription(ctx, strings.TrimSpace(name))
}

// Follow 为保存规则和定时任务提供同一入口；日志记录真实创建数量和失败原因。
func (s *TagService) Follow(ctx context.Context, name string) (int, error) {
	n, e := s.repository.Follow(ctx, name)
	if e != nil {
		logging.Error(logging.CategorySubscription, "标签追新未完成，已保存规则将在后续任务继续处理", "created", n, "error", e.Error())
	} else {
		logging.Info(logging.CategorySubscription, "标签追新完成", "created", n)
	}
	return n, e
}

// SearchMedia 精确查询标签关联影片，由数据库完成分页。
func (s *TagService) SearchMedia(ctx context.Context, name string, page, size int) (Page[domain.Media], error) {
	if e := validatePagination(page, size); e != nil {
		return Page[domain.Media]{}, e
	}
	result, e := s.repository.SearchMedia(ctx, strings.TrimSpace(name), size, (page-1)*size)
	return Page[domain.Media]{Page: page, PageSize: size, Total: result.Total, Items: nonNil(result.Items)}, e
}
