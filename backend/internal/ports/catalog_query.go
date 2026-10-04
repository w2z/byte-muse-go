package ports

import (
	"bytemuse/backend/internal/domain"
	"context"
)

// CatalogQueryRepository exposes read-only search and ordered rank projections.
type CatalogQueryRepository interface {
	Search(ctx context.Context, query string, limit, offset int) (domain.MediaPage, error)
	Rank(ctx context.Context, rankType string, limit, offset int, filters MediaListQuery) (domain.MediaPage, error)
	ReleaseToday(ctx context.Context, releaseDate string, limit, offset int, filters MediaListQuery) (domain.MediaPage, error)
	Recommend(ctx context.Context, startDate, endDate string, limit, offset int, filters MediaListQuery) (domain.MediaPage, error)
}
