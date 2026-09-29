package database

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"time"
)

// castMediaCTE 拆分兼容演员字段：casted 按影片去重演员名，cast_counts 统计每部影片的演员数。
// 追新匹配与演员数上限共用同一拆分，避免多处解析规则漂移。
func castMediaCTE(d Dialect) string {
	if d == DialectPostgres {
		return "WITH cast_parts AS (SELECT lm.media_id,BTRIM(part) AS name FROM legacy_media_metadata lm CROSS JOIN LATERAL unnest(string_to_array(COALESCE(lm.casts,''),',')) AS part WHERE BTRIM(part)<>''),casted AS (SELECT DISTINCT media_id,name FROM cast_parts),cast_counts AS (SELECT media_id,COUNT(*) AS c FROM cast_parts GROUP BY media_id)"
	}
	if d == DialectMySQL {
		return "WITH RECURSIVE parts(media_id,name,rest) AS (SELECT lm.media_id,CAST('' AS CHAR(65535)),CONCAT(lm.casts,',') FROM legacy_media_metadata lm WHERE lm.casts IS NOT NULL AND lm.casts<>'' UNION ALL SELECT media_id,TRIM(SUBSTRING(rest,1,INSTR(rest,',')-1)),SUBSTRING(rest,INSTR(rest,',')+1) FROM parts WHERE rest<>''),casted AS (SELECT DISTINCT media_id,name FROM parts WHERE name<>''),cast_counts AS (SELECT media_id,COUNT(*) AS c FROM parts WHERE name<>'' GROUP BY media_id)"
	}
	return "WITH RECURSIVE parts(media_id,name,rest) AS (SELECT lm.media_id,'',lm.casts||',' FROM legacy_media_metadata lm WHERE lm.casts IS NOT NULL AND lm.casts<>'' UNION ALL SELECT media_id,TRIM(SUBSTR(rest,1,INSTR(rest,',')-1)),SUBSTR(rest,INSTR(rest,',')+1) FROM parts WHERE rest<>''),casted AS (SELECT DISTINCT media_id,name FROM parts WHERE name<>''),cast_counts AS (SELECT media_id,COUNT(*) AS c FROM parts WHERE name<>'' GROUP BY media_id)"
}

// actorSubscriptionMigration 新建演员追新处理台账；不回填或更改历史影片与演员规则。
// actor_name 为演员原名，media_id 关联影片；processed_at 为 UTC 时间文本，记录已处理影片，
// 取消影片订阅后不自动重建。回退时保留新增表，删除它们会丢失去重依据。
func actorSubscriptionMigration(d Dialect) Migration {
	return Migration{Version: 22, Name: "actor_subscriptions", Statements: []string{
		"CREATE TABLE actor_subscription_matches (actor_name VARCHAR(255) NOT NULL, media_id VARCHAR(64) NOT NULL, processed_at VARCHAR(40) NOT NULL, PRIMARY KEY(actor_name,media_id), FOREIGN KEY(media_id) REFERENCES media(id))",
		"CREATE INDEX idx_actor_matches_media ON actor_subscription_matches(media_id)",
	}}
}

