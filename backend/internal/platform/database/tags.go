package database

import (
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"fmt"
)

// TagRepository 从兼容资料的逗号分隔标签生成只读目录，不写表或回填历史资料。
type TagRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewTagRepository 绑定已有数据库与 SQL 方言。
func NewTagRepository(db *sql.DB, dialect Dialect) *TagRepository {
	return &TagRepository{db: db, dialect: dialect}
}

// ListTags 在数据库中完成拆分、同影片去重、字面搜索和分页；不向前端传输全库影片。
func (r *TagRepository) ListTags(ctx context.Context, search string, limit, offset int) ([]ports.Tag, int, error) {
	if limit < 1 || limit > ports.MaxPageSize || offset < 0 {
		return nil, 0, fmt.Errorf("invalid tag pagination")
	}
	cte := r.tagCTE()
	contains := "INSTR(name, " + placeholder(r.dialect, 1) + ") > 0"
	if r.dialect == DialectPostgres {
		contains = "STRPOS(name, $1) > 0"
	}
	where := " WHERE " + contains
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRowContext(ctx, cte+" SELECT COUNT(*) FROM tag_counts"+where, search).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, cte+" SELECT name,media_count FROM tag_counts"+where+" ORDER BY media_count DESC,name ASC LIMIT "+placeholder(r.dialect, 2)+" OFFSET "+placeholder(r.dialect, 3), search, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items := []ports.Tag{}
	for rows.Next() {
		var item ports.Tag
		if err = rows.Scan(&item.Name, &item.MediaCount); err != nil {
			rows.Close()
			return nil, 0, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// tagCTE 为三种数据库提供相同的标签投影；SQL 值均参数化，空标签不会成为目录项。
func (r *TagRepository) tagCTE() string {
	if r.dialect == DialectPostgres {
		return "WITH tag_counts AS (SELECT BTRIM(part) AS name, COUNT(DISTINCT lm.media_id) AS media_count FROM legacy_media_metadata lm JOIN media m ON m.id=lm.media_id CROSS JOIN LATERAL unnest(string_to_array(COALESCE(lm.genres,''),',')) AS part WHERE BTRIM(part)<>'' GROUP BY BTRIM(part))"
	}
	if r.dialect == DialectMySQL {
		return "WITH RECURSIVE parts(media_id,name,rest) AS (SELECT lm.media_id,CAST('' AS CHAR(65535)),CONCAT(lm.genres,',') FROM legacy_media_metadata lm JOIN media m ON m.id=lm.media_id WHERE lm.genres IS NOT NULL AND lm.genres<>'' UNION ALL SELECT media_id,TRIM(SUBSTRING(rest,1,INSTR(rest,',')-1)),SUBSTRING(rest,INSTR(rest,',')+1) FROM parts WHERE rest<>''),tag_counts AS (SELECT name,COUNT(DISTINCT media_id) AS media_count FROM parts WHERE name<>'' GROUP BY name)"
	}
	return "WITH RECURSIVE parts(media_id,name,rest) AS (SELECT lm.media_id,'',lm.genres||',' FROM legacy_media_metadata lm JOIN media m ON m.id=lm.media_id WHERE lm.genres IS NOT NULL AND lm.genres<>'' UNION ALL SELECT media_id,TRIM(SUBSTR(rest,1,INSTR(rest,',')-1)),SUBSTR(rest,INSTR(rest,',')+1) FROM parts WHERE rest<>''),tag_counts AS (SELECT name,COUNT(DISTINCT media_id) AS media_count FROM parts WHERE name<>'' GROUP BY name)"
}
