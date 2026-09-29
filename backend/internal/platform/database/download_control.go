package database

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"fmt"
	"time"
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

// FinishControl 在下载器结果已核实后提交本地变更；搜索重试要求活动订阅且不存在另一有效任务。
func (r *SubscriptionDownloadRepository) FinishControl(ctx context.Context, id, token, action string, state *ports.TransferState) error {
	var query string
	var args []any
	switch action {
	case "delete", "delete_files":
		query = fmt.Sprintf("DELETE FROM download_tasks WHERE id=%s AND lease_token=%s", placeholder(r.dialect, 1), placeholder(r.dialect, 2))
		args = []any{id, token}
	case "retry_search":
		// 子查询再包一层以兼容 MySQL 对同表更新子查询的限制。
		query = fmt.Sprintf("UPDATE download_tasks SET status='queued',error_message=NULL,transfer_status=NULL,lease_until=NULL,lease_token=NULL,updated_at=%s WHERE id=%s AND lease_token=%s AND status='failed' AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.id=download_tasks.subscription_id AND s.status='active') AND NOT EXISTS (SELECT 1 FROM (SELECT id,subscription_id,status FROM download_tasks) other WHERE other.subscription_id=download_tasks.subscription_id AND other.id<>download_tasks.id AND other.status IN ('queued','searching','unknown','submitted','downloading','completed'))", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3))
		args = []any{encodeTime(time.Now().UTC(), r.dialect), id, token}
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