// ActiveNames 返回已订阅演员名称，供追新任务按演员抓取作品。
func (r *actorRepository) ActiveNames(ctx context.Context) ([]string, error) {
	rows, e := r.db.QueryContext(ctx, "SELECT name FROM actors WHERE limit_date IS NOT NULL ORDER BY name ASC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	names := make([]string, 0)
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			return nil, e
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// Follow 按演员截止日（严格晚于）分批查询，逐影片事务提交台账与订阅。
// 规则行锁串行化退订/编辑与追新；台账防止恢复被用户手动取消的影片订阅。
// 空 name 处理全部已订阅演员；maxActors<0 时不限制演员数；不创建下载任务或调用下载器。
func (r *actorRepository) Follow(ctx context.Context, name string, maxActors int) (int, error) {
	total := 0
	for {
		where := " WHERE a.limit_date IS NOT NULL AND " + actorReleaseAfterLimit(r.dialect) + " AND UPPER(m.code) NOT LIKE '%VR%' AND NOT EXISTS(SELECT 1 FROM actor_subscription_matches x WHERE x.actor_name=a.name AND x.media_id=m.id)"
		args := []any{}
		if name != "" {
			where += " AND a.name=" + placeholder(r.dialect, 1)
			args = append(args, name)
		}
		if maxActors >= 0 {
			where += " AND c.c<=" + placeholder(r.dialect, len(args)+1)
			args = append(args, maxActors)
		}
		rows, e := r.db.QueryContext(ctx, castMediaCTE(r.dialect)+" SELECT a.name,m.id FROM casted t JOIN actors a ON a.name=t.name JOIN media m ON m.id=t.media_id JOIN cast_counts c ON c.media_id=m.id"+where+" ORDER BY a.name,m.id LIMIT 200", args...)
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
			created, e := r.followMedia(ctx, c.name, c.id, maxActors)
			if e != nil {
				return total, e
			}
			if created {
				total++
			}
		}
	}
}

// followMedia 在锁定规则后重新检查日期、VR、演员数与台账，避免旧批次越过退订或编辑。
func (r *actorRepository) followMedia(ctx context.Context, name, id string, maxActors int) (bool, error) {
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE actors SET name=name WHERE name="+placeholder(r.dialect, 1), name); e != nil {
		return false, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE media SET id=id WHERE id="+placeholder(r.dialect, 1), id); e != nil {
		return false, e
	}
	check := castMediaCTE(r.dialect) + " SELECT m.library_status FROM casted t JOIN actors a ON a.name=t.name JOIN media m ON m.id=t.media_id JOIN cast_counts c ON c.media_id=m.id WHERE a.name=" + placeholder(r.dialect, 1) + " AND m.id=" + placeholder(r.dialect, 2) + " AND a.limit_date IS NOT NULL AND " + actorReleaseAfterLimit(r.dialect) + " AND UPPER(m.code) NOT LIKE '%VR%' AND NOT EXISTS(SELECT 1 FROM actor_subscription_matches x WHERE x.actor_name=a.name AND x.media_id=m.id)"
	args := []any{name, id}
	if maxActors >= 0 {
		check += " AND c.c<=" + placeholder(r.dialect, 3)
		args = append(args, maxActors)
	}
	var library string
	e = tx.QueryRowContext(ctx, check, args...).Scan(&library)
	if e == sql.ErrNoRows {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var protected int
	e = tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM subscriptions WHERE media_id="+placeholder(r.dialect, 1)+" AND status='active')+(SELECT COUNT(*) FROM download_tasks WHERE media_id="+placeholder(r.dialect, 2)+" AND status IN ('submitted','downloading','completed','unknown'))+(SELECT COUNT(*) FROM actor_subscription_matches WHERE media_id="+placeholder(r.dialect, 3)+")", id, id, id).Scan(&protected)
	if e != nil {
		return false, e
	}
	created := false
	if protected == 0 && library != "present" {
		repo := &sqlSubscriptionRepository{dialect: r.dialect, exec: tx}
		_, created, e = repo.Create(ctx, ports.CreateSubscription{IdempotencyKey: "actor:" + legacyStableID("actor", name+":"+id), MediaID: id, Mode: domain.SubscriptionModeStrict, Filter: map[string]any{}})
		if e != nil {
			return false, e
		}
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO actor_subscription_matches(actor_name,media_id,processed_at) VALUES("+placeholders(r.dialect, 3, 1)+")", name, id, time.Now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	return created, nil
}

// actorReleaseAfterLimit 保留旧版严格晚于截止日的语义，并对齐 PostgreSQL DATE 比较。
func actorReleaseAfterLimit(d Dialect) string {
	if d == DialectPostgres {
		return "m.release_date>CAST(a.limit_date AS DATE)"
	}
	return "m.release_date>a.limit_date"
}
