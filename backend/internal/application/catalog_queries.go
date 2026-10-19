package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// CatalogQueryService owns read-only catalog search and rank workflows.
type CatalogQueryService struct {
	repository ports.CatalogQueryRepository
	translator TranslationClient
	writer     ports.MediaTranslationWriter
}

// NewCatalogQueryService creates catalog projections backed by one repository.
func NewCatalogQueryService(repository ports.CatalogQueryRepository) *CatalogQueryService {
	return &CatalogQueryService{repository: repository}
}

// FindCode 在本地目录中分页查找精确番号；不采集、不写入，供查询卡片及按钮动作复用。
// 查询失败与未命中分别返回，避免将数据库异常误判为资料缺失。
func (s *CatalogQueryService) FindCode(ctx context.Context, raw string) (domain.Media, bool, error) {
	code := domain.NormalizeCode(raw)
	if code == "" {
		return domain.Media{}, false, nil
	}
	for page := 1; ; page++ {
		result, err := s.repository.Search(ctx, code, 100, (page-1)*100)
		if err != nil {
			return domain.Media{}, false, err
		}
		for _, media := range result.Items {
			if domain.NormalizeCode(media.Code) == code {
				return media, true, nil
			}
		}
		if len(result.Items) == 0 || page*100 >= result.Total {
			return domain.Media{}, false, nil
		}
	}
}

// NewCatalogQueryServiceWithTranslation enables the shared lazy title translation rule.
func NewCatalogQueryServiceWithTranslation(repository ports.CatalogQueryRepository, translator TranslationClient, writer ports.MediaTranslationWriter) *CatalogQueryService {
	return &CatalogQueryService{repository: repository, translator: translator, writer: writer}
}

// Search returns one validated page matching the supplied catalog query.
func (s *CatalogQueryService) Search(ctx context.Context, query string, page, pageSize int) (Page[domain.Media], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Media]{}, err
	}
	result, err := s.repository.Search(ctx, strings.TrimSpace(query), pageSize, (page-1)*pageSize)
	if err != nil {
		return Page[domain.Media]{}, fmt.Errorf("search catalog: %w", err)
	}
	items := nonNil(result.Items)
	for i := range items {
		items[i] = applyMediaTranslation(ctx, items[i], s.translator, s.writer)
	}
	return Page[domain.Media]{Page: page, PageSize: pageSize, Total: result.Total, Items: items}, nil
}

// SearchActor 校验分页并查询演员影片；空演员名不会退化为全目录查询。
func (s *CatalogQueryService) SearchActor(ctx context.Context, name string, page, pageSize int) (Page[domain.Media], error) {
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Media]{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Page[domain.Media]{Page: page, PageSize: pageSize, Items: []domain.Media{}}, nil
	}
	result, err := s.repository.SearchActor(ctx, name, pageSize, (page-1)*pageSize)
	if err != nil {
		return Page[domain.Media]{}, fmt.Errorf("search actor: %w", err)
	}
	items := nonNil(result.Items)
	for i := range items {
		items[i] = applyMediaTranslation(ctx, items[i], s.translator, s.writer)
	}
	return Page[domain.Media]{Page: page, PageSize: pageSize, Total: result.Total, Items: items}, nil
}

// Rank returns one validated page in the exact order stored by the selected legacy rank snapshot.
func (s *CatalogQueryService) Rank(ctx context.Context, rankType string, page, pageSize int, filters ports.MediaListQuery) (Page[domain.Media], error) {
	if err := validateMediaFilters(&filters); err != nil {
		return Page[domain.Media]{}, err
	}
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Media]{}, err
	}
	rankType = strings.TrimSpace(rankType)
	if rankType == "" {
		rankType = "monthly"
	}
	result, err := s.repository.Rank(ctx, rankType, pageSize, (page-1)*pageSize, filters)
	if err != nil {
		return Page[domain.Media]{}, fmt.Errorf("list rank: %w", err)
	}
	items := nonNil(result.Items)
	for i := range items {
		items[i] = applyMediaTranslation(ctx, items[i], s.translator, s.writer)
	}
	return Page[domain.Media]{Page: page, PageSize: pageSize, Total: result.Total, Items: items}, nil
}

// ReleaseToday returns media whose database release date equals the current local calendar day.
func (s *CatalogQueryService) ReleaseToday(ctx context.Context, page, pageSize int, filters ports.MediaListQuery) (Page[domain.Media], error) {
	if err := validateMediaFilters(&filters); err != nil {
		return Page[domain.Media]{}, err
	}
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Media]{}, err
	}
	releaseDate := time.Now().Format("2006-01-02")
	result, err := s.repository.ReleaseToday(ctx, releaseDate, pageSize, (page-1)*pageSize, filters)
	if err != nil {
		return Page[domain.Media]{}, fmt.Errorf("list today's releases: %w", err)
	}
	items := nonNil(result.Items)
	for i := range items {
		items[i] = applyMediaTranslation(ctx, items[i], s.translator, s.writer)
	}
	return Page[domain.Media]{Page: page, PageSize: pageSize, Total: result.Total, Items: items}, nil
}

// Recommend ranks filtered database media from the user's persisted subscription and library profile.
func (s *CatalogQueryService) Recommend(ctx context.Context, page, pageSize int, filters ports.MediaListQuery) (Page[domain.Media], error) {
	if err := validateMediaFilters(&filters); err != nil {
		return Page[domain.Media]{}, err
	}
	if err := validatePagination(page, pageSize); err != nil {
		return Page[domain.Media]{}, err
	}
	now := time.Now()
	startDate := now.AddDate(0, -1, 0).Format("2006-01-02")
	endDate := now.AddDate(0, 1, 0).Format("2006-01-02")
	result, err := s.repository.Recommend(ctx, startDate, endDate, pageSize, (page-1)*pageSize, filters)
	if err != nil {
		return Page[domain.Media]{}, fmt.Errorf("list recommendations: %w", err)
	}
	items := nonNil(result.Items)
	for i := range items {
		items[i] = applyMediaTranslation(ctx, items[i], s.translator, s.writer)
	}
	return Page[domain.Media]{Page: page, PageSize: pageSize, Total: result.Total, Items: items}, nil
}
