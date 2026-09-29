package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// SubscriptionDownloadRepository writes the durable search and submission state.
type SubscriptionDownloadRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewSubscriptionDownloadRepository constructs the task repository from the application store.
func NewSubscriptionDownloadRepository(db *sql.DB, dialect Dialect) *SubscriptionDownloadRepository {
	return &SubscriptionDownloadRepository{db: db, dialect: dialect}
}

// SaveTransferStates updates only matching qBittorrent tasks and never infers missing torrents as failures.
// 返回本次首次进入终态（completed / failed）的任务，供通知去重：轮询会重复上报同一状态，
// 只有旧值与新值不同才算一次跃迁。
func (r *SubscriptionDownloadRepository) SaveTransferStates(ctx context.Context, states []ports.TransferState) ([]ports.TransferTransition, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	update := fmt.Sprintf("UPDATE download_tasks SET transfer_status=%s,added_at=COALESCE(added_at,%s),completed_at=COALESCE(completed_at,%s) WHERE downloader='qbittorrent' AND LOWER(info_hash)=%s AND status IN ('submitted','downloading','completed') AND lease_token IS NULL AND updated_at<=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4), placeholder(r.dialect, 5))
	current := fmt.Sprintf("SELECT t.id,COALESCE(t.transfer_status,''),COALESCE(m.code,''),COALESCE(NULLIF(m.translated_title,''),m.title,''),"+mediaCoverColumn("m")+" FROM download_tasks t LEFT JOIN media m ON m.id=t.media_id "+mediaCoverJoin("m")+" WHERE t.downloader='qbittorrent' AND LOWER(t.info_hash)=%s AND t.status IN ('submitted','downloading','completed') AND t.lease_token IS NULL AND t.updated_at<=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2))
	transitions := make([]ports.TransferTransition, 0, len(states))
	for _, state := range states {
		if state.Hash == "" {
			continue
		}
		var added, completed any
		if state.AddedAt != nil {
			added = encodeTime(*state.AddedAt, r.dialect)
		}
		if state.CompletedAt != nil {
			completed = encodeTime(*state.CompletedAt, r.dialect)
		}
		observedAt := state.ObservedAt
		if observedAt.IsZero() {
			observedAt = time.Now().UTC()
		}
		var taskID, previous, code, title, cover string
		scanErr := tx.QueryRowContext(ctx, current, state.Hash, encodeTime(observedAt, r.dialect)).Scan(&taskID, &previous, &code, &title, &cover)
		if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
			return nil, scanErr
		}
		if _, err = tx.ExecContext(ctx, update, state.Status, added, completed, state.Hash, encodeTime(observedAt, r.dialect)); err != nil {
			return nil, err
		}
		if scanErr != nil || previous == state.Status {
			continue
		}
		if state.Status == "completed" || state.Status == "failed" {
			transitions = append(transitions, ports.TransferTransition{TaskID: taskID, Code: code, Title: title, Cover: cover, Status: state.Status})
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return transitions, nil
}

// Enqueue creates one pending task for an active subscription unless a nonfailed attempt already exists.
// origin 记录本次请求的发起方：用户请求命中已排队的定时任务时把来源升级为 user，
// 避免用户显式发起的下载被定时任务先建立的任务吸收而失去失败通知。
func (r *SubscriptionDownloadRepository) Enqueue(ctx context.Context, subscriptionID string, origin ports.DownloadOrigin) (domain.DownloadTask, error) {
	task, _, e := r.enqueue(ctx, subscriptionID, origin)
	return task, e
}

// normalizeOrigin 把发起方收敛到持久化词表：只有 user 是用户显式发起，其余一律按 schedule 处理。
// 空值或其他取值来自调用方笔误时按定时任务落库，不会因为违反列约束让整个下载队列写入失败。
func normalizeOrigin(origin ports.DownloadOrigin) ports.DownloadOrigin {
	if origin == ports.DownloadOriginUser {
		return ports.DownloadOriginUser
	}
	return ports.DownloadOriginSchedule
}

func (r *SubscriptionDownloadRepository) enqueue(ctx context.Context, subscriptionID string, origin ports.DownloadOrigin) (domain.DownloadTask, bool, error) {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.DownloadTask{}, false, e
	}
	defer tx.Rollback()
	var mediaID string
	e = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT media_id FROM subscriptions WHERE id=%s AND status='active'", placeholder(r.dialect, 1)), subscriptionID).Scan(&mediaID)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.DownloadTask{}, false, ports.ErrSubscriptionNotFound
	}
	if e != nil {
		return domain.DownloadTask{}, false, e
	}
	var item domain.DownloadTask
	var createdAt, updatedAt any
	var existingOrigin ports.DownloadOrigin
	e = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT id,media_id,status,origin,created_at,updated_at FROM download_tasks WHERE subscription_id=%s AND status IN ('queued','searching','unknown','submitted','downloading','completed') ORDER BY created_at DESC LIMIT 1", placeholder(r.dialect, 1)), subscriptionID).Scan(&item.ID, &item.MediaID, &item.Status, &existingOrigin, &createdAt, &updatedAt)
	if e == nil {
		item.CreatedAt, _ = valueToTime(createdAt)
		item.UpdatedAt, _ = valueToTime(updatedAt)
		if normalizeOrigin(origin) == ports.DownloadOriginUser && normalizeOrigin(existingOrigin) != ports.DownloadOriginUser {
			if _, e = tx.ExecContext(ctx, fmt.Sprintf("UPDATE download_tasks SET origin=%s WHERE id=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2)), ports.DownloadOriginUser, item.ID); e != nil {
				return domain.DownloadTask{}, false, e
			}
		}
		return item, false, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return domain.DownloadTask{}, false, e
	}
	now := time.Now().UTC()
	item = domain.DownloadTask{ID: newSortableID(), MediaID: mediaID, Status: domain.DownloadStatusQueued, CreatedAt: now, UpdatedAt: now}
	_, e = tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO download_tasks (id,media_id,subscription_id,status,created_at,updated_at,origin) VALUES (%s)", placeholders(r.dialect, 7, 1)), item.ID, item.MediaID, subscriptionID, item.Status, encodeTime(now, r.dialect), encodeTime(now, r.dialect), string(normalizeOrigin(origin)))
	if e != nil {
		return domain.DownloadTask{}, false, e
	}
	return item, true, tx.Commit()
}

// EnqueueActive schedules every active subscription without duplicating unfinished work.
// 批量扫描统一按 schedule 来源落库：它的失败是正常状态，不推送通知。
func (r *SubscriptionDownloadRepository) EnqueueActive(ctx context.Context) (int, error) {
	rows, e := r.db.QueryContext(ctx, "SELECT id FROM subscriptions WHERE status='active' ORDER BY id")
	if e != nil {
		return 0, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	if e == nil {
		e = rows.Err()
	}
	_ = rows.Close()
	if e != nil {
		return 0, e
	}
	count := 0
	for _, id := range ids {
		_, created, e := r.enqueue(ctx, id, ports.DownloadOriginSchedule)
		if e != nil {
			return count, e
		}
		if created {
			count++
		}
	}
	return count, nil
}

// Claim reserves a queued or expired searching attempt and returns its current subscription rules.
func (r *SubscriptionDownloadRepository) Claim(ctx context.Context, now time.Time) (*ports.SubscriptionDownloadAttempt, error) {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	query := fmt.Sprintf("SELECT d.id,d.media_id,m.code,COALESCE(NULLIF(m.translated_title,''),m.title,''),"+mediaCoverColumn("m")+",s.mode,s.filter_json,d.origin FROM download_tasks d JOIN subscriptions s ON s.id=d.subscription_id AND s.status='active' JOIN media m ON m.id=d.media_id "+mediaCoverJoin("m")+" WHERE (d.status='queued' OR (d.status='searching' AND d.lease_until<%s)) ORDER BY d.created_at,d.id LIMIT 1", placeholder(r.dialect, 1))
	var a ports.SubscriptionDownloadAttempt
	var filterJSON string
	e = tx.QueryRowContext(ctx, query, now.UnixMilli()).Scan(&a.ID, &a.MediaID, &a.Code, &a.Title, &a.Cover, &a.Mode, &filterJSON, &a.Origin)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if e = unmarshalFilter(filterJSON, &a.Filter); e != nil {
		return nil, e
	}
	a.LeaseToken = newSortableID()
	update := fmt.Sprintf("UPDATE download_tasks SET status='searching',lease_until=%s,lease_token=%s,attempt_count=attempt_count+1,updated_at=%s WHERE id=%s AND (status='queued' OR (status='searching' AND lease_until<%s))", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4), placeholder(r.dialect, 5))
	result, e := tx.ExecContext(ctx, update, now.Add(2*time.Minute).UnixMilli(), a.LeaseToken, encodeTime(now, r.dialect), a.ID, now.UnixMilli())
	if e != nil {
		return nil, e
	}
	affected, e := result.RowsAffected()
	if e != nil {
		return nil, e
	}
	if affected != 1 {
		return nil, nil
	}
	return &a, tx.Commit()
}

// SetCandidate stores the exact selected resource before any external submission.
func (r *SubscriptionDownloadRepository) SetCandidate(ctx context.Context, a ports.SubscriptionDownloadAttempt, site, kind, uri, hash, downloader string, passed bool) error {
	q := fmt.Sprintf("UPDATE download_tasks SET status='unknown',source_site=%s,source_kind=%s,resource_uri=%s,info_hash=%s,downloader=%s,filter_passed=%s,updated_at=%s WHERE id=%s AND lease_token=%s AND status='searching' AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.id=download_tasks.subscription_id AND s.status='active')", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4), placeholder(r.dialect, 5), placeholder(r.dialect, 6), placeholder(r.dialect, 7), placeholder(r.dialect, 8), placeholder(r.dialect, 9))
	result, e := r.db.ExecContext(ctx, q, site, kind, uri, hash, downloader, passed, encodeTime(time.Now().UTC(), r.dialect), a.ID, a.LeaseToken)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("download lease or subscription changed")
	}
	return nil
}

