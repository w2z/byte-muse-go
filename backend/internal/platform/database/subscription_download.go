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

// SubscriptionDownloadRepository writes the durable search queue and download task state.
// 搜索队列（subscription_scans）与下载任务（download_tasks）分开存储：没搜到资源只结束本次搜索，
// 不产生下载任务，下载页与影片卡片不会再被「没找到资源」的失败记录污染。
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

// activeTaskFilter 是「订阅已有有效任务」的唯一判定条件，与 idx_download_tasks_active_subscription 的取值集合一致。
const activeTaskFilter = "subscription_id=%s AND status IN ('queued','searching','unknown','submitted','downloading','completed')"

// EnqueueScan 为一条有效订阅登记一次资源搜索，返回本次请求对应的持久化标识。
// 该订阅已有进行中的下载任务时不再登记搜索，直接返回该任务标识：搜索的目的就是建立下载任务，
// 重复登记只会多打一次资源站；已有待执行搜索时只把发起方升级为 user，避免用户显式请求
// 被定时任务先登记的搜索吸收而失去失败通知。
func (r *SubscriptionDownloadRepository) EnqueueScan(ctx context.Context, subscriptionID string, origin ports.DownloadOrigin) (string, error) {
	id, _, e := r.enqueueScan(ctx, subscriptionID, origin)
	return id, e
}

func (r *SubscriptionDownloadRepository) enqueueScan(ctx context.Context, subscriptionID string, origin ports.DownloadOrigin) (string, bool, error) {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return "", false, e
	}
	defer tx.Rollback()
	var mediaID string
	e = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT media_id FROM subscriptions WHERE id=%s AND status='active'", placeholder(r.dialect, 1)), subscriptionID).Scan(&mediaID)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, ports.ErrSubscriptionNotFound
	}
	if e != nil {
		return "", false, e
	}
	var taskID string
	e = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT id FROM download_tasks WHERE "+activeTaskFilter+" ORDER BY created_at DESC LIMIT 1", placeholder(r.dialect, 1)), subscriptionID).Scan(&taskID)
	if e == nil {
		return taskID, false, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", false, e
	}
	var scanID string
	var scanOrigin ports.DownloadOrigin
	e = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT id,origin FROM subscription_scans WHERE subscription_id=%s", placeholder(r.dialect, 1)), subscriptionID).Scan(&scanID, &scanOrigin)
	if e == nil {
		if normalizeOrigin(origin) == ports.DownloadOriginUser && normalizeOrigin(scanOrigin) != ports.DownloadOriginUser {
			if _, e = tx.ExecContext(ctx, fmt.Sprintf("UPDATE subscription_scans SET origin=%s,updated_at=%s WHERE id=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3)), ports.DownloadOriginUser, encodeTime(time.Now().UTC(), r.dialect), scanID); e != nil {
				return "", false, e
			}
		}
		return scanID, false, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", false, e
	}
	now := time.Now().UTC()
	scanID = newSortableID()
	if _, e = tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO subscription_scans (id,subscription_id,origin,status,created_at,updated_at) VALUES (%s)", placeholders(r.dialect, 6, 1)), scanID, subscriptionID, string(normalizeOrigin(origin)), "queued", encodeTime(now, r.dialect), encodeTime(now, r.dialect)); e != nil {
		return "", false, e
	}
	return scanID, true, tx.Commit()
}

// normalizeOrigin 把发起方收敛到持久化词表：只有 user 是用户显式发起，其余一律按 schedule 处理。
// 空值或其他取值来自调用方笔误时按定时任务落库，不会因为违反列约束让整个搜索队列写入失败。
func normalizeOrigin(origin ports.DownloadOrigin) ports.DownloadOrigin {
	if origin == ports.DownloadOriginUser {
		return ports.DownloadOriginUser
	}
	return ports.DownloadOriginSchedule
}

// EnqueueActiveScans schedules every active subscription without duplicating unfinished work.
// 批量扫描统一按 schedule 来源落库：它的失败是正常状态，不推送通知。
func (r *SubscriptionDownloadRepository) EnqueueActiveScans(ctx context.Context) (int, error) {
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
		_, created, e := r.enqueueScan(ctx, id, ports.DownloadOriginSchedule)
		if e != nil {
			return count, e
		}
		if created {
			count++
		}
	}
	return count, nil
}

