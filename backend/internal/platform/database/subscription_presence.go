package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"bytemuse/backend/internal/ports"
)

// satisfiedMediaSQL 以同影片的实际下载成功为依据，兼容取消后重新订阅和内部 submitted 状态。
const satisfiedMediaSQL = "EXISTS (SELECT 1 FROM download_tasks done WHERE done.media_id=s.media_id AND (done.status='completed' OR done.transfer_status='completed'))"
const completedSubscriptionSQL = "EXISTS (SELECT 1 FROM subscription_completions fulfilled WHERE fulfilled.subscription_id=s.id)"

// reconcileCompletions 在任务执行时记下已有成功下载；后续移除下载历史也不会重新搜索。
// 只写当前有效订阅的运行状态，迁移本身不回填业务数据。
func (r *SubscriptionDownloadRepository) reconcileCompletions(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = r.reconcileCompletionsTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SubscriptionDownloadRepository) reconcileCompletionsTx(ctx context.Context, tx *sql.Tx) error {
	query := "INSERT INTO subscription_completions(subscription_id,reason,completed_at) SELECT s.id,'download'," + placeholder(r.dialect, 1) + " FROM subscriptions s WHERE s.status='active' AND " + satisfiedMediaSQL
	if r.dialect == DialectMySQL {
		query += " ON DUPLICATE KEY UPDATE subscription_id=VALUES(subscription_id)"
	} else {
		query += " ON CONFLICT(subscription_id) DO NOTHING"
	}
	if _, err := tx.ExecContext(ctx, query, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM subscription_scans WHERE subscription_id IN (SELECT subscription_id FROM subscription_completions)"); err != nil {
		return err
	}
	return nil
}

func saveLibrarySource(ctx context.Context, exec sqlExecutor, dialect Dialect, mediaID string, source ports.LibrarySource) error {
	payload, err := json.Marshal(source)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%x", sha256.Sum256(payload))
	query := "INSERT INTO media_library_sources(media_id,source_key,kind,scope,location,item_id) VALUES(" + placeholders(dialect, 6, 1) + ")"
	if dialect == DialectMySQL {
		query += " ON DUPLICATE KEY UPDATE kind=VALUES(kind)"
	} else {
		query += " ON CONFLICT(media_id,source_key) DO NOTHING"
	}
	_, err = exec.ExecContext(ctx, query, mediaID, key, source.Kind, source.Scope, source.Location, source.ItemID)
	return err
}

// LibrarySources 读取当前影片的全部已记录来源；多个来源只需其中一个确认存在。
func (r *SubscriptionDownloadRepository) LibrarySources(ctx context.Context, mediaID string) ([]ports.LibrarySource, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT kind,scope,location,item_id FROM media_library_sources WHERE media_id="+placeholder(r.dialect, 1), mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.LibrarySource
	for rows.Next() {
		var s ports.LibrarySource
		if err = rows.Scan(&s.Kind, &s.Scope, &s.Location, &s.ItemID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CompleteScan 原子保存满足事实并结束自己的搜索租约；取消或过期租约不能留下完成标记。
func (r *SubscriptionDownloadRepository) CompleteScan(ctx context.Context, a ports.SubscriptionScanAttempt, reason string, source *ports.LibrarySource) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 与取消订阅保持订阅→搜索队列的锁顺序，锁内重新核对所有权。
	query := "SELECT id FROM subscriptions WHERE id=" + placeholder(r.dialect, 1) + " AND status='active'"
	if r.dialect != DialectSQLite {
		query += " FOR UPDATE"
	}
	var id string
	if err = tx.QueryRowContext(ctx, query, a.SubscriptionID).Scan(&id); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM subscription_scans WHERE id="+placeholder(r.dialect, 1)+" AND subscription_id="+placeholder(r.dialect, 2)+" AND lease_token="+placeholder(r.dialect, 3), a.ID, a.SubscriptionID, a.LeaseToken)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return nil
	}
	query = "INSERT INTO subscription_completions(subscription_id,reason,completed_at) VALUES(" + placeholders(r.dialect, 3, 1) + ")" + upsertClause(r.dialect, "subscription_id", []string{"reason", "completed_at"})
	if _, err = tx.ExecContext(ctx, query, a.SubscriptionID, reason, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if source != nil {
		if err = saveLibrarySource(ctx, tx, r.dialect, a.MediaID, *source); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM subscription_scans WHERE id="+placeholder(r.dialect, 1)+" AND lease_token="+placeholder(r.dialect, 2), a.ID, a.LeaseToken); err != nil {
		return err
	}
	return tx.Commit()
}

// ActiveTransfers 包含同影片历史订阅/下载器导入的任务，不因重新订阅重复下载。
func (r *SubscriptionDownloadRepository) ActiveTransfers(ctx context.Context, subscriptionID string) ([]ports.ActiveTransfer, error) {
	query := "SELECT d.id,COALESCE(d.info_hash,''),COALESCE(d.downloader,''),d.updated_at FROM download_tasks d WHERE d.status IN ('submitted','downloading') AND COALESCE(d.transfer_status,'')<>'completed' AND d.lease_token IS NULL AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.media_id=d.media_id AND s.status='active'"
	var args []any
	if subscriptionID != "" {
		query += " AND s.id=" + placeholder(r.dialect, 1)
		args = append(args, subscriptionID)
	}
	query += ") ORDER BY d.id"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.ActiveTransfer
	for rows.Next() {
		var item ports.ActiveTransfer
		var stamp any
		if err = rows.Scan(&item.ID, &item.InfoHash, &item.Downloader, &stamp); err != nil {
			return nil, err
		}
		item.UpdatedAt, err = valueToTime(stamp)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// MarkTransferMissing 只关闭未被控制操作或完成同步更新的快照，保留历史记录。
func (r *SubscriptionDownloadRepository) MarkTransferMissing(ctx context.Context, item ports.ActiveTransfer) error {
	_, err := r.db.ExecContext(ctx, "UPDATE download_tasks SET status='failed',error_message='下载器中已不存在，允许下次搜索',updated_at="+placeholder(r.dialect, 1)+" WHERE id="+placeholder(r.dialect, 2)+" AND updated_at="+placeholder(r.dialect, 3)+" AND status IN ('submitted','downloading') AND COALESCE(transfer_status,'')<>'completed' AND lease_token IS NULL", encodeTime(time.Now().UTC(), r.dialect), item.ID, encodeTime(item.UpdatedAt, r.dialect))
	return err
}

// MarkTransferCompleted 将任意下载器确认的成功与订阅满足事实原子保存。
func (r *SubscriptionDownloadRepository) MarkTransferCompleted(ctx context.Context, item ports.ActiveTransfer) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "UPDATE download_tasks SET transfer_status='completed' WHERE id="+placeholder(r.dialect, 1)+" AND updated_at="+placeholder(r.dialect, 2)+" AND lease_token IS NULL", item.ID, encodeTime(item.UpdatedAt, r.dialect))
	if err != nil {
		return err
	}
	if err = r.reconcileCompletionsTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
