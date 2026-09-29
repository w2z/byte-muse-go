package database

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"fmt"
)

// TagRepository 统一查询源站字典和已采集标签，管理追新规则。
type TagRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewTagRepository 绑定已完成迁移的数据库。
func NewTagRepository(db *sql.DB, dialect Dialect) *TagRepository {
	return &TagRepository{db: db, dialect: dialect}
}

// tagMediaCTE 拆分兼容标签并按影片去重；目录、追新和搜索共用精确匹配。
func tagMediaCTE(d Dialect) string {
	if d == DialectPostgres {
		return "WITH tagged AS (SELECT DISTINCT lm.media_id,BTRIM(part) AS name FROM legacy_media_metadata lm CROSS JOIN LATERAL unnest(string_to_array(COALESCE(lm.genres,''),',')) AS part WHERE BTRIM(part)<>'')"
	}
	if d == DialectMySQL {
		return "WITH RECURSIVE parts(media_id,name,rest) AS (SELECT lm.media_id,CAST('' AS CHAR(65535)),CONCAT(lm.genres,',') FROM legacy_media_metadata lm WHERE lm.genres IS NOT NULL AND lm.genres<>'' UNION ALL SELECT media_id,TRIM(SUBSTRING(rest,1,INSTR(rest,',')-1)),SUBSTRING(rest,INSTR(rest,',')+1) FROM parts WHERE rest<>''),tagged AS (SELECT DISTINCT media_id,name FROM parts WHERE name<>'')"
	}
	return "WITH RECURSIVE parts(media_id,name,rest) AS (SELECT lm.media_id,'',lm.genres||',' FROM legacy_media_metadata lm WHERE lm.genres IS NOT NULL AND lm.genres<>'' UNION ALL SELECT media_id,TRIM(SUBSTR(rest,1,INSTR(rest,',')-1)),SUBSTR(rest,INSTR(rest,',')+1) FROM parts WHERE rest<>''),tagged AS (SELECT DISTINCT media_id,name FROM parts WHERE name<>'')"
}

// SearchMedia 按完整标签名称精确匹配，不把相似名称或标题当作同一个标签。
func (r *TagRepository) SearchMedia(ctx context.Context, name string, limit, offset int) (domain.MediaPage, error) {
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if e != nil {
		return domain.MediaPage{}, e
	}
	defer tx.Rollback()
	cte := tagMediaCTE(r.dialect)
	where := " WHERE EXISTS(SELECT 1 FROM tagged t WHERE t.media_id=m.id AND t.name=" + placeholder(r.dialect, 1) + ")"
	var total int
	if e = tx.QueryRowContext(ctx, cte+" SELECT COUNT(*) FROM media m"+where, name).Scan(&total); e != nil {
		return domain.MediaPage{}, e
	}
	rows, e := tx.QueryContext(ctx, cte+" SELECT "+mediaProjectionColumns("m")+" FROM media m "+mediaProjectionJoins()+where+" ORDER BY m.updated_at DESC,m.id LIMIT "+placeholder(r.dialect, 2)+" OFFSET "+placeholder(r.dialect, 3), name, limit, offset)
	if e != nil {
		return domain.MediaPage{}, e
	}
	items, e := scanMediaProjectionRows(rows)
	rows.Close()
	if e != nil {
		return domain.MediaPage{}, e
	}
	if e = tx.Commit(); e != nil {
		return domain.MediaPage{}, e
	}
	return domain.MediaPage{Items: items, Total: total}, nil
}

// tagCTE 合并无影片的源站标签、未知分类名称与仍有订阅规则的标签。
func (r *TagRepository) tagCTE() string {
	return tagMediaCTE(r.dialect) + ",tag_counts AS (SELECT t.name,COUNT(DISTINCT t.media_id) AS media_count FROM tagged t JOIN media m ON m.id=t.media_id GROUP BY t.name),tag_names AS (SELECT name FROM tag_catalog UNION SELECT name FROM tag_counts UNION SELECT name FROM tag_subscriptions),catalog AS (SELECT n.name,COALESCE(c.category,'未分类') AS category,COALESCE(t.media_count,0) AS media_count,s.limit_date FROM tag_names n LEFT JOIN tag_catalog c ON c.name=n.name LEFT JOIN tag_counts t ON t.name=n.name LEFT JOIN tag_subscriptions s ON s.name=n.name)"
}

// ListTags 使用服务端分页过滤订阅状态、名称与类型；计数与列表共享一致快照。
func (r *TagRepository) ListTags(ctx context.Context, q ports.TagListQuery) ([]ports.Tag, int, error) {
	if q.Limit < 1 || q.Limit > ports.MaxTagPageSize || q.Offset < 0 {
		return nil, 0, fmt.Errorf("invalid tag pagination")
	}
	contains := "INSTR(name," + placeholder(r.dialect, 1) + ")>0"
	if r.dialect == DialectPostgres {
		contains = "STRPOS(name,$1)>0"
	}
	where := " WHERE " + contains
	args := []any{q.Search}
	if q.Category != "" {
		args = append(args, q.Category)
		where += " AND category=" + placeholder(r.dialect, len(args))
	}
	if q.Subscription == "active" {
		where += " AND limit_date IS NOT NULL"
	}
	if q.Subscription == "none" {
		where += " AND limit_date IS NULL"
	}
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback()
	cte := r.tagCTE()
	var total int
	if e = tx.QueryRowContext(ctx, cte+" SELECT COUNT(*) FROM catalog"+where, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	args = append(args, q.Limit, q.Offset)
	rows, e := tx.QueryContext(ctx, cte+" SELECT name,category,media_count,limit_date FROM catalog"+where+" ORDER BY media_count DESC,name ASC LIMIT "+placeholder(r.dialect, len(args)-1)+" OFFSET "+placeholder(r.dialect, len(args)), args...)
	if e != nil {
		return nil, 0, e
	}
	items := []ports.Tag{}
	for rows.Next() {
		var item ports.Tag
		if e = rows.Scan(&item.Name, &item.Category, &item.MediaCount, &item.LimitDate); e != nil {
			rows.Close()
			return nil, 0, e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, e
	}
	if e = tx.Commit(); e != nil {
		return nil, 0, e
	}
	return items, total, nil
}

// getTagTx 从同一事务读取状态，未知标签返回领域错误。
func (r *TagRepository) getTagTx(ctx context.Context, tx *sql.Tx, name string) (ports.Tag, error) {
	var item ports.Tag
	e := tx.QueryRowContext(ctx, r.tagCTE()+" SELECT name,category,media_count,limit_date FROM catalog WHERE name="+placeholder(r.dialect, 1), name).Scan(&item.Name, &item.Category, &item.MediaCount, &item.LimitDate)
	if e == sql.ErrNoRows {
		return item, ports.ErrTagNotFound
	}
	return item, e
}
