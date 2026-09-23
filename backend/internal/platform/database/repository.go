package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

type sqlMediaRepository struct {
	dialect Dialect
	exec    sqlExecutor
}

func (r *sqlMediaRepository) List(ctx context.Context, query ports.MediaListQuery) (domain.MediaPage, error) {
	limit, offset := normalizePagination(query.Limit, query.Offset)
	var total int
	if err := r.exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&total); err != nil {
		return domain.MediaPage{}, err
	}
	rows, err := r.exec.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM media ORDER BY updated_at DESC, id ASC LIMIT %s OFFSET %s`, mediaColumns(), placeholder(r.dialect, 1), placeholder(r.dialect, 2)), limit, offset)
	if err != nil {
		return domain.MediaPage{}, err
	}
	defer rows.Close()
	items, err := scanMediaRows(rows)
	if err != nil {
		return domain.MediaPage{}, err
	}
	return domain.MediaPage{Items: items, Total: total}, nil
}

func (r *sqlMediaRepository) Get(ctx context.Context, id string) (domain.Media, error) {
	media, err := scanMedia(r.exec.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM media WHERE id = %s`, mediaColumns(), placeholder(r.dialect, 1)), id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Media{}, ports.ErrMediaNotFound
	}
	return media, err
}

func (r *sqlMediaRepository) upsert(ctx context.Context, media domain.Media) error {
	if err := validateMedia(media); err != nil {
		return err
	}
	columns := []string{"id", "code", "title", "translated_title", "poster_url", "release_date", "duration_minutes", "subscription_status", "library_status", "created_at", "updated_at"}
	base := fmt.Sprintf(`INSERT INTO media (%s) VALUES (%s)`, strings.Join(columns, ", "), placeholders(r.dialect, len(columns), 1))
	updates := []string{"code", "title", "translated_title", "poster_url", "release_date", "duration_minutes", "subscription_status", "library_status", "created_at", "updated_at"}
	query := base + upsertClause(r.dialect, "id", updates)
	_, err := r.exec.ExecContext(ctx, query, media.ID, media.Code, media.Title, nullString(media.TranslatedTitle), nullString(media.PosterURL), nullString(media.ReleaseDate), nullInt(media.DurationMinutes), media.SubscriptionStatus, media.LibraryStatus, encodeTime(media.CreatedAt, r.dialect), encodeTime(media.UpdatedAt, r.dialect))
	return err
}

type sqlSubscriptionRepository struct {
	dialect Dialect
	exec    sqlExecutor
}

func (r *sqlSubscriptionRepository) Create(ctx context.Context, request ports.CreateSubscription) (domain.Subscription, bool, error) {
	if request.IdempotencyKey == "" || request.MediaID == "" {
		return domain.Subscription{}, false, fmt.Errorf("idempotency key and media id are required")
	}
	if !validSubscriptionMode(request.Mode) {
		return domain.Subscription{}, false, fmt.Errorf("invalid subscription mode %q", request.Mode)
	}
	filterJSON, idempotencyHash, err := subscriptionPayload(request)
	if err != nil {
		return domain.Subscription{}, false, err
	}
	existing, err := r.getByIdempotencyKey(ctx, request.IdempotencyKey)
	if err == nil {
		if existing.idempotencyHash != idempotencyHash {
			return domain.Subscription{}, false, ports.ErrIdempotencyConflict
		}
		return existing.Subscription, false, nil
	}
	if !errors.Is(err, ports.ErrSubscriptionNotFound) {
		return domain.Subscription{}, false, err
	}
	now := time.Now().UTC()
	item := domain.Subscription{
		ID:        newSortableID(),
		MediaID:   request.MediaID,
		Status:    domain.SubscriptionStatusActive,
		Mode:      request.Mode,
		Filter:    cloneFilter(request.Filter),
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}
	columns := []string{"id", "media_id", "status", "mode", "filter_json", "idempotency_key", "idempotency_hash", "created_at", "updated_at", "version"}
	_, err = r.exec.ExecContext(ctx, fmt.Sprintf(`INSERT INTO subscriptions (%s) VALUES (%s)`, strings.Join(columns, ", "), placeholders(r.dialect, len(columns), 1)), item.ID, item.MediaID, item.Status, item.Mode, string(filterJSON), request.IdempotencyKey, idempotencyHash, encodeTime(item.CreatedAt, r.dialect), encodeTime(item.UpdatedAt, r.dialect), item.Version)
	if err != nil {
		return domain.Subscription{}, false, err
	}
	if err := updateMediaSubscriptionStatus(ctx, r.exec, r.dialect, item.MediaID, domain.SubscriptionStatusActive, now); err != nil {
		return domain.Subscription{}, false, err
	}
	return item, true, nil
}

