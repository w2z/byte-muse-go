package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

func TestSQLiteMediaRepositoryContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openMigratedSQLiteStore(t, ctx)
	t.Cleanup(func() { _ = store.Close() })

	var mediaRepo ports.MediaRepository = store.Media()
	createdAt := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	media := domain.Media{
		ID:                 "01K5Y8DB7W3YB6AJ6F8W9P8V4P",
		Code:               "BM-001",
		Title:              "脱敏影片一",
		TranslatedTitle:    stringPtr("ByteMuse 脱敏影片一"),
		PosterURL:          stringPtr("https://example.invalid/poster-001.jpg"),
		ReleaseDate:        stringPtr("2026-09-22"),
		DurationMinutes:    intValuePtr(120),
		SubscriptionStatus: domain.SubscriptionStatusNone,
		LibraryStatus:      domain.LibraryStatusUnknown,
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
	}
	if err := store.UpsertMedia(ctx, media); err != nil {
		t.Fatalf("upsert media fixture: %v", err)
	}

	got, err := mediaRepo.Get(ctx, media.ID)
	if err != nil {
		t.Fatalf("get media: %v", err)
	}
	assertDomainMediaEqual(t, media, got)

	page, err := mediaRepo.List(ctx, ports.MediaListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("list media: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("media page total/items = %d/%d, want 1/1", page.Total, len(page.Items))
	}
	assertDomainMediaEqual(t, media, page.Items[0])

	_, err = mediaRepo.Get(ctx, "01K5Y8QZ9WZZZZZZZZZZZZZZZZ")
	if !errors.Is(err, ports.ErrMediaNotFound) {
		t.Fatalf("missing media error = %v, want ErrMediaNotFound", err)
	}
}

func TestSQLiteSubscriptionRepositoryIdempotencyAndCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openMigratedSQLiteStore(t, ctx)
	t.Cleanup(func() { _ = store.Close() })

	media := fixtureMedia("01K5YA5S1ZX64E6KYR5T4J3B8G", "BM-SUB")
	if err := store.UpsertMedia(ctx, media); err != nil {
		t.Fatalf("upsert media fixture: %v", err)
	}

	var repo ports.SubscriptionRepository = store.Subscriptions()
	request := ports.CreateSubscription{
		IdempotencyKey: "subscribe-BM-SUB-001",
		MediaID:        media.ID,
		Mode:           domain.SubscriptionModeStrict,
		Filter:         map[string]any{"resolution": "1080p", "min_seeders": float64(3)},
	}
	created, wasCreated, err := repo.Create(ctx, request)
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if !wasCreated || created.ID == "" || created.Status != domain.SubscriptionStatusActive || created.Version != 1 {
		t.Fatalf("created subscription = %+v, created=%v", created, wasCreated)
	}

	replayed, wasCreated, err := repo.Create(ctx, request)
	if err != nil {
		t.Fatalf("replay subscription: %v", err)
	}
	if wasCreated || replayed.ID != created.ID {
		t.Fatalf("replayed subscription = %+v, created=%v; want same id %s and created=false", replayed, wasCreated, created.ID)
	}

	conflict := request
	conflict.Mode = domain.SubscriptionModePreload
	_, _, err = repo.Create(ctx, conflict)
	if !errors.Is(err, ports.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v, want ErrIdempotencyConflict", err)
	}

	page, err := repo.List(ctx, ports.SubscriptionListQuery{Limit: 10, Status: domain.SubscriptionStatusActive})
	if err != nil {
		t.Fatalf("list active subscriptions: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("active subscription page = %+v", page)
	}

	canceled, changed, err := repo.Cancel(ctx, created.ID)
	if err != nil {
		t.Fatalf("cancel subscription: %v", err)
	}
	if !changed || canceled.Status != domain.SubscriptionStatusCanceled || canceled.Version != 2 {
		t.Fatalf("canceled subscription = %+v, changed=%v", canceled, changed)
	}
	again, changed, err := repo.Cancel(ctx, created.ID)
	if err != nil {
		t.Fatalf("repeat cancel subscription: %v", err)
	}
	if changed || again.ID != created.ID || again.Version != 2 {
		t.Fatalf("repeat cancel = %+v, changed=%v", again, changed)
	}

	mediaAfterCancel, err := store.Media().Get(ctx, media.ID)
	if err != nil {
		t.Fatalf("get media after cancel: %v", err)
	}
	if mediaAfterCancel.SubscriptionStatus != domain.SubscriptionStatusCanceled {
		t.Fatalf("media subscription status = %q, want canceled", mediaAfterCancel.SubscriptionStatus)
	}
}

