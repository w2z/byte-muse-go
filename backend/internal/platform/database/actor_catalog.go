package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"bytemuse/backend/internal/ports"
)

// ActorCatalogRepository 保存演员目录和热门快照，不改订阅日期或影片关联。
type ActorCatalogRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewActorCatalogRepository 使用现有业务库，每个演员单独提交，重复导入安全。
func NewActorCatalogRepository(db *sql.DB, d Dialect) *ActorCatalogRepository {
	return &ActorCatalogRepository{db: db, dialect: d}
}

// SaveActorProfile 新演员建档，已有演员只补空头像；别名只登记关联，不合并历史实体。
func (r *ActorCatalogRepository) SaveActorProfile(ctx context.Context, p ports.ActorProfile) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	inserted, err := saveActorProfileTx(ctx, tx, r.dialect, p)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return inserted, nil
}

// saveActorProfileTx 是目录导入和逐影片入库共用的演员资料写入规则；不改订阅和已有头像。
func saveActorProfileTx(ctx context.Context, tx *sql.Tx, dialect Dialect, p ports.ActorProfile) (bool, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 255 || len(p.Photo) > 2048 || len(p.Aliases) > 500 {
		return false, fmt.Errorf("invalid actor profile")
	}
	r := &ActorCatalogRepository{dialect: dialect}
	now := encodeTime(time.Now().UTC(), r.dialect)
	q := (&CollectionRepository{dialect: r.dialect}).q
	var n int
	if err := tx.QueryRowContext(ctx, q("SELECT COUNT(*) FROM actors WHERE name=?"), p.Name).Scan(&n); err != nil {
		return false, err
	}
	insert := "INSERT INTO actors(name,photo,created_at,updated_at) VALUES(?,?,?,?)"
	if r.dialect == DialectMySQL {
		insert += " ON DUPLICATE KEY UPDATE name=VALUES(name)"
	} else {
		insert += " ON CONFLICT(name) DO NOTHING"
	}
	if _, err := tx.ExecContext(ctx, q(insert), p.Name, nullIfEmpty(p.Photo), now, now); err != nil {
		return false, err
	}
	if p.Photo != "" {
		if _, err := tx.ExecContext(ctx, q("UPDATE actors SET photo=?,updated_at=? WHERE name=? AND (photo IS NULL OR photo='')"), p.Photo, now, p.Name); err != nil {
			return false, err
		}
	}
	for _, alias := range p.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == p.Name || utf8.RuneCountInString(alias) > 255 {
			continue
		}
		query := "INSERT INTO actor_aliases(actor_name,alias) VALUES(?,?)"
		if r.dialect == DialectMySQL {
			query += " ON DUPLICATE KEY UPDATE alias=VALUES(alias)"
		} else {
			query += " ON CONFLICT(actor_name,alias) DO NOTHING"
		}
		if _, err := tx.ExecContext(ctx, q(query), p.Name, alias); err != nil {
			return false, err
		}
	}
	return n == 0, nil
}

// PublishHotActors 完整替换演员月榜，事务失败保留旧榜；不删除演员或修改影片榜。
func (r *ActorCatalogRepository) PublishHotActors(ctx context.Context, items []ports.ActorProfile) error {
	if len(items) == 0 {
		return fmt.Errorf("empty actor ranking")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := (&CollectionRepository{dialect: r.dialect}).q
	if _, err = tx.ExecContext(ctx, "DELETE FROM rank_entries WHERE rank_type='actors'"); err != nil {
		return err
	}
	for i, p := range items {
		if _, err = tx.ExecContext(ctx, q("INSERT INTO rank_entries(rank_type,position,code,source_created_at) VALUES('actors',?,?,?)"), i+1, p.Name, encodeTime(time.Now().UTC(), r.dialect)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// actorAliasesMigration 别名只用于目录搜索，复合主键允许同名别名关联多位演员，不猜测身份合并。
// 两列 VARCHAR(255) NOT NULL、无默认值；空串无业务意义，由服务拒绝。
func actorAliasesMigration(d Dialect) Migration {
	return Migration{Version: 31, Name: "actor_catalog_aliases", Statements: []string{
		"CREATE TABLE actor_aliases (actor_name VARCHAR(255) NOT NULL, alias VARCHAR(255) NOT NULL, PRIMARY KEY(actor_name,alias), FOREIGN KEY(actor_name) REFERENCES actors(name))",
		"CREATE INDEX idx_actor_alias_name ON actor_aliases(alias)",
	}}
}