// FinishSearch makes an unsuccessful search retryable on the next scheduler run.
func (r *SubscriptionDownloadRepository) FinishSearch(ctx context.Context, a ports.SubscriptionDownloadAttempt, message string) error {
	q := fmt.Sprintf("UPDATE download_tasks SET status='failed',error_message=%s,lease_until=NULL,lease_token=NULL,updated_at=%s WHERE id=%s AND lease_token=%s AND status='searching'", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4))
	_, e := r.db.ExecContext(ctx, q, message, encodeTime(time.Now().UTC(), r.dialect), a.ID, a.LeaseToken)
	return e
}

// FinishSubmission records a confirmed client result; nil success leaves an unknown task for reconciliation.
func (r *SubscriptionDownloadRepository) FinishSubmission(ctx context.Context, p ports.PendingSubmission, success bool, errorMessage string) error {
	status := "failed"
	if success {
		status = "submitted"
	}
	q := fmt.Sprintf("UPDATE download_tasks SET status=%s,error_message=%s,lease_until=NULL,lease_token=NULL,updated_at=%s WHERE id=%s AND status='unknown' AND lease_token=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4), placeholder(r.dialect, 5))
	_, e := r.db.ExecContext(ctx, q, status, errorMessage, encodeTime(time.Now().UTC(), r.dialect), p.ID, p.LeaseToken)
	return e
}

// ClaimPending reserves an ambiguous submission for hash reconciliation.
func (r *SubscriptionDownloadRepository) ClaimPending(ctx context.Context, now time.Time) (*ports.PendingSubmission, error) {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var p ports.PendingSubmission
	q := fmt.Sprintf("SELECT d.id,d.resource_uri,d.info_hash,d.downloader,COALESCE(d.source_site,''),COALESCE(m.code,''),COALESCE(NULLIF(m.translated_title,''),m.title,''),"+mediaCoverColumn("m")+" FROM download_tasks d LEFT JOIN media m ON m.id=d.media_id "+mediaCoverJoin("m")+" WHERE d.status='unknown' AND (d.lease_until IS NULL OR d.lease_until<%s) ORDER BY d.updated_at,d.id LIMIT 1", placeholder(r.dialect, 1))
	e = tx.QueryRowContext(ctx, q, now.UnixMilli()).Scan(&p.ID, &p.URI, &p.InfoHash, &p.Downloader, &p.Site, &p.Code, &p.Title, &p.Cover)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	p.LeaseToken = newSortableID()
	update := fmt.Sprintf("UPDATE download_tasks SET lease_until=%s,lease_token=%s WHERE id=%s AND status='unknown' AND (lease_until IS NULL OR lease_until<%s)", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4))
	result, e := tx.ExecContext(ctx, update, now.Add(2*time.Minute).UnixMilli(), p.LeaseToken, p.ID, now.UnixMilli())
	if e != nil {
		return nil, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return nil, e
	}
	if n != 1 {
		return nil, nil
	}
	return &p, tx.Commit()
}

// ReleasePending leaves an ambiguous outcome visible without resubmitting the same resource.
func (r *SubscriptionDownloadRepository) ReleasePending(ctx context.Context, p ports.PendingSubmission, message string) error {
	q := fmt.Sprintf("UPDATE download_tasks SET lease_until=%s,lease_token=NULL,error_message=%s,updated_at=%s WHERE id=%s AND lease_token=%s AND status='unknown'", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4), placeholder(r.dialect, 5))
	_, e := r.db.ExecContext(ctx, q, time.Now().Add(5*time.Minute).UnixMilli(), message, encodeTime(time.Now().UTC(), r.dialect), p.ID, p.LeaseToken)
	return e
}

func unmarshalFilter(raw string, target *map[string]any) error {
	if raw == "" {
		*target = map[string]any{}
		return nil
	}
	return json.Unmarshal([]byte(raw), target)
}