// ClaimScan reserves a queued or expired searching scan and returns its current subscription rules.
// 只领取仍处于 active 的订阅：订阅取消时其队列项已被删除，这里再过滤一次避免并发窗口内领取到失效订阅。
func (r *SubscriptionDownloadRepository) ClaimScan(ctx context.Context, now time.Time) (*ports.SubscriptionScanAttempt, error) {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	query := fmt.Sprintf("SELECT sc.id,sc.subscription_id,s.media_id,m.code,COALESCE(NULLIF(m.translated_title,''),m.title,''),"+mediaCoverColumn("m")+",s.mode,s.filter_json,sc.origin FROM subscription_scans sc JOIN subscriptions s ON s.id=sc.subscription_id AND s.status='active' JOIN media m ON m.id=s.media_id "+mediaCoverJoin("m")+" WHERE (sc.status='queued' OR (sc.status='searching' AND sc.lease_until<%s)) ORDER BY sc.created_at,sc.id LIMIT 1", placeholder(r.dialect, 1))
	var a ports.SubscriptionScanAttempt
	var filterJSON string
	e = tx.QueryRowContext(ctx, query, now.UnixMilli()).Scan(&a.ID, &a.SubscriptionID, &a.MediaID, &a.Code, &a.Title, &a.Cover, &a.Mode, &filterJSON, &a.Origin)
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
	update := fmt.Sprintf("UPDATE subscription_scans SET status='searching',lease_until=%s,lease_token=%s,updated_at=%s WHERE id=%s AND (status='queued' OR (status='searching' AND lease_until<%s))", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4), placeholder(r.dialect, 5))
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

// FinishScan 结束一次没有产生下载任务的搜索：删除队列项，本次搜索结束，等待下一次排期重新搜索。
// 只删除自己持有租约的队列项，避免覆盖其他 worker 已重新领取的搜索。
func (r *SubscriptionDownloadRepository) FinishScan(ctx context.Context, a ports.SubscriptionScanAttempt) error {
	_, e := r.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM subscription_scans WHERE id=%s AND lease_token=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2)), a.ID, a.LeaseToken)
	return e
}

// StartTask 用选中资源建立下载任务，是 download_tasks 的唯一写入入口。
// 任务先落库为 unknown 并持有短租约，提交结果由 FinishSubmission 或后续 ClaimPending 回查收敛；
// 该订阅已有有效任务时返回 ErrSubscriptionTaskActive，订阅已失效时返回 ErrSubscriptionNotFound。
func (r *SubscriptionDownloadRepository) StartTask(ctx context.Context, a ports.SubscriptionScanAttempt, c ports.ScanCandidate) (ports.PendingSubmission, error) {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return ports.PendingSubmission{}, e
	}
	defer tx.Rollback()
	var active int
	if e = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM subscriptions WHERE id=%s AND status='active'", placeholder(r.dialect, 1)), a.SubscriptionID).Scan(&active); e != nil {
		return ports.PendingSubmission{}, e
	}
	if active == 0 {
		return ports.PendingSubmission{}, ports.ErrSubscriptionNotFound
	}
	var existing int
	if e = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM download_tasks WHERE "+activeTaskFilter, placeholder(r.dialect, 1)), a.SubscriptionID).Scan(&existing); e != nil {
		return ports.PendingSubmission{}, e
	}
	if existing > 0 {
		return ports.PendingSubmission{}, ports.ErrSubscriptionTaskActive
	}
	now := time.Now().UTC()
	p := ports.PendingSubmission{ID: newSortableID(), URI: c.URI, InfoHash: c.InfoHash, Downloader: c.Downloader, LeaseToken: newSortableID(), Code: a.Code, Title: a.Title, Site: c.Site, Cover: a.Cover}
	insert := fmt.Sprintf("INSERT INTO download_tasks (id,media_id,subscription_id,status,source_site,source_kind,resource_uri,info_hash,downloader,filter_passed,lease_until,lease_token,created_at,updated_at,origin) VALUES (%s)", placeholders(r.dialect, 15, 1))
	if _, e = tx.ExecContext(ctx, insert, p.ID, a.MediaID, a.SubscriptionID, domain.DownloadStatusUnknown, c.Site, c.Kind, c.URI, c.InfoHash, c.Downloader, c.FilterPassed, now.Add(2*time.Minute).UnixMilli(), p.LeaseToken, encodeTime(now, r.dialect), encodeTime(now, r.dialect), string(normalizeOrigin(a.Origin))); e != nil {
		return ports.PendingSubmission{}, e
	}
	return p, tx.Commit()
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
