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

// pan115ScanPathsSettingMigration 登记 115 网盘扫描目录设置，默认空值表示不扫描任何目录。
// 只新增空配置键，不改表结构、不覆盖已有配置；重复执行安全。
func pan115ScanPathsSettingMigration(dialect Dialect) Migration {
	setting := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES (%s, %s, FALSE, %s)", sqlLiteral("PAN115_SCAN_PATHS"), sqlLiteral(""), currentTimestampExpression(dialect))
	if dialect == DialectMySQL {
		setting = strings.Replace(setting, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		setting += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 27, Name: "pan115_scan_paths_setting", Statements: []string{setting}}
}

// pan115CookieSettingMigration 把 115 生活事件凭据并入统一的 PAN115_COOKIE。
// 扫码换取与手动填写只维护这一份 Cookie，事件监听与后续能力共用同一凭据；
// 升级时把已加密的旧值原样搬到新键（同一 SESSION_SECRET，密文可直接复用），再删除旧键，
// 避免用户升级后需要重新填写。只增删配置行，不改表结构；重复执行安全。
func pan115CookieSettingMigration(dialect Dialect) Migration {
	now := currentTimestampExpression(dialect)
	// 先按旧键搬运密文，再补空默认值，最后删除旧键；三步都幂等。
	copyValue := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) SELECT %s, setting_value, is_secret, updated_at FROM app_settings WHERE setting_key = %s AND NOT EXISTS (SELECT 1 FROM app_settings WHERE setting_key = %s)",
		sqlLiteral("PAN115_COOKIE"), sqlLiteral("PAN115_EVENT_COOKIE"), sqlLiteral("PAN115_COOKIE"))
	defaultValue := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES (%s, %s, TRUE, %s)",
		sqlLiteral("PAN115_COOKIE"), sqlLiteral(""), now)
	if dialect == DialectMySQL {
		defaultValue = strings.Replace(defaultValue, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		defaultValue += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 35, Name: "pan115_cookie_setting", Statements: []string{
		copyValue,
		defaultValue,
		"DELETE FROM app_settings WHERE setting_key = 'PAN115_EVENT_COOKIE'",
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
