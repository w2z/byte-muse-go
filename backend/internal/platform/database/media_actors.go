package database

import (
	"context"
	"database/sql"
	"strings"

	"bytemuse/backend/internal/domain"
)

// actorIdentitySQL 统一演员身份规则：主名精确匹配优先，别名必须唯一归属。
// nameSQL 只能由程序提供列表达式或参数占位符，禁止拼接用户输入。
func actorIdentitySQL(nameSQL string) string {
	return "COALESCE((SELECT a.name FROM actors a WHERE a.name=" + nameSQL + "),(SELECT MIN(aa.actor_name) FROM actor_aliases aa WHERE aa.alias=" + nameSQL + " HAVING COUNT(DISTINCT aa.actor_name)=1))"
}

// actorNameParameter 为派生表参数提供文本类型；MySQL CAST 不支持 VARCHAR。
func actorNameParameter(dialect Dialect, index int) string {
	kind := "TEXT"
	if dialect == DialectMySQL {
		kind = "CHAR"
	}
	return "CAST(" + placeholder(dialect, index) + " AS " + kind + ")"
}

// matchMediaActors 每批最多 200 个去重名字，避免逐卡查询及数据库参数上限；只读演员目录。
func matchMediaActors(ctx context.Context, exec sqlExecutor, dialect Dialect, items []domain.Media) error {
	names := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		for _, actor := range item.Actors {
			if !seen[actor.Name] {
				names = append(names, actor.Name)
				seen[actor.Name] = true
			}
		}
	}
	matches := map[string]string{}
	for start := 0; start < len(names); start += 200 {
		batch := names[start:min(start+200, len(names))]
		selects := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, name := range batch {
			selects[i] = "SELECT " + actorNameParameter(dialect, i+1) + " AS name"
			args[i] = name
		}
		rows, err := exec.QueryContext(ctx, "SELECT n.name,"+actorIdentitySQL("n.name")+" FROM ("+strings.Join(selects, " UNION ALL ")+") n", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			var canonical sql.NullString
			if err = rows.Scan(&name, &canonical); err != nil {
				rows.Close()
				return err
			}
			if canonical.Valid {
				matches[name] = canonical.String
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for i := range items {
		for j := range items[i].Actors {
			if name, ok := matches[items[i].Actors[j].Name]; ok {
				items[i].Actors[j].ActorName = stringPointer(name)
			}
		}
	}
	return nil
}

// SearchActor 按演员身份精确检索，包含唯一别名，先筛选再分页；标题和相似姓名不参与匹配。
func (r *CatalogQueryRepository) SearchActor(ctx context.Context, name string, limit, offset int) (domain.MediaPage, error) {
	limit, offset = normalizePagination(limit, offset)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return domain.MediaPage{}, err
	}
	defer tx.Rollback()
	cte := castMediaCTE(r.dialect) + ", actor_target AS (SELECT " + actorIdentitySQL("n.name") + " AS name FROM (SELECT " + actorNameParameter(r.dialect, 1) + " AS name) n)"
	where := " WHERE EXISTS(SELECT 1 FROM casted t CROSS JOIN actor_target target WHERE t.media_id=m.id AND target.name IS NOT NULL AND " + actorIdentitySQL("t.name") + "=target.name)"
	var total int
	if err = tx.QueryRowContext(ctx, cte+" SELECT COUNT(*) FROM media m"+where, name).Scan(&total); err != nil {
		return domain.MediaPage{}, err
	}
	rows, err := tx.QueryContext(ctx, cte+" SELECT "+mediaProjectionColumns("m")+" FROM media m "+mediaProjectionJoins()+where+" ORDER BY m.updated_at DESC,m.id ASC LIMIT "+placeholder(r.dialect, 2)+" OFFSET "+placeholder(r.dialect, 3), name, limit, offset)
	if err != nil {
		return domain.MediaPage{}, err
	}
	items, err := scanMediaProjectionRows(ctx, tx, r.dialect, rows)
	if err != nil {
		return domain.MediaPage{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.MediaPage{}, err
	}
	return domain.MediaPage{Items: items, Total: total}, nil
}
