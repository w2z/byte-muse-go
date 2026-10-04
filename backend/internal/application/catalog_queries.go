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
