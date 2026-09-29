package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/ports"
)

// pan115AccountRowID 是绑定行的固定主键：115 绑定是单例状态，全表只保存一行。
const pan115AccountRowID = "current"

// pan115AccountMigration 新增 115 网盘账号绑定表，并登记离线下载的目标目录设置。
// 令牌列保存应用层 AES-GCM 密文，表本身不解释内容；解绑即删除该行，不保留历史令牌。
// 升级只新增空表与空配置，不写入任何账号数据；回退代码时保留该表即可。
func pan115AccountMigration(dialect Dialect) Migration {
	setting := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES (%s, %s, FALSE, %s)", sqlLiteral("PAN115_SAVE_PATH"), sqlLiteral(""), currentTimestampExpression(dialect))
	if dialect == DialectMySQL {
		setting = strings.Replace(setting, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		setting += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 26, Name: "create_pan115_account", Statements: []string{
		"CREATE TABLE pan115_account (id VARCHAR(16) NOT NULL PRIMARY KEY, user_id VARCHAR(64) NOT NULL, user_name VARCHAR(255) NOT NULL, access_token TEXT NOT NULL, refresh_token TEXT NOT NULL, access_expires_at VARCHAR(40) NOT NULL, updated_at VARCHAR(40) NOT NULL)",
		setting,
	}}
}

// Pan115AccountRepository 读写 115 账号绑定；令牌密文由应用层负责加解密。
type Pan115AccountRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewPan115AccountRepository 绑定已完成迁移的数据库。
func NewPan115AccountRepository(db *sql.DB, dialect Dialect) *Pan115AccountRepository {
	return &Pan115AccountRepository{db: db, dialect: dialect}
}

// Load 返回当前绑定；未绑定时 found 为 false，且不返回错误。
// 时间列统一按 RFC3339Nano 文本存取，避免三种方言的时间类型差异进入业务层。
func (r *Pan115AccountRepository) Load(ctx context.Context) (ports.Pan115Account, bool, error) {
	query := "SELECT user_id, user_name, access_token, refresh_token, access_expires_at, updated_at FROM pan115_account WHERE id = " + placeholder(r.dialect, 1)
	var account ports.Pan115Account
	var expiresAt, updatedAt string
	err := r.db.QueryRowContext(ctx, query, pan115AccountRowID).
		Scan(&account.UserID, &account.UserName, &account.AccessToken, &account.RefreshToken, &expiresAt, &updatedAt)
	if err == sql.ErrNoRows {
		return ports.Pan115Account{}, false, nil
	}
	if err != nil {
		return ports.Pan115Account{}, false, err
	}
	if account.AccessExpiresAt, err = parseTimeString(expiresAt); err != nil {
		return ports.Pan115Account{}, false, fmt.Errorf("解析 115 令牌有效期失败: %w", err)
	}
	if account.UpdatedAt, err = parseTimeString(updatedAt); err != nil {
		return ports.Pan115Account{}, false, fmt.Errorf("解析 115 绑定时间失败: %w", err)
	}
	return account, true, nil
}

// Save 覆盖式写入当前绑定，同一时刻只保留一个账号。
func (r *Pan115AccountRepository) Save(ctx context.Context, account ports.Pan115Account) error {
	columns := []string{"id", "user_id", "user_name", "access_token", "refresh_token", "access_expires_at", "updated_at"}
	query := fmt.Sprintf("INSERT INTO pan115_account (%s) VALUES (%s)%s",
		strings.Join(columns, ", "),
		placeholders(r.dialect, len(columns), 1),
		upsertClause(r.dialect, "id", columns[1:]))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.ExecContext(ctx, query,
		pan115AccountRowID, account.UserID, account.UserName, account.AccessToken, account.RefreshToken,
		account.AccessExpiresAt.UTC().Format(time.RFC3339Nano), now)
	return err
}

// Clear 删除当前绑定，使 115 回到未登录状态。
func (r *Pan115AccountRepository) Clear(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM pan115_account")
	return err
}
