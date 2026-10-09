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
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

type sqlMediaRepository struct {
	dialect Dialect
	exec    sqlExecutor
}

func (r *sqlMediaRepository) List(ctx context.Context, query ports.MediaListQuery) (domain.MediaPage, error) {
	limit, offset := normalizePagination(query.Limit, query.Offset)
	where, args := mediaListWhere(r.dialect, query)
	var total int
	if err := r.exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM media m"+where, args...).Scan(&total); err != nil {
		return domain.MediaPage{}, err
	}
	selectArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.exec.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM media m %s%s ORDER BY m.updated_at DESC, m.id ASC LIMIT %s OFFSET %s", mediaProjectionColumns("m"), mediaProjectionJoins(), where, placeholder(r.dialect, len(selectArgs)-1), placeholder(r.dialect, len(selectArgs))), selectArgs...)
	if err != nil {
		return domain.MediaPage{}, err
	}
	defer rows.Close()
	items, err := scanMediaProjectionRows(rows)
	if err != nil {
		return domain.MediaPage{}, err
	}
	return domain.MediaPage{Items: items, Total: total}, nil
}

// mediaListWhere 为影片视图共用服务端筛选；VR 按番号判断，统计与分页复用相同条件。
func mediaListWhere(dialect Dialect, query ports.MediaListQuery) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 4)
	add := func(sql string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(sql, placeholder(dialect, len(args))))
	}
	if term := strings.TrimSpace(query.Search); term != "" {
		pattern := "%" + strings.ToUpper(term) + "%"
		args = append(args, pattern, pattern, pattern)
		clauses = append(clauses, fmt.Sprintf("(UPPER(m.code) LIKE %s OR UPPER(m.title) LIKE %s OR UPPER(COALESCE(m.translated_title, '')) LIKE %s)", placeholder(dialect, len(args)-2), placeholder(dialect, len(args)-1), placeholder(dialect, len(args))))
	}
	if value := strings.TrimSpace(query.SubscriptionStatus); value != "" {
		if value == string(domain.SubscriptionStatusNone) {
			clauses = append(clauses, "NOT EXISTS (SELECT 1 FROM subscriptions sf WHERE sf.media_id = m.id AND sf.status = 'active')")
		} else {
			add("CASE WHEN EXISTS (SELECT 1 FROM subscriptions sf WHERE sf.media_id = m.id AND sf.status = 'active') THEN 'active' ELSE m.subscription_status END = %s", value)
		}
	}
	if value := strings.TrimSpace(query.DownloadStatus); value != "" {
		add("COALESCE((SELECT df.status FROM download_tasks df WHERE df.media_id = m.id ORDER BY df.updated_at DESC, df.id DESC LIMIT 1), 'unknown') = %s", value)
	}
	if value := strings.TrimSpace(query.LibraryStatus); value != "" {
		add("m.library_status = %s", value)
	}
	if value := strings.TrimSpace(query.VideoType); value == "unknown" {
		clauses = append(clauses, "m.video_type IS NULL")
	} else if value != "" {
		add("m.video_type = %s", value)
	}
	switch query.VR {
	case "hide":
		clauses = append(clauses, "UPPER(m.code) NOT LIKE '%VR%'")
	case "only":
		clauses = append(clauses, "UPPER(m.code) LIKE '%VR%'")
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (r *sqlMediaRepository) Get(ctx context.Context, id string) (domain.Media, error) {
	media, err := scanMediaProjection(r.exec.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM media m %s WHERE m.id = %s`, mediaProjectionColumns("m"), mediaProjectionJoins(), placeholder(r.dialect, 1)), id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Media{}, ports.ErrMediaNotFound
	}
	if err != nil {
		return domain.Media{}, err
	}
	// 扩展资料仅在详情读取，避免给每个分页卡片附加元数据查询。
	details := &domain.MediaDetails{Actors: []string{}, Tags: []string{}}
	var casts, genres, producer, publisher, series sql.NullString
	err = r.exec.QueryRowContext(ctx, "SELECT casts, genres, producer, publisher, series FROM legacy_media_metadata WHERE media_id = "+placeholder(r.dialect, 1), id).Scan(&casts, &genres, &producer, &publisher, &series)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Media{}, err
	}
	if err == nil {
		details.Actors = splitRecommendationValues(casts.String)
		details.Tags = splitRecommendationValues(genres.String)
		for field, value := range map[**string]sql.NullString{&details.Producer: producer, &details.Publisher: publisher, &details.Series: series} {
			if value.Valid && strings.TrimSpace(value.String) != "" {
				*field = stringPointer(strings.TrimSpace(value.String))
			}
		}
	}
	// 只采用明确分类；流出类别不能说明是否有码或有马赛克。
	if media.VideoType != nil {
		switch *media.VideoType {
		case "censored", "uncensored", "uncensored_cracked":
			censored, mosaic := *media.VideoType != "uncensored", *media.VideoType == "censored"
			details.Censored, details.Mosaic = &censored, &mosaic
		}
	}
	media.Details = details
	return media, nil
}

// UpdateTranslatedTitle 只更新媒体译文和更新时间，避免覆盖并发写入的其他字段。
func (r *sqlMediaRepository) UpdateTranslatedTitle(ctx context.Context, id, translatedTitle string) error {
	translatedTitle = strings.TrimSpace(translatedTitle)
	if id == "" || translatedTitle == "" {
		return fmt.Errorf("media id and translated title are required")
	}
	now := time.Now().UTC()
	result, err := r.exec.ExecContext(ctx, fmt.Sprintf(`UPDATE media SET translated_title = %s, updated_at = %s WHERE id = %s`, placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3)), translatedTitle, encodeTime(now, r.dialect), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ports.ErrMediaNotFound
	}
	return nil
}

func (r *sqlMediaRepository) upsert(ctx context.Context, media domain.Media) error {
	if err := validateMedia(media); err != nil {
		return err
	}
	columns := []string{"id", "code", "title", "translated_title", "poster_url", "release_date", "duration_minutes", "subscription_status", "library_status", "created_at", "updated_at", "video_type"}
	base := fmt.Sprintf(`INSERT INTO media (%s) VALUES (%s)`, strings.Join(columns, ", "), placeholders(r.dialect, len(columns), 1))
	updates := []string{"code", "title", "translated_title", "poster_url", "release_date", "duration_minutes", "subscription_status", "library_status", "created_at", "updated_at", "video_type"}
	query := base + upsertClause(r.dialect, "id", updates)
	_, err := r.exec.ExecContext(ctx, query, media.ID, media.Code, media.Title, nullString(media.TranslatedTitle), nullString(media.PosterURL), nullString(media.ReleaseDate), nullInt(media.DurationMinutes), media.SubscriptionStatus, media.LibraryStatus, encodeTime(media.CreatedAt, r.dialect), encodeTime(media.UpdatedAt, r.dialect), nullString(media.VideoType))
	return err
}

type sqlSubscriptionRepository struct {
	dialect Dialect
	exec    sqlExecutor
	db      *sql.DB
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
	if exists, err := r.hasActiveForMedia(ctx, request.MediaID); err != nil {
		return domain.Subscription{}, false, err
	} else if exists {
		return domain.Subscription{}, false, ports.ErrActiveSubscriptionExists
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
		if activeSubscriptionConstraintError(err) {
			return domain.Subscription{}, false, ports.ErrActiveSubscriptionExists
		}
		return domain.Subscription{}, false, err
	}
	if err := updateMediaSubscriptionStatus(ctx, r.exec, r.dialect, item.MediaID, domain.SubscriptionStatusActive, now); err != nil {
		return domain.Subscription{}, false, err
	}
	// 与 List 保持同一返回形状：订阅成功通知的标题与推送封面直接用这里带出的影片快照，调用方不必再查一次。
	// 订阅此时已提交，快照只影响文案与配图，取不到时只记录日志，不把已成功的创建报成失败。
	items := []domain.Subscription{item}
	if err := r.attachMedia(ctx, items); err != nil {
		// 番号正是这次没读到的东西，此时手上只剩内部 media_id，对用户没有任何意义；
		// 因此只说明「影片信息获取失败」并带上原因，不回退到内部标识。
		logging.Error(logging.CategorySubscription, "订阅已保存，影片信息获取失败", "error", err.Error())
		return item, true, nil
	}
	return items[0], true, nil
}

// hasActiveForMedia provides a clear domain error before the database uniqueness constraint
// handles the concurrent-create race.
func (r *sqlSubscriptionRepository) hasActiveForMedia(ctx context.Context, mediaID string) (bool, error) {
	var marker int
	err := r.exec.QueryRowContext(ctx, fmt.Sprintf(`SELECT 1 FROM subscriptions WHERE media_id = %s AND status = %s LIMIT 1`, placeholder(r.dialect, 1), placeholder(r.dialect, 2)), mediaID, domain.SubscriptionStatusActive).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func activeSubscriptionConstraintError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "idx_subscriptions_one_active_per_media") ||
		strings.Contains(message, "subscriptions.active_media_id") ||
		strings.Contains(message, "unique constraint failed: subscriptions.media_id")
}

// Cancel removes the subscription record and clears the media's active subscription status.
// Pending queue/search tasks for the media are removed in the same transaction; submitted or later tasks are retained as history.
func (r *sqlSubscriptionRepository) Cancel(ctx context.Context, id string) (domain.Subscription, bool, error) {
	if r.db != nil {
		if _, inTransaction := r.exec.(*sql.Tx); !inTransaction {
			tx, err := r.db.BeginTx(ctx, nil)
			if err != nil {
				return domain.Subscription{}, false, err
			}
			transactional := &sqlSubscriptionRepository{dialect: r.dialect, exec: tx}
			item, changed, err := transactional.cancel(ctx, id)
			if err != nil {
				_ = tx.Rollback()
				return domain.Subscription{}, false, err
			}
			if err := tx.Commit(); err != nil {
				return domain.Subscription{}, false, err
			}
			return item, changed, nil
		}
	}
	return r.cancel(ctx, id)
}

func (r *sqlSubscriptionRepository) cancel(ctx context.Context, id string) (domain.Subscription, bool, error) {
	current, err := r.get(ctx, id)
	if err != nil {
		return domain.Subscription{}, false, err
	}
	now := time.Now().UTC()
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	result, err := r.exec.ExecContext(ctx, fmt.Sprintf(`DELETE FROM subscriptions WHERE id = %s`, placeholder(r.dialect, 1)), id)
	if err != nil {
		return domain.Subscription{}, false, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return domain.Subscription{}, false, ports.ErrSubscriptionNotFound
	}
	// 取消订阅必须同时清掉它登记的待执行搜索，否则搜索队列会留下无主行，重新订阅时被误用。
	if _, err := r.exec.ExecContext(ctx, fmt.Sprintf(`DELETE FROM subscription_scans WHERE subscription_id = %s`, placeholder(r.dialect, 1)), id); err != nil {
		return domain.Subscription{}, false, err
	}
	if _, err := r.exec.ExecContext(ctx, fmt.Sprintf(`DELETE FROM download_tasks WHERE media_id = %s AND status IN (%s, %s)`, placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3)), current.MediaID, domain.DownloadStatusQueued, domain.DownloadStatusSearching); err != nil {
		return domain.Subscription{}, false, err
	}
	if err := updateMediaSubscriptionStatus(ctx, r.exec, r.dialect, current.MediaID, domain.SubscriptionStatusNone, now); err != nil {
		return domain.Subscription{}, false, err
	}
	current.Status = domain.SubscriptionStatusCanceled
	current.UpdatedAt = now
	// 与 List/Create 保持同一返回形状：日志、通知与渠道卡片都用番号标识订阅，调用方不必再按 media_id 反查。
	// 取消已提交，快照只影响展示，取不到时只记录日志，不把已成功的取消报成失败。
	items := []domain.Subscription{current}
	if err := r.attachMedia(ctx, items); err != nil {
		// 同 Create：不回退到内部 media_id，只说明影片信息获取失败。
		logging.Error(logging.CategorySubscription, "订阅已取消，影片信息获取失败", "error", err.Error())
		return current, true, nil
	}
	return items[0], true, nil
}

// Update atomically replaces editable rules when the caller still holds the current version.
func (r *sqlSubscriptionRepository) Update(ctx context.Context, request ports.UpdateSubscription) (domain.Subscription, error) {
	if request.ID == "" || request.ExpectedVersion < 1 || !validSubscriptionMode(request.Mode) {
		return domain.Subscription{}, fmt.Errorf("invalid subscription update")
	}
	current, err := r.get(ctx, request.ID)
	if err != nil {
		return domain.Subscription{}, err
	}
	if current.Status != domain.SubscriptionStatusActive {
		return domain.Subscription{}, ports.ErrSubscriptionInactive
	}
	if current.Version != request.ExpectedVersion {
		return domain.Subscription{}, ports.ErrVersionConflict
	}
	filterJSON, err := json.Marshal(cloneFilter(request.Filter))
	if err != nil {
		return domain.Subscription{}, err
	}
	now := time.Now().UTC()
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	result, err := r.exec.ExecContext(ctx, fmt.Sprintf(
		`UPDATE subscriptions SET mode = %s, filter_json = %s, updated_at = %s, version = %s WHERE id = %s AND status = %s AND version = %s`,
		placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4),
		placeholder(r.dialect, 5), placeholder(r.dialect, 6), placeholder(r.dialect, 7),
	), request.Mode, string(filterJSON), encodeTime(now, r.dialect), current.Version+1, request.ID, domain.SubscriptionStatusActive, request.ExpectedVersion)
	if err != nil {
		return domain.Subscription{}, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return domain.Subscription{}, ports.ErrVersionConflict
	}
	current.Mode = request.Mode
	current.Filter = cloneFilter(request.Filter)
	current.UpdatedAt = now
	current.Version++
	// 与 Cancel 同理：编辑结果同样带出影片快照，调用方统一用番号标识这条订阅。
	items := []domain.Subscription{current}
	if err := r.attachMedia(ctx, items); err != nil {
		// 同 Create：不回退到内部 media_id，只说明影片信息获取失败。
		logging.Error(logging.CategorySubscription, "订阅已编辑，影片信息获取失败", "error", err.Error())
		return current, nil
	}
	return items[0], nil
}

func (r *sqlSubscriptionRepository) List(ctx context.Context, query ports.SubscriptionListQuery) (domain.SubscriptionPage, error) {
	limit, offset := normalizePagination(query.Limit, query.Offset)
	if query.Status == "" {
		query.Status = domain.SubscriptionStatusActive
	}
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
	if err := rows.Close(); err != nil {
		return domain.SubscriptionPage{}, err
	}
	if err := r.attachMedia(ctx, items); err != nil {
		return domain.SubscriptionPage{}, err
	}
	return domain.SubscriptionPage{Items: items, Total: total}, nil
}

func (r *sqlSubscriptionRepository) attachMedia(ctx context.Context, items []domain.Subscription) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]any, 0, len(items))
	byMedia := make(map[string][]int, len(items))
	for index := range items {
		if _, seen := byMedia[items[index].MediaID]; !seen {
			ids = append(ids, items[index].MediaID)
		}
		byMedia[items[index].MediaID] = append(byMedia[items[index].MediaID], index)
	}
	rows, err := r.exec.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM media m %s WHERE m.id IN (%s)`, mediaProjectionColumns("m"), mediaProjectionJoins(), placeholders(r.dialect, len(ids), 1)), ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	media, err := scanMediaProjectionRows(rows)
	if err != nil {
		return err
	}
	for index := range media {
		for _, subscriptionIndex := range byMedia[media[index].ID] {
			copy := media[index]
			items[subscriptionIndex].Media = &copy
		}
	}
	return nil
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
	var conditions []string
	var args []any
	add := func(column string, value any, operator string) {
		args = append(args, value)
		conditions = append(conditions, column+operator+placeholder(r.dialect, len(args)))
	}
	if query.Status != "" {
		add("status", query.Status, " = ")
	}
	if query.MediaID != "" {
		add("media_id", query.MediaID, " = ")
	}
	if query.TransferStatus != "" {
		if query.TransferStatus == "failed" {
			conditions = append(conditions, "(status='failed' OR transfer_status='failed')")
		} else {
			add("transfer_status", query.TransferStatus, " = ")
		}
	}
	for _, bound := range []struct {
		column, operator string
		value            *time.Time
	}{
		{"added_at", " >= ", query.AddedFrom}, {"added_at", " < ", query.AddedTo},
		{"completed_at", " >= ", query.CompletedFrom}, {"completed_at", " < ", query.CompletedTo},
	} {
		if bound.value != nil {
			if r.dialect == DialectSQLite {
				args = append(args, encodeTime(*bound.value, r.dialect))
				conditions = append(conditions, "julianday("+bound.column+")"+bound.operator+"julianday("+placeholder(r.dialect, len(args))+")")
			} else {
				add(bound.column, encodeTime(*bound.value, r.dialect), bound.operator)
			}
		}
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int
	if err := r.exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_tasks`+where, args...).Scan(&total); err != nil {
		return domain.DownloadPage{}, err
	}
	args = append(args, limit, offset)
	statement := fmt.Sprintf(`SELECT %s FROM download_tasks%s ORDER BY updated_at DESC, id ASC`, downloadColumns(), where)
	if query.All {
		args = args[:len(args)-2]
	} else {
		statement += fmt.Sprintf(` LIMIT %s OFFSET %s`, placeholder(r.dialect, len(args)-1), placeholder(r.dialect, len(args)))
	}
	rows, err := r.exec.QueryContext(ctx, statement, args...)
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
	if media.VideoType != nil && !domain.ValidVideoType(*media.VideoType) {
		return fmt.Errorf("invalid video type")
	}
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
	if limit <= 0 || limit > ports.MaxPageSize {
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

func mediaProjectionColumns(alias string) string {
	base := strings.Replace(prefixedMediaColumns(alias), alias+".subscription_status", "CASE WHEN s.id IS NOT NULL THEN 'active' ELSE "+alias+".subscription_status END", 1)
	return base + ", " + alias + ".video_type, lm.banner_url, lm.preview_url, lm.still_photo, " +
		"s.id, s.media_id, s.status, s.mode, s.filter_json, s.created_at, s.updated_at, s.version, d.status"
}

func mediaProjectionJoins() string {
	return `LEFT JOIN legacy_media_metadata lm ON lm.media_id = m.id
		LEFT JOIN subscriptions s ON s.id = (
			SELECT s2.id FROM subscriptions s2
			WHERE s2.media_id = m.id AND s2.status = 'active'
			ORDER BY s2.updated_at DESC, s2.id ASC LIMIT 1
		)
		LEFT JOIN download_tasks d ON d.id = (
			SELECT d2.id FROM download_tasks d2
			WHERE d2.media_id = m.id
			ORDER BY d2.updated_at DESC, d2.id ASC LIMIT 1
		)`
}

// mediaCoverColumn 返回媒体封面的列表达式：优先旧版横幅图，缺失时回退海报图，都没有则为空串。
// 取值规则与 application.MediaCover 一致，推送配图不出现第二种口径；使用时必须同时连接 mediaCoverJoin。
func mediaCoverColumn(mediaAlias string) string {
	return fmt.Sprintf("COALESCE(NULLIF(lm.banner_url,''), %s.poster_url, '')", mediaAlias)
}

// mediaCoverJoin 是 mediaCoverColumn 依赖的元数据连接；legacy_media_metadata 以 media_id 为主键，连接为一次主键查找。
func mediaCoverJoin(mediaAlias string) string {
	return fmt.Sprintf("LEFT JOIN legacy_media_metadata lm ON lm.media_id = %s.id", mediaAlias)
}

func subscriptionColumns() string {
	return "id, media_id, status, mode, filter_json, created_at, updated_at, version"
}

func downloadColumns() string {
	return "id, media_id, status, external_id, error_message, created_at, updated_at, source_site, source_kind, downloader, info_hash, transfer_status, added_at, completed_at, (SELECT code FROM media WHERE media.id=download_tasks.media_id)"
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

func scanMediaProjectionRows(rows *sql.Rows) ([]domain.Media, error) {
	items := make([]domain.Media, 0)
	for rows.Next() {
		item, err := scanMediaProjection(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanMediaProjection(row rowScanner) (domain.Media, error) {
	var item domain.Media
	var translatedTitle, posterURL, releaseDate any
	var duration sql.NullInt64
	var createdAt, updatedAt any
	var bannerURL, previewURL, stillPhoto sql.NullString
	var subscriptionID, subscriptionMediaID, subscriptionStatus, subscriptionMode, filterJSON sql.NullString
	var subscriptionCreatedAt, subscriptionUpdatedAt any
	var subscriptionVersion sql.NullInt64
	var downloadStatus sql.NullString
	if err := row.Scan(
		&item.ID, &item.Code, &item.Title, &translatedTitle, &posterURL, &releaseDate, &duration,
		&item.SubscriptionStatus, &item.LibraryStatus, &createdAt, &updatedAt,
		&item.VideoType,
		&bannerURL, &previewURL, &stillPhoto,
		&subscriptionID, &subscriptionMediaID, &subscriptionStatus, &subscriptionMode, &filterJSON,
		&subscriptionCreatedAt, &subscriptionUpdatedAt, &subscriptionVersion, &downloadStatus,
	); err != nil {
		return domain.Media{}, err
	}
	item.TranslatedTitle, _ = valueToStringPtr(translatedTitle)
	item.PosterURL, _ = valueToStringPtr(posterURL)
	item.ReleaseDate, _ = valueToStringPtr(releaseDate)
	if duration.Valid {
		value := int(duration.Int64)
		item.DurationMinutes = &value
	}
	if bannerURL.Valid && strings.TrimSpace(bannerURL.String) != "" {
		item.BannerURL = stringPointer(bannerURL.String)
	}
	if previewURL.Valid && strings.TrimSpace(previewURL.String) != "" {
		item.PreviewURL = stringPointer(previewURL.String)
	}
	item.StillPhotos = parseStillPhotos(stillPhoto.String)
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
	if subscriptionID.Valid {
		active := domain.Subscription{
			ID: subscriptionID.String, MediaID: subscriptionMediaID.String,
			Status: domain.SubscriptionStatus(subscriptionStatus.String), Mode: domain.SubscriptionMode(subscriptionMode.String),
			Version: int(subscriptionVersion.Int64), Filter: map[string]any{},
		}
		if filterJSON.Valid && filterJSON.String != "" {
			if err := json.Unmarshal([]byte(filterJSON.String), &active.Filter); err != nil {
				return domain.Media{}, fmt.Errorf("subscription filter: %w", err)
			}
		}
		active.CreatedAt, err = valueToTime(subscriptionCreatedAt)
		if err != nil {
			return domain.Media{}, fmt.Errorf("subscription created_at: %w", err)
		}
		active.UpdatedAt, err = valueToTime(subscriptionUpdatedAt)
		if err != nil {
			return domain.Media{}, fmt.Errorf("subscription updated_at: %w", err)
		}
		item.ActiveSubscription = &active
	}
	if downloadStatus.Valid {
		status := domain.DownloadStatus(downloadStatus.String)
		item.DownloadStatus = &status
	}
	item.DisplayStatus = domain.ResolveMediaDisplayStatus(item.LibraryStatus, item.SubscriptionStatus, item.DownloadStatus)
	return item, nil
}

func parseStillPhotos(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil {
		values = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func stringPointer(value string) *string { return &value }

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
		var externalID, errorMessage, sourceSite, sourceKind, downloader, infoHash, transferStatus, addedAt, completedAt any
		var createdAt, updatedAt any
		if err := rows.Scan(&item.ID, &item.MediaID, &item.Status, &externalID, &errorMessage, &createdAt, &updatedAt, &sourceSite, &sourceKind, &downloader, &infoHash, &transferStatus, &addedAt, &completedAt, &item.Code); err != nil {
			return nil, err
		}
		item.ExternalID, _ = valueToStringPtr(externalID)
		item.ErrorMessage, _ = valueToStringPtr(errorMessage)
		item.SourceSite, _ = valueToStringPtr(sourceSite)
		item.SourceKind, _ = valueToStringPtr(sourceKind)
		item.Downloader, _ = valueToStringPtr(downloader)
		item.InfoHash, _ = valueToStringPtr(infoHash)
		item.TransferStatus, _ = valueToStringPtr(transferStatus)
		if addedAt != nil {
			at, err := valueToTime(addedAt)
			if err != nil {
				return nil, err
			}
			item.AddedAt = &at
		}
		if completedAt != nil {
			at, err := valueToTime(completedAt)
			if err != nil {
				return nil, err
			}
			item.CompletedAt = &at
		}
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
