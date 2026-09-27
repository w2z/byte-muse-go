package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
)

// LegacyCatalogImportResult summarizes an idempotent import from the legacy code table.
type LegacyCatalogImportResult struct {
	Imported              int
	Skipped               int
	ActiveSubscriptions   int
	CanceledSubscriptions int
}

type legacyCatalogRow struct {
	code, title                             string
	poster, banner, previewURL              sql.NullString
	duration                                any
	releaseDate, genres, casts              sql.NullString
	producer, publisher, series, stillPhoto sql.NullString
	status, mode                            string
	filter, createdAt, updatedAt            sql.NullString
	star                                    sql.NullInt64
	cnTitle, localBanner, localStillPhoto   sql.NullString
}

// ImportLegacyCatalog reads the legacy SQLite database in read-only mode and preserves raw legacy semantics in metadata.
func ImportLegacyCatalog(ctx context.Context, target Store, dialect Dialect, legacyPath string) (LegacyCatalogImportResult, error) {
	source, err := sql.Open("sqlite", "file:"+legacyPath+"?mode=ro")
	if err != nil {
		return LegacyCatalogImportResult{}, fmt.Errorf("open legacy database: %w", err)
	}
	defer source.Close()
	if err := source.PingContext(ctx); err != nil {
		return LegacyCatalogImportResult{}, fmt.Errorf("ping legacy database: %w", err)
	}
	query := "SELECT code, title, poster, banner, preview_url, duration, release_date, genres, casts, producer, publisher, series, still_photo, status, mode, filter, create_time, update_time, star, cn_title, local_banner, local_still_photo FROM code ORDER BY code"
	rows, err := source.QueryContext(ctx, query)
	if err != nil {
		return LegacyCatalogImportResult{}, fmt.Errorf("read legacy catalog: %w", err)
	}
	defer rows.Close()
	items := make([]legacyCatalogRow, 0, 1024)
	result := LegacyCatalogImportResult{}
	for rows.Next() {
		var item legacyCatalogRow
		if err := rows.Scan(&item.code, &item.title, &item.poster, &item.banner, &item.previewURL, &item.duration, &item.releaseDate, &item.genres, &item.casts, &item.producer, &item.publisher, &item.series, &item.stillPhoto, &item.status, &item.mode, &item.filter, &item.createdAt, &item.updatedAt, &item.star, &item.cnTitle, &item.localBanner, &item.localStillPhoto); err != nil {
			return result, fmt.Errorf("scan legacy catalog: %w", err)
		}
		item.code = strings.TrimSpace(item.code)
		item.title = strings.TrimSpace(item.title)
		if item.code == "" {
			result.Skipped++
			continue
		}
		if item.title == "" {
			item.title = item.code
		}
		items = append(items, item)
		result.Imported++
		switch item.status {
		case "SUBSCRIBE":
			result.ActiveSubscriptions++
		case "CANCEL":
			result.CanceledSubscriptions++
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate legacy catalog: %w", err)
	}
	tx, err := target.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin legacy catalog import: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, item := range items {
		if err := importLegacyCatalogRow(ctx, tx, dialect, item); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit legacy catalog import: %w", err)
	}
	committed = true
	return result, nil
}

func importLegacyCatalogRow(ctx context.Context, tx *sql.Tx, dialect Dialect, item legacyCatalogRow) error {
	mediaID, err := mediaIDForLegacyCode(ctx, tx, dialect, item.code)
	if err != nil {
		return err
	}
	createdAt := parseLegacyCatalogTime(item.createdAt.String, time.Unix(0, 0).UTC())
	updatedAt := parseLegacyCatalogTime(item.updatedAt.String, createdAt)
	subscriptionStatus := domain.SubscriptionStatusNone
	if item.status == "SUBSCRIBE" {
		subscriptionStatus = domain.SubscriptionStatusActive
	} else if item.status == "CANCEL" {
		subscriptionStatus = domain.SubscriptionStatusCanceled
	}
	columns := []string{"id", "code", "title", "translated_title", "poster_url", "release_date", "duration_minutes", "subscription_status", "library_status", "created_at", "updated_at"}
	query := "INSERT INTO media (" + strings.Join(columns, ", ") + ") VALUES (" + placeholders(dialect, len(columns), 1) + ")" + upsertClause(dialect, "id", columns[1:])
	_, err = tx.ExecContext(ctx, query, mediaID, item.code, item.title, nullableString(item.cnTitle), nullableString(item.poster), nullableString(item.releaseDate), parseLegacyDuration(item.duration), subscriptionStatus, domain.LibraryStatusUnknown, encodeTime(createdAt, dialect), encodeTime(updatedAt, dialect))
	if err != nil {
		return fmt.Errorf("upsert legacy media %q: %w", item.code, err)
	}
	if err := upsertLegacyMetadata(ctx, tx, dialect, mediaID, item); err != nil {
		return err
	}
	if item.status == "SUBSCRIBE" || item.status == "CANCEL" {
		if err := upsertLegacySubscription(ctx, tx, dialect, mediaID, item.status, item.mode, item.filter.String, createdAt, updatedAt); err != nil {
			return fmt.Errorf("upsert legacy subscription %q: %w", item.code, err)
		}
	}
	return nil
}

func mediaIDForLegacyCode(ctx context.Context, tx *sql.Tx, dialect Dialect, code string) (string, error) {
	var existing string
	err := tx.QueryRowContext(ctx, "SELECT id FROM media WHERE code = "+placeholder(dialect, 1), code).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("find existing legacy media %q: %w", code, err)
	}
	return legacyStableID("media", code), nil
}