func (r *sqlSubscriptionRepository) Cancel(ctx context.Context, id string) (domain.Subscription, bool, error) {
	current, err := r.get(ctx, id)
	if err != nil {
		return domain.Subscription{}, false, err
	}
	if current.Status == domain.SubscriptionStatusCanceled {
		return current, false, nil
	}
	now := time.Now().UTC()
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	version := current.Version + 1
	result, err := r.exec.ExecContext(ctx, fmt.Sprintf(`UPDATE subscriptions SET status = %s, updated_at = %s, version = %s WHERE id = %s`, placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4)), domain.SubscriptionStatusCanceled, encodeTime(now, r.dialect), version, id)
	if err != nil {
		return domain.Subscription{}, false, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return domain.Subscription{}, false, ports.ErrSubscriptionNotFound
	}
	if err := updateMediaSubscriptionStatus(ctx, r.exec, r.dialect, current.MediaID, domain.SubscriptionStatusCanceled, now); err != nil {
		return domain.Subscription{}, false, err
	}
	current.Status = domain.SubscriptionStatusCanceled
	current.UpdatedAt = now
	current.Version = version
	return current, true, nil
}

func (r *sqlSubscriptionRepository) List(ctx context.Context, query ports.SubscriptionListQuery) (domain.SubscriptionPage, error) {
	limit, offset := normalizePagination(query.Limit, query.Offset)
	where, args := statusWhere(r.dialect, string(query.Status))
	var total int
	if err := r.exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscriptions`+where, args...).Scan(&total); err != nil {
		return domain.SubscriptionPage{}, err
	}
	args = append(args, limit, offset)
	rows, err := r.exec.QueryContext(ctx, fmt.Sprintf(`SELECT %s, idempotency_hash FROM subscriptions%s ORDER BY updated_at DESC, id ASC LIMIT %s OFFSET %s`, subscriptionColumns(), where, placeholder(r.dialect, len(args)-1), placeholder(r.dialect, len(args))), args...)
	if err != nil {
		return domain.SubscriptionPage{}, err
	}
	defer rows.Close()
	items, err := scanSubscriptionRows(rows)
	if err != nil {
		return domain.SubscriptionPage{}, err
	}
	return domain.SubscriptionPage{Items: items, Total: total}, nil
}

func (r *sqlSubscriptionRepository) get(ctx context.Context, id string) (domain.Subscription, error) {
	item, _, err := scanSubscription(r.exec.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s, idempotency_hash FROM subscriptions WHERE id = %s`, subscriptionColumns(), placeholder(r.dialect, 1)), id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Subscription{}, ports.ErrSubscriptionNotFound
	}
	return item, err
}

type subscriptionWithHash struct {
	domain.Subscription
	idempotencyHash string
}

