package database

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"time"
)

// SaveSubscription 保存起始日期，重复保存不清除处理台账。
func (r *TagRepository) SaveSubscription(ctx context.Context, name, date string) (ports.Tag, error) {
	return r.saveRule(ctx, name, &date)
}

// CancelSubscription 幂等清空规则，保留影片订阅、下载历史及台账。
func (r *TagRepository) CancelSubscription(ctx context.Context, name string) (ports.Tag, error) {
	return r.saveRule(ctx, name, nil)
}

// saveRule 原子更新规则并返回同一事务中的标签信息。
func (r *TagRepository) saveRule(ctx context.Context, name string, date *string) (ports.Tag, error) {
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return ports.Tag{}, e
	}
	defer tx.Rollback()
	item, e := r.getTagTx(ctx, tx, name)
	if e != nil {
		return item, e
	}
	query := "INSERT INTO tag_subscriptions(name,limit_date,updated_at) VALUES(" + placeholders(r.dialect, 3, 1) + ")" + upsertClause(r.dialect, "name", []string{"limit_date", "updated_at"})
	if _, e = tx.ExecContext(ctx, query, name, date, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return item, e
	}
	item.LimitDate = date
	if e = tx.Commit(); e != nil {
		return item, e
	}
	return item, nil
}

// Follow 按起始日（含当天）分批查询，逐影片事务提交台账与订阅。
// 规则行锁串行化退订/编辑与追新；台账防止恢复被用户手动取消的影片订阅。
// 空 name 处理全部活动规则，不创建下载任务或调用下载器。
func (r *TagRepository) Follow(ctx context.Context, name string) (int, error) {
	total := 0
	for {
		where := " WHERE s.limit_date IS NOT NULL AND " + r.releaseAfterLimit() + " AND NOT EXISTS(SELECT 1 FROM tag_subscription_matches x WHERE x.tag_name=t.name AND x.media_id=m.id)"
		args := []any{}
		if name != "" {
			where += " AND s.name=" + placeholder(r.dialect, 1)
			args = append(args, name)
		}
		rows, e := r.db.QueryContext(ctx, tagMediaCTE(r.dialect)+" SELECT t.name,m.id FROM tagged t JOIN tag_subscriptions s ON s.name=t.name JOIN media m ON m.id=t.media_id"+where+" ORDER BY t.name,m.id LIMIT 200", args...)
		if e != nil {
			return total, e
		}
		type candidate struct{ name, id string }
		batch := []candidate{}
		for rows.Next() {
			var c candidate
			if e = rows.Scan(&c.name, &c.id); e != nil {
				rows.Close()
				return total, e
			}
			batch = append(batch, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return total, e
		}
		if len(batch) == 0 {
			return total, nil
		}
		for _, c := range batch {
			created, e := r.followMedia(ctx, c.name, c.id)
			if e != nil {
				return total, e
			}
			if created {
				total++
			}
		}
	}
}

// followMedia 在锁定规则后重新检查日期和台账，避免旧批次越过退订或编辑操作。
func (r *TagRepository) followMedia(ctx context.Context, name, id string) (bool, error) {
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE tag_subscriptions SET name=name WHERE name="+placeholder(r.dialect, 1), name); e != nil {
		return false, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE media SET id=id WHERE id="+placeholder(r.dialect, 1), id); e != nil {
		return false, e
	}
	var library string
	e = tx.QueryRowContext(ctx, "SELECT m.library_status FROM media m JOIN tag_subscriptions s ON s.name="+placeholder(r.dialect, 1)+" WHERE m.id="+placeholder(r.dialect, 2)+" AND s.limit_date IS NOT NULL AND "+r.releaseAfterLimit()+" AND NOT EXISTS(SELECT 1 FROM tag_subscription_matches x WHERE x.tag_name=s.name AND x.media_id=m.id)", name, id).Scan(&library)
	if e == sql.ErrNoRows {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var protected int
	e = tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM subscriptions WHERE media_id="+placeholder(r.dialect, 1)+" AND status='active')+(SELECT COUNT(*) FROM download_tasks WHERE media_id="+placeholder(r.dialect, 2)+" AND status IN ('submitted','downloading','completed','unknown'))+(SELECT COUNT(*) FROM tag_subscription_matches WHERE media_id="+placeholder(r.dialect, 3)+")", id, id, id).Scan(&protected)
	if e != nil {
		return false, e
	}
	created := false
	if protected == 0 && library != "present" {
		repo := &sqlSubscriptionRepository{dialect: r.dialect, exec: tx}
		_, created, e = repo.Create(ctx, ports.CreateSubscription{IdempotencyKey: "tag:" + legacyStableID("tag", name+":"+id), MediaID: id, Mode: domain.SubscriptionModeStrict, Filter: map[string]any{}})
		if e != nil {
			return false, e
		}
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO tag_subscription_matches(tag_name,media_id,processed_at) VALUES("+placeholders(r.dialect, 3, 1)+")", name, id, time.Now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	return created, nil
}

// releaseAfterLimit 对齐 PostgreSQL DATE 与兼容库 ISO 日期文本的比较语义。
func (r *TagRepository) releaseAfterLimit() string {
	if r.dialect == DialectPostgres {
		return "m.release_date>=CAST(s.limit_date AS DATE)"
	}
	return "m.release_date>=s.limit_date"
}
