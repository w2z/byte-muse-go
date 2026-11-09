package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"bytemuse/backend/internal/ports"
)

// uploadReceiptMigration adds an independent handoff without rewriting upload snapshots.
// All columns are NOT NULL without defaults. Keys/account/file/parent identify the verified object;
// info is immutable JSON ancestry; local_root/relative identify a rooted local source; sha1 verifies content;
// size is bytes, modified is Unix nanoseconds, verified_at is Unix seconds; state is pending/completed.
// rules_hash is the SHA256 of the active local rules; changed rules cannot suppress later events.
// Rollback retains this additive table. Old binaries never rewrite these receipts.
func uploadReceiptMigration(dialect Dialect) Migration {
	payload := "TEXT"
	if dialect == DialectMySQL {
		payload = "LONGTEXT"
	}
	statements := []string{
		fmt.Sprintf("CREATE TABLE upload_postprocess (receipt_key VARCHAR(64) NOT NULL PRIMARY KEY, account_id VARCHAR(64) NOT NULL, file_id VARCHAR(64) NOT NULL, parent_id VARCHAR(64) NOT NULL, info %s NOT NULL, local_root TEXT NOT NULL, relative_path TEXT NOT NULL, sha1 VARCHAR(40) NOT NULL, size BIGINT NOT NULL, modified BIGINT NOT NULL, verified_at BIGINT NOT NULL, rules_hash VARCHAR(64) NOT NULL, state VARCHAR(16) NOT NULL CHECK(state IN ('pending','completed')))", payload),
		"CREATE INDEX idx_upload_postprocess_file ON upload_postprocess(account_id,file_id,verified_at)",
	}
	comments := []struct{ name, typ, text string }{
		{"receipt_key", "VARCHAR(64)", "账号、上传任务及远端文件身份摘要，唯一后处理键"},
		{"account_id", "VARCHAR(64)", "115 绑定账号标识"}, {"file_id", "VARCHAR(64)", "核验成功的远端文件标识"},
		{"parent_id", "VARCHAR(64)", "核验成功的远端父目录标识"}, {"info", payload, "不可变文件回执及祖先路径 JSON，不含凭据"},
		{"local_root", "TEXT", "受限本地上传根目录"}, {"relative_path", "TEXT", "相对本地上传根目录的源文件路径"},
		{"sha1", "VARCHAR(40)", "已核验源文件 SHA1"}, {"size", "BIGINT", "源文件大小，单位字节"},
		{"modified", "BIGINT", "源文件修改时间，Unix 纳秒"}, {"verified_at", "BIGINT", "回执创建时间，Unix 秒"},
		{"rules_hash", "VARCHAR(64)", "本地映射及生成规则 SHA256，规则变化不跳过事件"},
		{"state", "VARCHAR(16)", "后处理状态：pending 待处理，completed 已完成"},
	}
	for _, c := range comments {
		if dialect == DialectPostgres {
			statements = append(statements, "COMMENT ON COLUMN upload_postprocess."+c.name+" IS '"+c.text+"'")
		}
		if dialect == DialectMySQL {
			needle := c.name + " " + c.typ + " NOT NULL"
			statements[0] = strings.ReplaceAll(statements[0], needle, needle+" COMMENT '"+c.text+"'")
		}
	}
	return Migration{Version: 48, Name: "upload_postprocess", Statements: statements}
}

// UploadReceiptRepository persists immutable receipts and updates completion independently.
type UploadReceiptRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewUploadReceiptRepository uses the versioned schema and performs no implicit DDL.
func NewUploadReceiptRepository(db *sql.DB, dialect Dialect) *UploadReceiptRepository {
	return &UploadReceiptRepository{db, dialect}
}

func (r *UploadReceiptRepository) query(query string) string {
	return (&CollectionRepository{dialect: r.dialect}).q(query)
}

// Insert never overwrites a receipt or resets completed processing on upload recovery.
func (r *UploadReceiptRepository) Insert(ctx context.Context, v ports.UploadReceipt) error {
	q := "INSERT INTO upload_postprocess(receipt_key,account_id,file_id,parent_id,info,local_root,relative_path,sha1,size,modified,verified_at,rules_hash,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(receipt_key) DO NOTHING"
	if r.dialect == DialectMySQL {
		q = "INSERT INTO upload_postprocess(receipt_key,account_id,file_id,parent_id,info,local_root,relative_path,sha1,size,modified,verified_at,rules_hash,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE receipt_key=VALUES(receipt_key)"
	}
	_, err := r.db.ExecContext(ctx, r.query(q), v.Key, v.Account, v.FileID, v.ParentID, v.Info, v.LocalRoot, v.Relative, v.SHA1, v.Size, v.Modified, v.VerifiedAt, v.Rules, "pending")
	return err
}

const uploadReceiptColumns = "receipt_key,account_id,file_id,parent_id,info,local_root,relative_path,sha1,size,modified,verified_at,rules_hash,state"

func (r *UploadReceiptRepository) read(ctx context.Context, where string, args ...any) (*ports.UploadReceipt, error) {
	v := &ports.UploadReceipt{}
	err := r.db.QueryRowContext(ctx, r.query("SELECT "+uploadReceiptColumns+" FROM upload_postprocess WHERE "+where), args...).Scan(&v.Key, &v.Account, &v.FileID, &v.ParentID, &v.Info, &v.LocalRoot, &v.Relative, &v.SHA1, &v.Size, &v.Modified, &v.VerifiedAt, &v.Rules, &v.State)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Get retrieves a handoff by upload identity, including completed rows needed by move recovery.
func (r *UploadReceiptRepository) Get(ctx context.Context, key string) (*ports.UploadReceipt, error) {
	return r.read(ctx, "receipt_key=?", key)
}

// Find performs an indexed account/file lookup; it never scans the upload queue JSON snapshots.
func (r *UploadReceiptRepository) Find(ctx context.Context, account, file string) (*ports.UploadReceipt, error) {
	return r.read(ctx, "account_id=? AND file_id=? ORDER BY verified_at DESC,receipt_key DESC LIMIT 1", account, file)
}

// Complete changes only processing state, leaving the immutable source and remote identity intact.
func (r *UploadReceiptRepository) Complete(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, r.query("UPDATE upload_postprocess SET state='completed' WHERE receipt_key=?"), key)
	return err
}