func (r *sqlSubscriptionRepository) getByIdempotencyKey(ctx context.Context, key string) (subscriptionWithHash, error) {
	item, hash, err := scanSubscription(r.exec.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s, idempotency_hash FROM subscriptions WHERE idempotency_key = %s`, subscriptionColumns(), placeholder(r.dialect, 1)), key))
	if errors.Is(err, sql.ErrNoRows) {
		return subscriptionWithHash{}, ports.ErrSubscriptionNotFound
	}
	return subscriptionWithHash{Subscription: item, idempotencyHash: hash}, err
}

type sqlDownloadRepository struct {
	dialect Dialect
	exec    sqlExecutor
}

func (r *sqlDownloadRepository) List(ctx context.Context, query ports.DownloadListQuery) (domain.DownloadPage, error) {
	limit, offset := normalizePagination(query.Limit, query.Offset)
	where, args := statusWhere(r.dialect, string(query.Status))
	var total int
	if err := r.exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_tasks`+where, args...).Scan(&total); err != nil {
		return domain.DownloadPage{}, err
	}
	args = append(args, limit, offset)
	rows, err := r.exec.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM download_tasks%s ORDER BY updated_at DESC, id ASC LIMIT %s OFFSET %s`, downloadColumns(), where, placeholder(r.dialect, len(args)-1), placeholder(r.dialect, len(args))), args...)
	if err != nil {
		return domain.DownloadPage{}, err
	}
	defer rows.Close()
	items, err := scanDownloadRows(rows)
	if err != nil {
		return domain.DownloadPage{}, err
	}
	return domain.DownloadPage{Items: items, Total: total}, nil
}

func (r *sqlDownloadRepository) insert(ctx context.Context, task domain.DownloadTask) error {
	if !validDownloadStatus(task.Status) {
		return fmt.Errorf("invalid download status %q", task.Status)
	}
	_, err := r.exec.ExecContext(ctx, fmt.Sprintf(`INSERT INTO download_tasks (id, media_id, status, external_id, error_message, created_at, updated_at) VALUES (%s)`, placeholders(r.dialect, 7, 1)), task.ID, task.MediaID, task.Status, nullString(task.ExternalID), nullString(task.ErrorMessage), encodeTime(task.CreatedAt, r.dialect), encodeTime(task.UpdatedAt, r.dialect))
	return err
}

type sqlReadinessProbe struct {
	dialect Dialect
	exec    sqlExecutor
}

func (p *sqlReadinessProbe) Ready(ctx context.Context) error {
	var version int64
	if err := p.exec.QueryRowContext(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version); err != nil {
		return err
	}
	var mediaCount int
	return p.exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&mediaCount)
}

func validateMedia(media domain.Media) error {
	if media.ID == "" || media.Code == "" || media.Title == "" {
		return fmt.Errorf("media id, code and title are required")
	}
	if !validSubscriptionStatus(media.SubscriptionStatus) || !validLibraryStatus(media.LibraryStatus) {
		return fmt.Errorf("invalid media status %q/%q", media.SubscriptionStatus, media.LibraryStatus)
	}
	if media.CreatedAt.IsZero() || media.UpdatedAt.IsZero() {
		return fmt.Errorf("media timestamps are required")
	}
	return nil
}

func validSubscriptionStatus(status domain.SubscriptionStatus) bool {
	switch status {
	case domain.SubscriptionStatusNone, domain.SubscriptionStatusActive, domain.SubscriptionStatusCanceled:
		return true
	default:
		return false
	}
}

func validSubscriptionMode(mode domain.SubscriptionMode) bool {
	switch mode {
	case domain.SubscriptionModeStrict, domain.SubscriptionModePreload:
		return true
	default:
		return false
	}
}

func validDownloadStatus(status domain.DownloadStatus) bool {
	switch status {
	case domain.DownloadStatusQueued, domain.DownloadStatusSearching, domain.DownloadStatusSubmitted, domain.DownloadStatusDownloading, domain.DownloadStatusCompleted, domain.DownloadStatusFailed, domain.DownloadStatusUnknown:
		return true
	default:
		return false
	}
}

func validLibraryStatus(status domain.LibraryStatus) bool {
	switch status {
	case domain.LibraryStatusUnknown, domain.LibraryStatusAbsent, domain.LibraryStatusPresent:
		return true
	default:
		return false
	}
}

func normalizePagination(limit int, offset int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func statusWhere(dialect Dialect, status string) (string, []any) {
	if status == "" {
		return "", nil
	}
	return " WHERE status = " + placeholder(dialect, 1), []any{status}
}

func updateMediaSubscriptionStatus(ctx context.Context, exec sqlExecutor, dialect Dialect, mediaID string, status domain.SubscriptionStatus, updatedAt time.Time) error {
	_, err := exec.ExecContext(ctx, fmt.Sprintf(`UPDATE media SET subscription_status = %s, updated_at = %s WHERE id = %s`, placeholder(dialect, 1), placeholder(dialect, 2), placeholder(dialect, 3)), status, encodeTime(updatedAt, dialect), mediaID)
	return err
}

func subscriptionPayload(request ports.CreateSubscription) ([]byte, string, error) {
	filter := cloneFilter(request.Filter)
	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256([]byte(request.MediaID + "\x00" + string(request.Mode) + "\x00" + string(filterJSON)))
	return filterJSON, hex.EncodeToString(hash[:]), nil
}

func cloneFilter(filter map[string]any) map[string]any {
	if filter == nil {
		return map[string]any{}
	}
	clone := make(map[string]any, len(filter))
	for key, value := range filter {
		clone[key] = value
	}
	return clone
}

func newSortableID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes[:])[:26]
}

func upsertClause(dialect Dialect, conflictColumn string, updates []string) string {
	switch dialect {
	case DialectPostgres:
		sets := make([]string, len(updates))
		for i, column := range updates {
			sets[i] = fmt.Sprintf("%s = EXCLUDED.%s", column, column)
		}
		return " ON CONFLICT (" + conflictColumn + ") DO UPDATE SET " + strings.Join(sets, ", ")
	case DialectMySQL:
		sets := make([]string, len(updates))
		for i, column := range updates {
			sets[i] = fmt.Sprintf("%s = VALUES(%s)", column, column)
		}
		return " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	default:
		sets := make([]string, len(updates))
		for i, column := range updates {
			sets[i] = fmt.Sprintf("%s = excluded.%s", column, column)
		}
		return " ON CONFLICT(" + conflictColumn + ") DO UPDATE SET " + strings.Join(sets, ", ")
	}
}

func mediaColumns() string {
	return "id, code, title, translated_title, poster_url, release_date, duration_minutes, subscription_status, library_status, created_at, updated_at"
}

func subscriptionColumns() string {
	return "id, media_id, status, mode, filter_json, created_at, updated_at, version"
}

func downloadColumns() string {
	return "id, media_id, status, external_id, error_message, created_at, updated_at"
}

type rowScanner interface{ Scan(dest ...any) error }

func scanMediaRows(rows *sql.Rows) ([]domain.Media, error) {
	var items []domain.Media
	for rows.Next() {
		item, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanMedia(row rowScanner) (domain.Media, error) {
	var item domain.Media
	var translatedTitle, posterURL, releaseDate any
	var duration sql.NullInt64
	var createdAt, updatedAt any
	if err := row.Scan(&item.ID, &item.Code, &item.Title, &translatedTitle, &posterURL, &releaseDate, &duration, &item.SubscriptionStatus, &item.LibraryStatus, &createdAt, &updatedAt); err != nil {
		return domain.Media{}, err
	}
	item.TranslatedTitle, _ = valueToStringPtr(translatedTitle)
	item.PosterURL, _ = valueToStringPtr(posterURL)
	item.ReleaseDate, _ = valueToStringPtr(releaseDate)
	if duration.Valid {
		v := int(duration.Int64)
		item.DurationMinutes = &v
	}
	created, err := valueToTime(createdAt)
	if err != nil {
		return domain.Media{}, fmt.Errorf("created_at: %w", err)
	}
	updated, err := valueToTime(updatedAt)
	if err != nil {
		return domain.Media{}, fmt.Errorf("updated_at: %w", err)
	}
	item.CreatedAt = created
	item.UpdatedAt = updated
	return item, nil
}

func scanSubscriptionRows(rows *sql.Rows) ([]domain.Subscription, error) {
	var items []domain.Subscription
	for rows.Next() {
		item, _, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanSubscription(row rowScanner) (domain.Subscription, string, error) {
	var item domain.Subscription
	var filterJSON string
	var createdAt, updatedAt any
	var hash sql.NullString
	if err := row.Scan(&item.ID, &item.MediaID, &item.Status, &item.Mode, &filterJSON, &createdAt, &updatedAt, &item.Version, &hash); err != nil {
		return domain.Subscription{}, "", err
	}
	if filterJSON == "" {
		item.Filter = map[string]any{}
	} else if err := json.Unmarshal([]byte(filterJSON), &item.Filter); err != nil {
		return domain.Subscription{}, "", err
	}
	created, err := valueToTime(createdAt)
	if err != nil {
		return domain.Subscription{}, "", fmt.Errorf("created_at: %w", err)
	}
	updated, err := valueToTime(updatedAt)
	if err != nil {
		return domain.Subscription{}, "", fmt.Errorf("updated_at: %w", err)
	}
	item.CreatedAt = created
	item.UpdatedAt = updated
	return item, hash.String, nil
}

func scanDownloadRows(rows *sql.Rows) ([]domain.DownloadTask, error) {
	var items []domain.DownloadTask
	for rows.Next() {
		var item domain.DownloadTask
		var externalID, errorMessage any
		var createdAt, updatedAt any
		if err := rows.Scan(&item.ID, &item.MediaID, &item.Status, &externalID, &errorMessage, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.ExternalID, _ = valueToStringPtr(externalID)
		item.ErrorMessage, _ = valueToStringPtr(errorMessage)
		created, err := valueToTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("created_at: %w", err)
		}
		updated, err := valueToTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("updated_at: %w", err)
		}
		item.CreatedAt = created
		item.UpdatedAt = updated
		items = append(items, item)
	}
	return items, rows.Err()
}

func nullString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func encodeTime(value time.Time, dialect Dialect) any {
	if dialect == DialectSQLite {
		return value.UTC().Format(time.RFC3339Nano)
	}
	return value.UTC()
}

func valueToStringPtr(value any) (*string, bool) {
	switch v := value.(type) {
	case nil:
		return nil, false
	case string:
		return &v, true
	case []byte:
		s := string(v)
		return &s, true
	case time.Time:
		s := v.Format("2006-01-02")
		return &s, true
	default:
		s := fmt.Sprint(v)
		return &s, true
	}
}

func valueToTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v.UTC(), nil
	case string:
		return parseTimeString(v)
	case []byte:
		return parseTimeString(string(v))
	default:
		return time.Time{}, fmt.Errorf("unsupported time value %T", value)
	}
}

func parseTimeString(value string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q", value)
}
