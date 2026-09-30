package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// LockControl 用已有租约排他保留任务；只允许终止搜索或已提交后的可操作状态。
func (r *SubscriptionDownloadRepository) LockControl(ctx context.Context, id string) (domain.DownloadTask, string, error) {
	token := newSortableID()
	now := time.Now()
	q := fmt.Sprintf("UPDATE download_tasks SET lease_token=%s,lease_until=%s WHERE id=%s AND status IN ('failed','submitted','downloading','completed') AND (lease_until IS NULL OR lease_until<%s)", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4))
	result, err := r.db.ExecContext(ctx, q, token, now.Add(2*time.Minute).UnixMilli(), id, now.UnixMilli())
	if err != nil {
		return domain.DownloadTask{}, "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return domain.DownloadTask{}, "", err
	}
	if n != 1 {
		var count int
		if err = r.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM download_tasks WHERE id=%s", placeholder(r.dialect, 1)), id).Scan(&count); err != nil {
			return domain.DownloadTask{}, "", err
		}
		if count == 0 {
			return domain.DownloadTask{}, "", ports.ErrDownloadNotFound
		}
		return domain.DownloadTask{}, "", ports.ErrDownloadConflict
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM download_tasks WHERE id=%s", downloadColumns(), placeholder(r.dialect, 1)), id)
	if err != nil {
		_ = r.ReleaseControl(ctx, id, token)
		return domain.DownloadTask{}, "", err
	}
	items, err := scanDownloadRows(rows)
	rows.Close()
	if err != nil || len(items) != 1 {
		_ = r.ReleaseControl(ctx, id, token)
		if err == nil {
			err = ports.ErrDownloadNotFound
		}
		return domain.DownloadTask{}, "", err
	}
	return items[0], token, nil
}

// ReleaseControl 只释放本请求拥有的租约，不覆盖下载状态。
func (r *SubscriptionDownloadRepository) ReleaseControl(ctx context.Context, id, token string) error {
	_, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE download_tasks SET lease_until=NULL,lease_token=NULL WHERE id=%s AND lease_token=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2)), id, token)
	return err
}

// FinishControl 在下载器结果已核实后提交本地变更。
// retry_search 不再复活失败任务，而是把「没找到资源」的任务换成一次新的资源搜索。
func (r *SubscriptionDownloadRepository) FinishControl(ctx context.Context, id, token, action string, state *ports.TransferState) error {
	if action == "retry_search" {
		return r.retrySearch(ctx, id, token)
	}
	var query string
	var args []any
	switch action {
	case "delete", "delete_files":
		query = fmt.Sprintf("DELETE FROM download_tasks WHERE id=%s AND lease_token=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2))
		args = []any{id, token}
	default:
		if state == nil {
			return ports.ErrDownloadAction
		}
		query = fmt.Sprintf("UPDATE download_tasks SET transfer_status=%s,error_message=NULL,lease_until=NULL,lease_token=NULL,updated_at=%s WHERE id=%s AND lease_token=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3), placeholder(r.dialect, 4))
		args = []any{state.Status, encodeTime(time.Now().UTC(), r.dialect), id, token}
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ports.ErrDownloadConflict
	}
	return nil
}

// retrySearch 把「没找到资源」的失败任务换成一次新的资源搜索：删除任务行，登记搜索队列。
// 手动重试由用户显式发起，搜索失败必须推送，因此队列项来源固定为 user；
// 同一订阅已存在的待执行搜索会被替换，保证最多一条待执行搜索（与唯一索引一致）。
func (r *SubscriptionDownloadRepository) retrySearch(ctx context.Context, id, token string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var subscriptionID string
	q := fmt.Sprintf("SELECT subscription_id FROM download_tasks WHERE id=%s AND lease_token=%s AND status='failed' AND (info_hash IS NULL OR info_hash=%s) AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.id=download_tasks.subscription_id AND s.status='active')", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3))
	if err = tx.QueryRowContext(ctx, q, id, token, "").Scan(&subscriptionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrDownloadConflict
		}
		return err
	}
	result, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM download_tasks WHERE id=%s AND lease_token=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2)), id, token)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ports.ErrDownloadConflict
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM subscription_scans WHERE subscription_id=%s", placeholder(r.dialect, 1)), subscriptionID); err != nil {
		return err
	}
	now := time.Now().UTC()
	insert := fmt.Sprintf("INSERT INTO subscription_scans (id,subscription_id,origin,status,created_at,updated_at) VALUES (%s)", placeholders(r.dialect, 6, 1))
	if _, err = tx.ExecContext(ctx, insert, newSortableID(), subscriptionID, string(ports.DownloadOriginUser), "queued", encodeTime(now, r.dialect), encodeTime(now, r.dialect)); err != nil {
		return err
	}
	return tx.Commit()
}