func upsertLegacyMetadata(ctx context.Context, tx *sql.Tx, dialect Dialect, mediaID string, item legacyCatalogRow) error {
	columns := []string{"media_id", "code", "banner_url", "preview_url", "genres", "casts", "producer", "publisher", "series", "still_photo", "local_banner", "local_still_photo", "legacy_status", "legacy_mode", "legacy_filter", "legacy_star"}
	query := "INSERT INTO legacy_media_metadata (" + strings.Join(columns, ", ") + ") VALUES (" + placeholders(dialect, len(columns), 1) + ")" + upsertClause(dialect, "media_id", columns[1:])
	_, err := tx.ExecContext(ctx, query, mediaID, item.code, nullableString(item.banner), nullableString(item.previewURL), nullableString(item.genres), nullableString(item.casts), nullableString(item.producer), nullableString(item.publisher), nullableString(item.series), nullableString(item.stillPhoto), nullableString(item.localBanner), nullableString(item.localStillPhoto), item.status, item.mode, nullableString(item.filter), nullableInt64(item.star))
	if err != nil {
		return fmt.Errorf("upsert legacy metadata %q: %w", item.code, err)
	}
	return nil
}

func upsertLegacySubscription(ctx context.Context, tx *sql.Tx, dialect Dialect, mediaID, legacyStatus, legacyMode, filter string, createdAt, updatedAt time.Time) error {
	status := domain.SubscriptionStatusActive
	if legacyStatus == "CANCEL" {
		status = domain.SubscriptionStatusCanceled
	}
	mode := domain.SubscriptionModeStrict
	if strings.EqualFold(legacyMode, "PRELOAD") {
		mode = domain.SubscriptionModePreload
	}
	filter = normalizeLegacyFilter(filter)
	id := legacyStableID("subscription", mediaID)
	key := "legacy:" + mediaID
	hash := sha256.Sum256([]byte(mediaID + "\x00" + string(mode) + "\x00" + filter))
	columns := []string{"id", "media_id", "status", "mode", "filter_json", "idempotency_key", "idempotency_hash", "created_at", "updated_at", "version"}
	query := "INSERT INTO subscriptions (" + strings.Join(columns, ", ") + ") VALUES (" + placeholders(dialect, len(columns), 1) + ")" + upsertClause(dialect, "id", columns[1:])
	_, err := tx.ExecContext(ctx, query, id, mediaID, status, mode, filter, key, hex.EncodeToString(hash[:]), encodeTime(createdAt, dialect), encodeTime(updatedAt, dialect), 1)
	return err
}

func normalizeLegacyFilter(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !json.Valid([]byte(value)) {
		return "{}"
	}
	return value
}
func legacyStableID(namespace, value string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + value))
	return hex.EncodeToString(sum[:])[:26]
}
func parseLegacyCatalogTime(value string, fallback time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return fallback
}
func nullableInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

// parseLegacyDuration normalizes SQLite's mixed INTEGER, minute-text, and H:MM:SS values to whole minutes.
func parseLegacyDuration(value any) any {
	if value == nil {
		return nil
	}
	if number, ok := value.(int64); ok {
		return number
	}
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" || raw == "-" {
		return nil
	}
	if parts := strings.Split(raw, ":"); len(parts) == 3 {
		hours, hourErr := strconv.Atoi(parts[0])
		minutes, minuteErr := strconv.Atoi(parts[1])
		seconds, secondErr := strconv.Atoi(parts[2])
		if hourErr == nil && minuteErr == nil && secondErr == nil && hours >= 0 && minutes >= 0 && minutes < 60 && seconds >= 0 && seconds < 60 {
			totalSeconds := hours*3600 + minutes*60 + seconds
			return int64((totalSeconds + 59) / 60)
		}
	}
	digitEnd := 0
	for digitEnd < len(raw) && raw[digitEnd] >= '0' && raw[digitEnd] <= '9' {
		digitEnd++
	}
	if digitEnd == 0 {
		return nil
	}
	minutes, err := strconv.ParseInt(raw[:digitEnd], 10, 64)
	if err != nil {
		return nil
	}
	return minutes
}
