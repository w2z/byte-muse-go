package database

import (
	"fmt"
	"strings"
)

// strmDownloadSettingsMigration 只登记媒体下载开关和后缀，不改历史媒体或覆盖已有配置。
// 两项使用 app_settings 的非空文本值；false 默认关闭，后缀为 JSON 字符串数组，[] 表示不下载。
func strmDownloadSettingsMigration(dialect Dialect) Migration {
	statements := make([]string, 0, 2)
	for _, seed := range []struct{ key, value string }{
		{"STRM_DOWNLOAD_ENABLE", "false"},
		{"STRM_DOWNLOAD_EXTENSIONS", `["srt","ssa","ass","nfo","jpg","png"]`},
	} {
		statement := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES (%s, %s, FALSE, %s)", sqlLiteral(seed.key), sqlLiteral(seed.value), currentTimestampExpression(dialect))
		if dialect == DialectMySQL {
			statement = strings.Replace(statement, "INSERT INTO", "INSERT IGNORE INTO", 1)
		} else {
			statement += " ON CONFLICT (setting_key) DO NOTHING"
		}
		statements = append(statements, statement)
	}
	return Migration{Version: 37, Name: "strm_download_settings", Statements: statements}
}

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

// strmRootSettingMigration 登记本地 strm 根目录设置 STRM_ROOT。
// 默认值为空串，表示沿用进程配置（环境变量 STRM_ROOT，缺省容器内 /strm），
// 因此升级后的行为与升级前完全一致；只新增键值，不改表结构、不覆盖已有配置；重复执行安全。
func strmRootSettingMigration(dialect Dialect) Migration {
	statement := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES (%s, %s, FALSE, %s)",
		sqlLiteral("STRM_ROOT"), sqlLiteral(""), currentTimestampExpression(dialect))
	if dialect == DialectMySQL {
		statement = strings.Replace(statement, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		statement += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 33, Name: "strm_root_setting", Statements: []string{statement}}
}
