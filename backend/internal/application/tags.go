package application

import (
	"bytemuse/backend/internal/ports"
	"context"
	"strings"
)

// TagService 提供已采集标签的只读目录，不执行影片匹配或追新订阅。
type TagService struct{ repository ports.TagRepository }

// NewTagService 绑定标签目录仓储。
func NewTagService(repository ports.TagRepository) *TagService {
	return &TagService{repository: repository}
}

// List 验证分页后返回标签名及关联影片数，空结果使用空数组。
func (s *TagService) List(ctx context.Context, page, pageSize int, search string) (Page[ports.Tag], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[ports.Tag]{}, err
	}
	items, total, err := s.repository.ListTags(ctx, strings.TrimSpace(search), pageSize, (page-1)*pageSize)
	if err != nil {
		return Page[ports.Tag]{}, err
	}
	return Page[ports.Tag]{Page: page, PageSize: pageSize, Total: total, Items: nonNil(items)}, nil
}
