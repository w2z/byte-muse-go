package database

import (
	"context"
	"time"

	"bytemuse/backend/internal/domain"
)

// SeedDevelopment explicitly inserts two 脱敏 catalog records for local demonstrations. It is never called by Open or Migrate.
func SeedDevelopment(ctx context.Context, store Store) error {
	createdAt := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	media := []domain.Media{
		{
			ID:                 "01K5Y9H8S7M2B4C6D8E0F1G2H3",
			Code:               "BM-DEMO-001",
			Title:              "脱敏演示影片一",
			TranslatedTitle:    stringPtr("ByteMuse Demo Movie One"),
			PosterURL:          stringPtr("https://example.invalid/poster-demo-001.jpg"),
			ReleaseDate:        stringPtr("2026-09-20"),
			DurationMinutes:    intValuePtr(118),
			SubscriptionStatus: domain.SubscriptionStatusNone,
			LibraryStatus:      domain.LibraryStatusUnknown,
			CreatedAt:          createdAt,
			UpdatedAt:          createdAt,
		},
		{
			ID:                 "01K5Y9J0NJQ6R8S1T3V5W7X9YB",
			Code:               "BM-DEMO-002",
			Title:              "脱敏演示影片二",
			TranslatedTitle:    stringPtr("ByteMuse Demo Movie Two"),
			PosterURL:          stringPtr("https://example.invalid/poster-demo-002.jpg"),
			ReleaseDate:        stringPtr("2026-09-21"),
			DurationMinutes:    intValuePtr(96),
			SubscriptionStatus: domain.SubscriptionStatusNone,
			LibraryStatus:      domain.LibraryStatusUnknown,
			CreatedAt:          createdAt,
			UpdatedAt:          createdAt,
		},
	}
	return store.WithTx(ctx, func(tx Store) error {
		for _, item := range media {
			if err := tx.UpsertMedia(ctx, item); err != nil {
				return err
			}
		}
		return nil
	})
}

func stringPtr(value string) *string { return &value }
func intValuePtr(value int) *int     { return &value }