func TestSQLiteDownloadRepositoryAndReadiness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openMigratedSQLiteStore(t, ctx)
	t.Cleanup(func() { _ = store.Close() })

	media := fixtureMedia("01K5YB0F1W2X3Y4Z5A6B7C8D9E", "BM-DOWN")
	if err := store.UpsertMedia(ctx, media); err != nil {
		t.Fatalf("upsert media fixture: %v", err)
	}
	if err := store.InsertDownloadTaskForTest(ctx, domain.DownloadTask{
		ID:           "01K5YB3W7M8N9P0Q1R2S3T4V5W",
		MediaID:      media.ID,
		Status:       domain.DownloadStatusQueued,
		ExternalID:   stringPtr("external-task-1"),
		ErrorMessage: nil,
		CreatedAt:    media.CreatedAt,
		UpdatedAt:    media.UpdatedAt,
	}); err != nil {
		t.Fatalf("insert download fixture: %v", err)
	}

	var repo ports.DownloadRepository = store.Downloads()
	page, err := repo.List(ctx, ports.DownloadListQuery{Limit: 10, Status: domain.DownloadStatusQueued})
	if err != nil {
		t.Fatalf("list downloads: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Status != domain.DownloadStatusQueued || page.Items[0].MediaID != media.ID {
		t.Fatalf("download page = %+v", page)
	}

	var readiness ports.ReadinessProbe = store.ReadinessProbe()
	if err := readiness.Ready(ctx); err != nil {
		t.Fatalf("readiness: %v", err)
	}
}

func TestDevelopmentSeedIsExplicitMediaOnlyAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openMigratedSQLiteStore(t, ctx)
	t.Cleanup(func() { _ = store.Close() })

	page, err := store.Media().List(ctx, ports.MediaListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("list before seed: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("default migrated database has %d media rows, want 0", page.Total)
	}

	if err := SeedDevelopment(ctx, store); err != nil {
		t.Fatalf("seed development: %v", err)
	}
	if err := SeedDevelopment(ctx, store); err != nil {
		t.Fatalf("seed development idempotently: %v", err)
	}

	page, err = store.Media().List(ctx, ports.MediaListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("list after seed: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("seeded media total/items = %d/%d, want 2/2", page.Total, len(page.Items))
	}
	for _, item := range page.Items {
		if item.Code != "BM-DEMO-001" && item.Code != "BM-DEMO-002" {
			t.Fatalf("unexpected seed code %q", item.Code)
		}
	}
	assertTableCount(t, store.SQLDB(), "subscriptions", 0)
	assertTableCount(t, store.SQLDB(), "download_tasks", 0)
}

func openMigratedSQLiteStore(t *testing.T, ctx context.Context) Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bytemuse-test.db")
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: path})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		t.Fatalf("migrate sqlite store: %v", err)
	}
	return store
}

func fixtureMedia(id string, code string) domain.Media {
	createdAt := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	return domain.Media{
		ID:                 id,
		Code:               code,
		Title:              "脱敏影片 " + code,
		TranslatedTitle:    stringPtr("ByteMuse " + code),
		PosterURL:          stringPtr("https://example.invalid/" + code + ".jpg"),
		ReleaseDate:        stringPtr("2026-09-22"),
		DurationMinutes:    intValuePtr(120),
		SubscriptionStatus: domain.SubscriptionStatusNone,
		LibraryStatus:      domain.LibraryStatusUnknown,
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
	}
}

func assertDomainMediaEqual(t *testing.T, want domain.Media, got domain.Media) {
	t.Helper()
	if got.ID != want.ID || got.Code != want.Code || got.Title != want.Title {
		t.Fatalf("media identity/text mismatch\nwant: %+v\n got: %+v", want, got)
	}
	if derefString(got.TranslatedTitle) != derefString(want.TranslatedTitle) || derefString(got.PosterURL) != derefString(want.PosterURL) || derefString(got.ReleaseDate) != derefString(want.ReleaseDate) || derefInt(got.DurationMinutes) != derefInt(want.DurationMinutes) {
		t.Fatalf("media optional fields mismatch\nwant: %+v\n got: %+v", want, got)
	}
	if got.SubscriptionStatus != want.SubscriptionStatus || got.LibraryStatus != want.LibraryStatus {
		t.Fatalf("media status mismatch\nwant: %+v\n got: %+v", want, got)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("media timestamps mismatch\nwant: %s/%s\n got: %s/%s", want.CreatedAt, want.UpdatedAt, got.CreatedAt, got.UpdatedAt)
	}
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
