package database

import (
	"fmt"
	"strings"
)

// strmSettingsMigration 登记 strm 相关设置：网盘映射、strm 内容使用的对外基址与 Emby 自动刷新开关。
// 默认值分别是空映射、空基址与关闭，升级后的行为与升级前完全一致；
// 只新增键值，不改表结构、不覆盖已有配置；重复执行安全。
func strmSettingsMigration(dialect Dialect) Migration {
	seeds := []struct {
		key   string
		value string
	}{
		{"STRM_PATHS", ""},
		{"STRM_PLAY_BASE", ""},
		{"STRM_EMBY_REFRESH", "false"},
	}
	statements := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		statement := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES (%s, %s, FALSE, %s)",
			sqlLiteral(seed.key), sqlLiteral(seed.value), currentTimestampExpression(dialect))
		if dialect == DialectMySQL {
			statement = strings.Replace(statement, "INSERT INTO", "INSERT IGNORE INTO", 1)
		} else {
			statement += " ON CONFLICT (setting_key) DO NOTHING"
		}
		statements = append(statements, statement)
	}
	return Migration{Version: 28, Name: "strm_settings", Statements: statements}
}
