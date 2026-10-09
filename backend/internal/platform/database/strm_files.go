package database

import (
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
)

// StrmFileRepository 使用正式迁移后的文件归属表，不回填历史本地文件。
type StrmFileRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewStrmFileRepository 复用应用连接池。
func NewStrmFileRepository(db *sql.DB, dialect Dialect) *StrmFileRepository {
	return &StrmFileRepository{db, dialect}
}

// Save 按本地路径幂等更新归属，后写入文件替换该路径的旧归属。
func (r *StrmFileRepository) Save(ctx context.Context, item ports.StrmFileRecord) error {
	query := "INSERT INTO strm_files(file_key,scope,file_id,parent_id,ancestors,relative_path,sha256) VALUES(?,?,?,?,?,?,?) ON CONFLICT(file_key) DO UPDATE SET scope=excluded.scope,file_id=excluded.file_id,parent_id=excluded.parent_id,ancestors=excluded.ancestors,relative_path=excluded.relative_path,sha256=excluded.sha256"
	if r.dialect == DialectMySQL {
		query = "INSERT INTO strm_files(file_key,scope,file_id,parent_id,ancestors,relative_path,sha256) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE scope=VALUES(scope),file_id=VALUES(file_id),parent_id=VALUES(parent_id),ancestors=VALUES(ancestors),relative_path=VALUES(relative_path),sha256=VALUES(sha256)"
	}
	_, err := r.db.ExecContext(ctx, (&CollectionRepository{dialect: r.dialect}).q(query), item.Key, item.Scope, item.FileID, item.ParentID, item.Ancestors, item.RelativePath, item.SHA256)
	return err
}

// List 只查询当前账号和映射规则的文件，避免换号或改路径后误删。
func (r *StrmFileRepository) List(ctx context.Context, scope string) ([]ports.StrmFileRecord, error) {
	rows, err := r.db.QueryContext(ctx, (&CollectionRepository{dialect: r.dialect}).q("SELECT file_key,scope,file_id,parent_id,ancestors,relative_path,sha256 FROM strm_files WHERE scope=?"), scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ports.StrmFileRecord
	for rows.Next() {
		var item ports.StrmFileRecord
		if err := rows.Scan(&item.Key, &item.Scope, &item.FileID, &item.ParentID, &item.Ancestors, &item.RelativePath, &item.SHA256); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Delete 幂等移除已清理记录；数据库失败时保留事件游标以便重试。
func (r *StrmFileRepository) Delete(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, (&CollectionRepository{dialect: r.dialect}).q("DELETE FROM strm_files WHERE file_key=?"), key)
	return err
}
