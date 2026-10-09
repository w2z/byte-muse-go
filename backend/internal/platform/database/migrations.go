package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Migration is one ordered schema change. The same version/name exists for every supported dialect.
type Migration struct {
	Version    int64
	Name       string
	Statements []string
}

// scanTasksMigration 新增任务台账，不改历史媒体与文件。所有字段非空且由服务显式赋值，无默认值。
// id 为随机任务标识，kind 为 library/strm，state 为执行状态，snapshot 为完整 JSON 快照，
// created_at 为 UTC RFC3339Nano；JSON 内空 result 表示尚无最终结果，空 error 表示没有错误。
// 升级只建表和索引，回退程序时保留该表即可，不需要删除业务数据。
func scanTasksMigration(dialect Dialect) Migration {
	payload := "TEXT"
	if dialect == DialectMySQL {
		payload = "LONGTEXT"
	}
	return Migration{Version: 36, Name: "scan_tasks", Statements: []string{
		fmt.Sprintf("CREATE TABLE scan_tasks (id VARCHAR(32) PRIMARY KEY, kind VARCHAR(16) NOT NULL CHECK (kind IN ('library','strm')), state VARCHAR(16) NOT NULL CHECK (state IN ('running','pausing','paused','canceling','canceled','completed','failed','interrupted')), snapshot %s NOT NULL, created_at VARCHAR(40) NOT NULL)", payload),
		"CREATE INDEX idx_scan_tasks_kind_created ON scan_tasks(kind,created_at,id)",
	}}
}

// MigrationPlan returns the authoritative dialect-specific migration list.
func MigrationPlan(dialect Dialect) []Migration {
	var plan []Migration
	switch dialect {
	case DialectSQLite:
		plan = sqliteMigrations()
	case DialectPostgres:
		plan = postgresMigrations()
	case DialectMySQL:
		plan = mysqlMigrations()
	default:
		return nil
	}
	return append(plan, settingsMigration(dialect), activeSubscriptionMigration(dialect), defaultSettingsMigration(dialect), systemLogsMigration(dialect), logRetentionSettingMigration(dialect), cleanupCanceledSubscriptionsMigration(dialect), catalogQueryIndexesMigration(dialect), downloaderAndBypassSettingsMigration(dialect), collectionMigration(dialect), collectionQueueMigration(dialect), mediaTypeMigration(dialect), subscriptionDownloadMigration(dialect), downloadTransferMigration(dialect), ptSiteSettingsMigration(dialect), siteAuthSettingsMigration(dialect), tagSubscriptionMigration(dialect), bypassProxySettingMigration(dialect), actorSubscriptionMigration(dialect), translationModelSettingsMigration(dialect), notificationSettingsMigration(dialect), tagAliasMigration(dialect), pan115AccountMigration(dialect), pan115ScanPathsSettingMigration(dialect), strmSettingsMigration(dialect), downloadOriginMigration(dialect), dropUnusedJavdbHostSettingMigration(dialect), actorAliasesMigration(dialect), dropUnusedPhotoCacheSettingMigration(dialect), strmRootSettingMigration(dialect), subscriptionScanMigration(dialect), pan115CookieSettingMigration(dialect), scanTasksMigration(dialect), strmDownloadSettingsMigration(dialect), strmEmbyMediaSettingsMigration(dialect), cloudUploadMigration(dialect), taskCheckpointsMigration(dialect), downloadURLMigration(dialect), strmFilesMigration(dialect))
}

// strmFilesMigration 新增受管文件归属，不回填历史文件；回退保留表即可。
// 所有字段非空且无默认值：file_key 为本地路径摘要主键，scope 为账号与映射摘要，
// file_id/parent_id 为网盘标识，ancestors 为祖先 ID JSON，relative_path 为根内相对路径，sha256 为写入内容摘要。
func strmFilesMigration(dialect Dialect) Migration {
	return Migration{Version: 42, Name: "strm_files", Statements: []string{
		"CREATE TABLE strm_files (file_key VARCHAR(64) PRIMARY KEY, scope VARCHAR(64) NOT NULL, file_id VARCHAR(32) NOT NULL, parent_id VARCHAR(32) NOT NULL, ancestors TEXT NOT NULL, relative_path TEXT NOT NULL, sha256 VARCHAR(64) NOT NULL)",
		"CREATE INDEX idx_strm_files_scope ON strm_files(scope)",
	}}
}

// downloadURLMigration 保存提交前实际使用的下载链接快照，TEXT 可空、默认 NULL。
// NULL 表示未记录；不回填历史任务。链接可能过期或含凭据，仅供内部追溯，回退保留此列。
func downloadURLMigration(dialect Dialect) Migration {
	statement := "ALTER TABLE download_tasks ADD COLUMN download_url TEXT DEFAULT NULL"
	if dialect == DialectMySQL {
		statement = "ALTER TABLE download_tasks ADD COLUMN download_url TEXT NULL COMMENT '实际下载链接快照，NULL 表示未记录，可能含临时凭据'"
	}
	statements := []string{statement}
	if dialect == DialectPostgres {
		statements = append(statements, "COMMENT ON COLUMN download_tasks.download_url IS '实际下载链接快照，NULL 表示未记录，可能含临时凭据'")
	}
	return Migration{Version: 41, Name: "download_url_snapshot", Statements: statements}
}

// taskCheckpointsMigration 只新增断点台账，保留历史任务与媒体数据。
// task_id 为任务归属，checkpoint_key 为 SHA256 稳定键，payload 为 JSON；均非空、无默认值。
// 无记录表示未确认完成；媒体刷新快照也使用此表，回退时保留表即可。
func taskCheckpointsMigration(dialect Dialect) Migration {
	payload := "TEXT"
	if dialect == DialectMySQL {
		payload = "LONGTEXT"
	}
	return Migration{Version: 40, Name: "task_checkpoints", Statements: []string{
		fmt.Sprintf("CREATE TABLE task_checkpoints (task_id VARCHAR(64) NOT NULL, checkpoint_key VARCHAR(64) NOT NULL, payload %s NOT NULL, PRIMARY KEY(task_id,checkpoint_key))", payload),
	}}
}

// dropUnusedPhotoCacheSettingMigration 清理已废弃的 ENABLE_PHOTO_CACHE 配置行。
// 图片缓存已改为固定开启、不再有开关，Go 版只从 COVER_ROOT 环境变量取缓存目录，不读取该键；
// 这里只删除这一行历史数据，不改表结构，重复执行安全。
func dropUnusedPhotoCacheSettingMigration(dialect Dialect) Migration {
	return Migration{Version: 32, Name: "drop_unused_photo_cache_setting", Statements: []string{
		"DELETE FROM app_settings WHERE setting_key = 'ENABLE_PHOTO_CACHE'",
	}}
}

// dropUnusedJavdbHostSettingMigration 清理已废弃的 JAVDB_HOST 配置行。
// 该键只有迁移 7 种下的默认值，采集、调度与通知均不读取它（榜单采集固定访问 javdb.com），
// 属于可写清单之外的死配置；这里只删除这一行历史数据，不改表结构，重复执行安全。
func dropUnusedJavdbHostSettingMigration(dialect Dialect) Migration {
	return Migration{Version: 30, Name: "drop_unused_javdb_host_setting", Statements: []string{
		"DELETE FROM app_settings WHERE setting_key = 'JAVDB_HOST'",
	}}
}

// downloadOriginMigration 记录每次下载尝试的发起方：VARCHAR(16)、NOT NULL、默认 'schedule'。
// 取值只有 schedule（定时任务与后台批处理）和 user（用户显式发起）两种，列不可为空，因此没有空值语义。
// 该列只决定搜索失败时是否推送通知；历史行沿用默认值 schedule，不回填、不重算，升级后立即停止历史刷屏。
func downloadOriginMigration(dialect Dialect) Migration {
	column := "ALTER TABLE download_tasks ADD COLUMN origin VARCHAR(16) NOT NULL DEFAULT 'schedule' CHECK (origin IN ('schedule','user'))"
	if dialect == DialectMySQL {
		column = "ALTER TABLE download_tasks ADD COLUMN origin VARCHAR(16) NOT NULL DEFAULT 'schedule' COMMENT '下载尝试发起方：schedule 定时任务、user 用户显式发起' CHECK (origin IN ('schedule','user'))"
	}
	statements := []string{column}
	if dialect == DialectPostgres {
		statements = append(statements, "COMMENT ON COLUMN download_tasks.origin IS '下载尝试发起方：schedule 定时任务、user 用户显式发起'")
	}
	return Migration{Version: 29, Name: "download_task_origin", Statements: statements}
}

// subscriptionScanMigration 把资源搜索从下载任务里独立出来：subscription_scans 只保存待执行的订阅搜索，
// download_tasks 只保存已经选中资源、真正提交给下载器的任务；搜索没找到资源不再产生失败任务。
// 同一订阅同时只允许一次待执行搜索（唯一索引），重复登记只升级发起方，不重复入队。
// 迁移同时清理两类历史噪音：旧搜索队列项（queued/searching，从未提交下载器）与
// 没有 info_hash 的失败任务（只是没搜到资源）；两条 DELETE 都只命中无下载信息的行，重复执行安全。
func subscriptionScanMigration(dialect Dialect) Migration {
	idType, timeType, originComment := "TEXT", "TEXT", ""
	if dialect == DialectPostgres {
		timeType = "TIMESTAMPTZ"
	}
	if dialect == DialectMySQL {
		idType, timeType = "VARCHAR(26)", "DATETIME(6)"
		originComment = " COMMENT '搜索发起方：schedule 定时任务、user 用户显式发起'"
	}
	return Migration{Version: 34, Name: "subscription_scans", Statements: []string{
		fmt.Sprintf(`CREATE TABLE subscription_scans (
	id %s PRIMARY KEY,
	subscription_id %s NOT NULL,
	origin VARCHAR(16) NOT NULL DEFAULT 'schedule'%s CHECK (origin IN ('schedule','user')),
	status VARCHAR(16) NOT NULL CHECK (status IN ('queued','searching')),
	lease_until BIGINT DEFAULT NULL,
	lease_token VARCHAR(64) DEFAULT NULL,
	created_at %s NOT NULL,
	updated_at %s NOT NULL
)`, idType, idType, originComment, timeType, timeType),
		"CREATE UNIQUE INDEX idx_subscription_scans_subscription ON subscription_scans(subscription_id)",
		"CREATE INDEX idx_subscription_scans_status_created ON subscription_scans(status, created_at)",
		"DELETE FROM download_tasks WHERE status IN ('queued','searching')",
		"DELETE FROM download_tasks WHERE status = 'failed' AND (info_hash IS NULL OR info_hash = '')",
	}}
}

// notificationSettingsMigration 为微信与 Telegram 各新增 6 个业务通知开关，两个渠道互不影响。
// 5 个推送类通知是新增行为，默认关闭，避免升级后未经确认就向渠道推送；
// 「Agent 对话」只是给已有渠道对话增加按渠道关闭的能力，默认开启以保持升级前后行为一致。
// 只新增键值，不改表结构、不覆盖已有配置；重复执行安全。
func notificationSettingsMigration(dialect Dialect) Migration {
	now := currentTimestampExpression(dialect)
	values := make([]string, 0, 12)
	for _, item := range []struct{ key, value string }{
		{"WECHAT_NOTIFY_SUBSCRIBE", "false"},
		{"WECHAT_NOTIFY_SUBSCRIBE_FAILED", "false"},
		{"WECHAT_NOTIFY_DOWNLOAD_START", "false"},
		{"WECHAT_NOTIFY_DOWNLOAD_COMPLETE", "false"},
		{"WECHAT_NOTIFY_DOWNLOAD_FAILED", "false"},
		{"WECHAT_NOTIFY_AGENT_CHAT", "true"},
		{"TELEGRAM_NOTIFY_SUBSCRIBE", "false"},
		{"TELEGRAM_NOTIFY_SUBSCRIBE_FAILED", "false"},
		{"TELEGRAM_NOTIFY_DOWNLOAD_START", "false"},
		{"TELEGRAM_NOTIFY_DOWNLOAD_COMPLETE", "false"},
		{"TELEGRAM_NOTIFY_DOWNLOAD_FAILED", "false"},
		{"TELEGRAM_NOTIFY_AGENT_CHAT", "true"},
	} {
		values = append(values, fmt.Sprintf("(%s, %s, FALSE, %s)", sqlLiteral(item.key), sqlLiteral(item.value), now))
	}
	statement := "INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES " + strings.Join(values, ", ")
	if dialect == DialectMySQL {
		statement = strings.Replace(statement, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		statement += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 24, Name: "channel_notification_settings", Statements: []string{statement}}
}

// translationModelSettingsMigration 把翻译模型从对话 Agent 的 OpenAI 配置中拆成独立配置。
// 拆分本身会改变现有安装的行为，因此翻译引擎已是 openai 时先把当前生效的 Agent 配置复制一份，
// 保证升级前后翻译使用的接口、模型与密钥完全一致；目标键已存在时不覆盖，重复执行安全。
// API Key 与 Agent 使用同一 SESSION_SECRET 加密，直接复制密文即可解密。
func translationModelSettingsMigration(dialect Dialect) Migration {
	now := currentTimestampExpression(dialect)
	statements := make([]string, 0, 4)
	for _, copy := range [][3]string{
		{"OPENAI_URL", "TRANSLATION_OPENAI_URL", "FALSE"},
		{"OPENAI_MODEL", "TRANSLATION_OPENAI_MODEL", "FALSE"},
		{"OPENAI_API_KEY", "TRANSLATION_OPENAI_API_KEY", "TRUE"},
	} {
		statements = append(statements, fmt.Sprintf(
			"INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) SELECT %s, setting_value, %s, %s FROM app_settings WHERE setting_key = %s AND (SELECT setting_value FROM app_settings WHERE setting_key = 'TRANSLATION_ENGINE') = 'openai' AND NOT EXISTS (SELECT 1 FROM app_settings WHERE setting_key = %s)",
			sqlLiteral(copy[1]), copy[2], now, sqlLiteral(copy[0]), sqlLiteral(copy[1])))
	}
	values := []string{
		fmt.Sprintf("('TRANSLATION_OPENAI_URL', '', FALSE, %s)", now),
		fmt.Sprintf("('TRANSLATION_OPENAI_MODEL', '', FALSE, %s)", now),
		fmt.Sprintf("('TRANSLATION_OPENAI_API_KEY', '', TRUE, %s)", now),
	}
	insert := "INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES " + strings.Join(values, ", ")
	if dialect == DialectMySQL {
		insert = strings.Replace(insert, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		insert += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 23, Name: "translation_model_settings", Statements: append(statements, insert)}
}

// bypassProxySettingMigration 新增非敏感字符串布尔配置，默认 false；空值视为关闭。
// 复用既有非空 setting_value，不改表结构或历史配置，回退代码时保留此键即可。
func bypassProxySettingMigration(dialect Dialect) Migration {
	statement := "INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES ('BYPASS_USE_PROXY', 'false', FALSE, " + currentTimestampExpression(dialect) + ")"
	if dialect == DialectMySQL {
		statement = strings.Replace(statement, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		statement += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 21, Name: "bypass_proxy_setting", Statements: []string{statement}}
}

// siteAuthSettingsMigration 为两种凭据分别建立配置记录，新增模式默认密钥。
// 仅新增键值，不改表结构、不覆盖凭据；回退代码时保留新增配置即可。
func siteAuthSettingsMigration(dialect Dialect) Migration {
	values := []string{}
	for _, prefix := range []string{"PTT", "PTFANS", "ROUSIPRO", "NICEPT"} {
		values = append(values, fmt.Sprintf("(%s, 'key', FALSE, %s)", sqlLiteral(prefix+"_AUTH_TYPE"), currentTimestampExpression(dialect)))
		key := prefix + "_API_KEY"
		if prefix == "PTT" {
			key = "PTT_PASSKEY"
		}
		values = append(values, fmt.Sprintf("(%s, '', TRUE, %s)", sqlLiteral(key), currentTimestampExpression(dialect)))
	}
	values = append(values, fmt.Sprintf("('PTT_UID', '', FALSE, %s)", currentTimestampExpression(dialect)))
	insert := "INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES " + strings.Join(values, ", ")
	if dialect == DialectMySQL {
		insert = strings.Replace(insert, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		insert += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 19, Name: "site_auth_settings", Statements: []string{insert}}
}

// ptSiteSettingsMigration seeds credentials for the two requested sites without deleting legacy secrets.
// A retired main-site preference falls back to automatic selection; historical downloads remain unchanged.
func ptSiteSettingsMigration(dialect Dialect) Migration {
	values := []string{}
	for _, key := range []string{"PTFANS_COOKIE", "ROUSIPRO_COOKIE"} {
		values = append(values, fmt.Sprintf("(%s, '', TRUE, %s)", sqlLiteral(key), currentTimestampExpression(dialect)))
	}
	insert := "INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES " + strings.Join(values, ", ")
	if dialect == DialectMySQL {
		insert = strings.Replace(insert, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		insert += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 18, Name: "pt_site_settings", Statements: []string{
		insert,
		"UPDATE app_settings SET setting_value='ALL' WHERE setting_key='MAIN_SITE' AND setting_value='Rousi'",
	}}
}

// downloadTransferMigration stores downloader-observed status and times without changing historical task states.
// NULL means no verified value; rollback requires a deliberate table rebuild on SQLite.
func downloadTransferMigration(dialect Dialect) Migration {
	timeType := "TEXT"
	if dialect == DialectPostgres {
		timeType = "TIMESTAMPTZ"
	}
	if dialect == DialectMySQL {
		timeType = "DATETIME(6)"
	}
	return Migration{Version: 17, Name: "download_transfer_snapshots", Statements: []string{
		"ALTER TABLE download_tasks ADD COLUMN transfer_status VARCHAR(16) DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN added_at " + timeType + " DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN completed_at " + timeType + " DEFAULT NULL",
		"CREATE INDEX idx_download_tasks_transfer_status ON download_tasks(transfer_status)",
		"CREATE INDEX idx_download_tasks_added_at ON download_tasks(added_at)",
		"CREATE INDEX idx_download_tasks_completed_at ON download_tasks(completed_at)",
	}}
}

// subscriptionDownloadMigration records one idempotent attempt per subscription and resource.
// NULL candidate fields mean search has not found a resource; unknown submission requires client-side reconciliation.
func subscriptionDownloadMigration(dialect Dialect) Migration {
	columns := []string{
		"ALTER TABLE download_tasks ADD COLUMN subscription_id VARCHAR(64) DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN source_site VARCHAR(128) DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN source_kind VARCHAR(8) DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN resource_uri TEXT DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN info_hash VARCHAR(40) DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN downloader VARCHAR(32) DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN filter_passed BOOLEAN DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN lease_until BIGINT DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN lease_token VARCHAR(64) DEFAULT NULL",
		"ALTER TABLE download_tasks ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0",
		"CREATE INDEX idx_download_tasks_subscription_status ON download_tasks(subscription_id,status)",
		"CREATE INDEX idx_download_tasks_hash ON download_tasks(info_hash)",
	}
	if dialect == DialectMySQL {
		columns = append(columns, "ALTER TABLE download_tasks ADD COLUMN active_subscription_id VARCHAR(64) GENERATED ALWAYS AS (CASE WHEN status IN ('queued','searching','unknown','submitted','downloading','completed') THEN subscription_id ELSE NULL END) STORED", "CREATE UNIQUE INDEX idx_download_tasks_active_subscription ON download_tasks(active_subscription_id)")
	} else {
		columns = append(columns, "CREATE UNIQUE INDEX idx_download_tasks_active_subscription ON download_tasks(subscription_id) WHERE status IN ('queued','searching','unknown','submitted','downloading','completed')")
	}
	if dialect == DialectMySQL {
		columns[3] = "ALTER TABLE download_tasks ADD COLUMN resource_uri TEXT NULL"
	}
	return Migration{Version: 16, Name: "subscription_download_attempts", Statements: columns}
}

// mediaTypeMigration 新增影片类型：VARCHAR(32)，可空，默认 NULL（尚未分类）。
// 有码/无码/无码破解/流出单值存储；不推断或回填历史类型，回退程序时保留该列。
func mediaTypeMigration(dialect Dialect) Migration {
	column := "ALTER TABLE media ADD COLUMN video_type VARCHAR(32) DEFAULT NULL CHECK (video_type IN ('censored','uncensored','uncensored_cracked','leaked'))"
	if dialect == DialectMySQL {
		column = "ALTER TABLE media ADD COLUMN video_type VARCHAR(32) DEFAULT NULL COMMENT '影片类型：有码、无码、无码破解、流出；NULL 尚未分类' CHECK (video_type IN ('censored','uncensored','uncensored_cracked','leaked'))"
	}
	statements := []string{column, "CREATE INDEX idx_media_video_type_updated ON media(video_type, updated_at, id)"}
	if dialect == DialectPostgres {
		statements = append(statements, "COMMENT ON COLUMN media.video_type IS '影片类型：有码、无码、无码破解、流出；NULL 尚未分类'")
	}
	return Migration{Version: 15, Name: "add_media_video_type", Statements: statements}
}

// downloaderAndBypassSettingsMigration adds settings introduced after the initial
// default seed. It is idempotent so existing installations receive the fields
// without overwriting administrator values.
func downloaderAndBypassSettingsMigration(dialect Dialect) Migration {
	keys := []struct {
		key, value string
		secret     bool
	}{
		{key: "ARIA2_URL"}, {key: "ARIA2_SECRET", secret: true}, {key: "ARIA2_DOWNLOAD_PATH"},
		{key: "PT_DEFAULT_DOWNLOADER", value: "qbittorrent"}, {key: "BT_DEFAULT_DOWNLOADER", value: "qbittorrent"},
		{key: "BYPASS_ENGINE"},
	}
	values := make([]string, 0, len(keys))
	for _, item := range keys {
		secret := "FALSE"
		if item.secret {
			secret = "TRUE"
		}
		values = append(values, fmt.Sprintf("(%s, %s, %s, %s)", sqlLiteral(item.key), sqlLiteral(item.value), secret, currentTimestampExpression(dialect)))
	}
	statement := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES %s", strings.Join(values, ", "))
	if dialect == DialectMySQL {
		statement = strings.Replace(statement, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		statement += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 12, Name: "persist_downloader_and_bypass_settings", Statements: []string{statement}}
}

// catalogQueryIndexesMigration adds the indexes used by the release and recommendation projections.
func catalogQueryIndexesMigration(dialect Dialect) Migration {
	return Migration{Version: 11, Name: "add_catalog_query_indexes", Statements: []string{
		"CREATE INDEX idx_media_release_date_code ON media (release_date, code)",
		"CREATE INDEX idx_media_release_subscription ON media (release_date, subscription_status)",
		"CREATE INDEX idx_legacy_metadata_status_media ON legacy_media_metadata (legacy_status, media_id)",
	}}
}

// cleanupCanceledSubscriptionsMigration removes rows written by the former soft-cancel behavior.
// Cancellation is now a physical delete, so old canceled rows must not remain in storage or reappear in lists.
func cleanupCanceledSubscriptionsMigration(dialect Dialect) Migration {
	return Migration{Version: 10, Name: "remove_canceled_subscription_rows", Statements: []string{
		"DELETE FROM subscriptions WHERE status = 'canceled'",
		"UPDATE media SET subscription_status = CASE WHEN EXISTS (SELECT 1 FROM subscriptions s WHERE s.media_id = media.id AND s.status = 'active') THEN 'active' ELSE 'none' END WHERE subscription_status = 'canceled'",
	}}
}

// logRetentionSettingMigration adds the default system-log retention period without overriding existing settings.
func logRetentionSettingMigration(dialect Dialect) Migration {
	statement := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES (%s, %s, FALSE, %s)", sqlLiteral("LOG_RETENTION_DAYS"), sqlLiteral("30"), currentTimestampExpression(dialect))
	if dialect == DialectMySQL {
		statement = strings.Replace(statement, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		statement += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 9, Name: "persist_log_retention_setting", Statements: []string{statement}}
}

// systemLogsMigration creates the durable source used by the management log page.
func systemLogsMigration(dialect Dialect) Migration {
	var table string
	switch dialect {
	case DialectSQLite:
		table = "CREATE TABLE system_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, logged_at TEXT NOT NULL, level TEXT NOT NULL, category TEXT NOT NULL, message TEXT NOT NULL, attrs_json TEXT NOT NULL DEFAULT '{}')"
	case DialectPostgres:
		table = "CREATE TABLE system_logs (id BIGSERIAL PRIMARY KEY, logged_at TIMESTAMPTZ NOT NULL, level TEXT NOT NULL, category TEXT NOT NULL, message TEXT NOT NULL, attrs_json JSONB NOT NULL DEFAULT '{}'::jsonb)"
	case DialectMySQL:
		table = "CREATE TABLE system_logs (id BIGINT AUTO_INCREMENT PRIMARY KEY, logged_at DATETIME(6) NOT NULL, level VARCHAR(16) NOT NULL, category VARCHAR(32) NOT NULL, message TEXT NOT NULL, attrs_json JSON NOT NULL)"
	}
	return Migration{Version: 8, Name: "create_system_logs", Statements: []string{
		table,
		"CREATE INDEX idx_system_logs_logged_at ON system_logs (logged_at)",
		"CREATE INDEX idx_system_logs_category_level ON system_logs (category, level)",
	}}
}

func settingsMigration(dialect Dialect) Migration {
	columnTypes := map[Dialect]string{
		DialectSQLite:   "TEXT PRIMARY KEY, setting_value TEXT NOT NULL, is_secret BOOLEAN NOT NULL DEFAULT FALSE, updated_at TEXT NOT NULL",
		DialectPostgres: "TEXT PRIMARY KEY, setting_value TEXT NOT NULL, is_secret BOOLEAN NOT NULL DEFAULT FALSE, updated_at TIMESTAMPTZ NOT NULL",
		DialectMySQL:    "VARCHAR(128) PRIMARY KEY, setting_value TEXT NOT NULL, is_secret BOOLEAN NOT NULL DEFAULT FALSE, updated_at DATETIME(6) NOT NULL",
	}
	return Migration{Version: 5, Name: "create_app_settings", Statements: []string{fmt.Sprintf("CREATE TABLE app_settings (setting_key %s)", columnTypes[dialect])}}
}

func activeSubscriptionMigration(dialect Dialect) Migration {
	if dialect == DialectMySQL {
		return Migration{Version: 6, Name: "enforce_one_active_subscription_per_media", Statements: []string{
			`ALTER TABLE subscriptions ADD COLUMN active_media_id VARCHAR(64) GENERATED ALWAYS AS (CASE WHEN status = 'active' THEN media_id ELSE NULL END) STORED`,
			`CREATE UNIQUE INDEX idx_subscriptions_one_active_per_media ON subscriptions (active_media_id)`,
		}}
	}
	return Migration{Version: 6, Name: "enforce_one_active_subscription_per_media", Statements: []string{
		`CREATE UNIQUE INDEX idx_subscriptions_one_active_per_media ON subscriptions (media_id) WHERE status = 'active'`,
	}}
}

// defaultSettingsMigration persists the product defaults so all runtime setting reads have one database source.
// Existing administrator values, including deliberately saved empty strings, are never overwritten.
func defaultSettingsMigration(dialect Dialect) Migration {
	type settingSeed struct {
		key, value string
		secret     bool
	}
	defaults := []settingSeed{
		{key: "MTEAM_API_KEY", secret: true}, {key: "PTT_COOKIE", secret: true}, {key: "ROUSI_COOKIE", secret: true}, {key: "NICEPT_COOKIE", secret: true},
		{key: "EMBY_URL"}, {key: "EMBY_API_KEY", secret: true}, {key: "PLEX_URL"}, {key: "PLEX_TOKEN", secret: true},
		{key: "JELLYFIN_URL"}, {key: "JELLYFIN_API_KEY", secret: true}, {key: "JELLYFIN_USER"},
		{key: "WECHAT_CORP_ID"}, {key: "WECHAT_CORP_SECRET", secret: true}, {key: "WECHAT_AGENT_ID"}, {key: "WECHAT_PROXY"}, {key: "WECHAT_PHOTO"},
		{key: "WECHAT_TOKEN", secret: true}, {key: "WECHAT_ENCODING_AES_KEY", secret: true}, {key: "WECHAT_TO_USER", value: "@all"}, {key: "WECHAT_BANNER", value: "false"},
		{key: "TELEGRAM_BOT_TOKEN", secret: true}, {key: "TELEGRAM_CHAT_ID"}, {key: "TELEGRAM_WHITELIST"}, {key: "TELEGRAM_SPOILER", value: "false"},
		{key: "QBITTORRENT_URL"}, {key: "QBITTORRENT_USERNAME"}, {key: "QBITTORRENT_PASSWORD", secret: true}, {key: "QBITTORRENT_DOWNLOAD_PATH"}, {key: "QBITTORRENT_CATEGORY"},
		{key: "ARIA2_URL"}, {key: "ARIA2_SECRET", secret: true}, {key: "ARIA2_DOWNLOAD_PATH"},
		{key: "PT_DEFAULT_DOWNLOADER", value: "qbittorrent"}, {key: "BT_DEFAULT_DOWNLOADER", value: "qbittorrent"},
		{key: "TRANSMISSION_URL"}, {key: "TRANSMISSION_USERNAME"}, {key: "TRANSMISSION_PASSWORD", secret: true}, {key: "TRANSMISSION_DOWNLOAD_PATH"}, {key: "TRANSMISSION_LABEL"},
		{key: "THUNDER_URL"}, {key: "THUNDER_FILE_ID"}, {key: "THUNDER_AUTHORIZATION", secret: true},
		{key: "CLOUDNAS_URL"}, {key: "CLOUDNAS_USERNAME"}, {key: "CLOUDNAS_PASSWORD", secret: true}, {key: "CLOUDNAS_SAVEPATH", value: "/115open"},
		{key: "DEFAULT_FILTER", value: `{"only_chinese":false,"only_uc":false,"exclude_uc":false,"only_uhd":false,"only_free":false,"exclude_uhd":true,"include_keywords":"","exclude_keywords":"","min_size":"","max_size":""}`},
		{key: "DEFAULT_SORT", value: "free,chinese,uc,!uc,site,seeders,!uhd,uhd"}, {key: "MAIN_SITE", value: "ALL"},
		{key: "RANK_PAGE"}, {key: "RANK_TYPE"}, {key: "BRAND_TYPE"}, {key: "RANK_SCHEDULE_TIME", value: "0 20 * * *"},
		{key: "ACTOR_SCHEDULE_TIME", value: "0 21 * * *"}, {key: "TAG_SCHEDULE_TIME", value: "30 21 * * *"}, {key: "DOWNLOAD_SCHEDULE_TIME", value: "0 22 * * *"},
		{key: "MAX_ACTOR", value: "3"}, {key: "TAG_MAX_SUB_PER_RUN", value: "10"}, {key: "PT_SEARCH_INTERVAL"},
		{key: "BAIDU_APP_ID"}, {key: "BAIDU_API_KEY", secret: true}, {key: "GOOGLE_API_KEY", secret: true}, {key: "DEEPLX_URL"},
		{key: "TRANSLATION_ENGINE", value: "none"}, {key: "TRANSLATION_PROMPT"},
		{key: "OPENAI_URL"}, {key: "OPENAI_MODEL"}, {key: "OPENAI_API_KEY", secret: true}, {key: "AGENT_ENABLE", value: "false"}, {key: "AGENT_SYSTEM_PROMPT"},
		{key: "IMAGE_MODE", value: "BLUR"}, {key: "PROXY"}, {key: "EXTERNAL_DOMAIN"}, {key: "BYPASS_ENGINE", value: ""}, {key: "BYPASS_URL"}, {key: "JAVDB_HOST", value: "https://apidd.czssdgz.com"},
		{key: "ENABLE_BT_ANTI_LEECH", value: "true"}, {key: "ENABLE_PHOTO_CACHE", value: "false"}, {key: "ENABLE_AUTO_COMPLETE", value: "true"},
	}
	values := make([]string, 0, len(defaults))
	for _, item := range defaults {
		secret := "FALSE"
		if item.secret {
			secret = "TRUE"
		}
		values = append(values, fmt.Sprintf("(%s, %s, %s, %s)", sqlLiteral(item.key), sqlLiteral(item.value), secret, currentTimestampExpression(dialect)))
	}
	statement := fmt.Sprintf("INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES %s", strings.Join(values, ", "))
	if dialect == DialectMySQL {
		statement = strings.Replace(statement, "INSERT INTO", "INSERT IGNORE INTO", 1)
	} else {
		statement += " ON CONFLICT (setting_key) DO NOTHING"
	}
	return Migration{Version: 7, Name: "persist_default_settings", Statements: []string{statement}}
}

func currentTimestampExpression(dialect Dialect) string {
	switch dialect {
	case DialectSQLite:
		return "strftime('%Y-%m-%dT%H:%M:%fZ', 'now')"
	case DialectMySQL:
		return "CURRENT_TIMESTAMP(6)"
	default:
		return "CURRENT_TIMESTAMP"
	}
}

func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func migrate(ctx context.Context, db *sql.DB, dialect Dialect) error {
	plan := MigrationPlan(dialect)
	if len(plan) == 0 {
		return fmt.Errorf("unsupported migration dialect %q", dialect)
	}
	if err := ensureMigrationTable(ctx, db, dialect); err != nil {
		return err
	}
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	for _, migration := range plan {
		if applied[migration.Version] {
			continue
		}
		if err := applyMigration(ctx, db, dialect, migration); err != nil {
			return err
		}
	}
	return nil
}

func ensureMigrationTable(ctx context.Context, db *sql.DB, dialect Dialect) error {
	var ddl string
	switch dialect {
	case DialectSQLite:
		ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`
	case DialectPostgres:
		ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version BIGINT PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
	case DialectMySQL:
		ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version BIGINT PRIMARY KEY,
	name VARCHAR(255) NOT NULL,
	applied_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
)`
	default:
		return fmt.Errorf("unsupported migration dialect %q", dialect)
	}
	_, err := db.ExecContext(ctx, ddl)
	return err
}

func appliedMigrations(ctx context.Context, db *sql.DB) (map[int64]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := make(map[int64]bool)
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return applied, nil
}

func applyMigration(ctx context.Context, db *sql.DB, dialect Dialect, migration Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, statement := range migration.Statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration %d %s: %w", migration.Version, migration.Name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO schema_migrations (version, name) VALUES (%s, %s)`, placeholder(dialect, 1), placeholder(dialect, 2)), migration.Version, migration.Name); err != nil {
		return fmt.Errorf("record migration %d %s: %w", migration.Version, migration.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func sqliteMigrations() []Migration {
	return []Migration{{Version: 1, Name: "create_media_subscriptions_downloads", Statements: []string{
		sqliteMediaTable("media"),
		`CREATE UNIQUE INDEX idx_media_code ON media (code)`,
		`CREATE INDEX idx_media_subscription_status ON media (subscription_status)`,
		`CREATE INDEX idx_media_updated_at ON media (updated_at)`,
		sqliteMediaTable("movies"),
		`CREATE UNIQUE INDEX idx_movies_code ON movies (code)`,
		`CREATE TABLE subscriptions (
	id TEXT PRIMARY KEY,
	media_id TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('none', 'active', 'canceled')),
	mode TEXT NOT NULL CHECK (mode IN ('strict', 'preload')),
	filter_json TEXT NOT NULL DEFAULT '{}',
	idempotency_key TEXT NOT NULL,
	idempotency_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
	FOREIGN KEY (media_id) REFERENCES media(id) ON UPDATE CASCADE ON DELETE RESTRICT
)`,
		`CREATE UNIQUE INDEX idx_subscriptions_idempotency_key ON subscriptions (idempotency_key)`,
		`CREATE INDEX idx_subscriptions_media_status ON subscriptions (media_id, status)`,
		`CREATE INDEX idx_subscriptions_status_updated_at ON subscriptions (status, updated_at)`,
		`CREATE TABLE download_tasks (
	id TEXT PRIMARY KEY,
	media_id TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('queued', 'searching', 'submitted', 'downloading', 'completed', 'failed', 'unknown')),
	external_id TEXT,
	error_message TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY (media_id) REFERENCES media(id) ON UPDATE CASCADE ON DELETE RESTRICT
)`,
		`CREATE INDEX idx_download_tasks_media_status ON download_tasks (media_id, status)`,
		`CREATE INDEX idx_download_tasks_status_updated_at ON download_tasks (status, updated_at)`,
		`CREATE INDEX idx_download_tasks_external_id ON download_tasks (external_id)`,
	}}, {Version: 2, Name: "create_actors", Statements: []string{`CREATE TABLE actors (name TEXT PRIMARY KEY, photo TEXT, limit_date TEXT, created_at TEXT, updated_at TEXT)`, `CREATE INDEX idx_actors_limit_date ON actors (limit_date)`}}, {Version: 3, Name: "create_legacy_media_metadata", Statements: []string{`CREATE TABLE legacy_media_metadata (media_id TEXT PRIMARY KEY, code TEXT NOT NULL, banner_url TEXT, preview_url TEXT, genres TEXT, casts TEXT, producer TEXT, publisher TEXT, series TEXT, still_photo TEXT, local_banner TEXT, local_still_photo TEXT, legacy_status TEXT NOT NULL, legacy_mode TEXT NOT NULL, legacy_filter TEXT, legacy_star INTEGER, FOREIGN KEY (media_id) REFERENCES media(id) ON UPDATE CASCADE ON DELETE CASCADE)`, `CREATE UNIQUE INDEX idx_legacy_media_metadata_code ON legacy_media_metadata (code)`}}, {Version: 4, Name: "create_rank_entries", Statements: []string{`CREATE TABLE rank_entries (rank_type TEXT NOT NULL, position INTEGER NOT NULL CHECK (position > 0), code TEXT NOT NULL, source_created_at TEXT, PRIMARY KEY (rank_type, position))`, `CREATE INDEX idx_rank_entries_code ON rank_entries (code)`}}}
}

func postgresMigrations() []Migration {
	return []Migration{{Version: 1, Name: "create_media_subscriptions_downloads", Statements: []string{
		postgresMediaTable("media"),
		`CREATE UNIQUE INDEX idx_media_code ON media (code)`,
		`CREATE INDEX idx_media_subscription_status ON media (subscription_status)`,
		`CREATE INDEX idx_media_updated_at ON media (updated_at)`,
		postgresMediaTable("movies"),
		`CREATE UNIQUE INDEX idx_movies_code ON movies (code)`,
		`CREATE TABLE subscriptions (
	id TEXT PRIMARY KEY,
	media_id TEXT NOT NULL REFERENCES media(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	status TEXT NOT NULL CHECK (status IN ('none', 'active', 'canceled')),
	mode TEXT NOT NULL CHECK (mode IN ('strict', 'preload')),
	filter_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	idempotency_key TEXT NOT NULL,
	idempotency_hash TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL,
	version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1)
)`,
		`CREATE UNIQUE INDEX idx_subscriptions_idempotency_key ON subscriptions (idempotency_key)`,
		`CREATE INDEX idx_subscriptions_media_status ON subscriptions (media_id, status)`,
		`CREATE INDEX idx_subscriptions_status_updated_at ON subscriptions (status, updated_at)`,
		`CREATE TABLE download_tasks (
	id TEXT PRIMARY KEY,
	media_id TEXT NOT NULL REFERENCES media(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	status TEXT NOT NULL CHECK (status IN ('queued', 'searching', 'submitted', 'downloading', 'completed', 'failed', 'unknown')),
	external_id TEXT,
	error_message TEXT,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
)`,
		`CREATE INDEX idx_download_tasks_media_status ON download_tasks (media_id, status)`,
		`CREATE INDEX idx_download_tasks_status_updated_at ON download_tasks (status, updated_at)`,
		`CREATE INDEX idx_download_tasks_external_id ON download_tasks (external_id)`,
	}}, {Version: 2, Name: "create_actors", Statements: []string{`CREATE TABLE actors (name TEXT PRIMARY KEY, photo TEXT, limit_date DATE, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ)`, `CREATE INDEX idx_actors_limit_date ON actors (limit_date)`}}, {Version: 3, Name: "create_legacy_media_metadata", Statements: []string{`CREATE TABLE legacy_media_metadata (media_id TEXT PRIMARY KEY REFERENCES media(id) ON UPDATE CASCADE ON DELETE CASCADE, code TEXT NOT NULL, banner_url TEXT, preview_url TEXT, genres TEXT, casts TEXT, producer TEXT, publisher TEXT, series TEXT, still_photo TEXT, local_banner TEXT, local_still_photo TEXT, legacy_status TEXT NOT NULL, legacy_mode TEXT NOT NULL, legacy_filter TEXT, legacy_star BIGINT)`, `CREATE UNIQUE INDEX idx_legacy_media_metadata_code ON legacy_media_metadata (code)`}}, {Version: 4, Name: "create_rank_entries", Statements: []string{`CREATE TABLE rank_entries (rank_type TEXT NOT NULL, position INTEGER NOT NULL CHECK (position > 0), code TEXT NOT NULL, source_created_at TIMESTAMPTZ, PRIMARY KEY (rank_type, position))`, `CREATE INDEX idx_rank_entries_code ON rank_entries (code)`}}}
}

func mysqlMigrations() []Migration {
	return []Migration{{Version: 1, Name: "create_media_subscriptions_downloads", Statements: []string{
		mysqlMediaTable("media"),
		`CREATE UNIQUE INDEX idx_media_code ON media (code)`,
		`CREATE INDEX idx_media_subscription_status ON media (subscription_status)`,
		`CREATE INDEX idx_media_updated_at ON media (updated_at)`,
		mysqlMediaTable("movies"),
		`CREATE UNIQUE INDEX idx_movies_code ON movies (code)`,
		`CREATE TABLE subscriptions (
	id VARCHAR(26) PRIMARY KEY,
	media_id VARCHAR(64) NOT NULL,
	status VARCHAR(32) NOT NULL CHECK (status IN ('none', 'active', 'canceled')),
	mode VARCHAR(32) NOT NULL CHECK (mode IN ('strict', 'preload')),
	filter_json JSON NOT NULL,
	idempotency_key VARCHAR(128) NOT NULL,
	idempotency_hash CHAR(64) NOT NULL,
	created_at DATETIME(6) NOT NULL,
	updated_at DATETIME(6) NOT NULL,
	version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
	CONSTRAINT fk_subscriptions_media FOREIGN KEY (media_id) REFERENCES media(id) ON UPDATE CASCADE ON DELETE RESTRICT
)`,
		`CREATE UNIQUE INDEX idx_subscriptions_idempotency_key ON subscriptions (idempotency_key)`,
		`CREATE INDEX idx_subscriptions_media_status ON subscriptions (media_id, status)`,
		`CREATE INDEX idx_subscriptions_status_updated_at ON subscriptions (status, updated_at)`,
		`CREATE TABLE download_tasks (
	id VARCHAR(26) PRIMARY KEY,
	media_id VARCHAR(64) NOT NULL,
	status VARCHAR(32) NOT NULL CHECK (status IN ('queued', 'searching', 'submitted', 'downloading', 'completed', 'failed', 'unknown')),
	external_id VARCHAR(255),
	error_message TEXT,
	created_at DATETIME(6) NOT NULL,
	updated_at DATETIME(6) NOT NULL,
	CONSTRAINT fk_download_tasks_media FOREIGN KEY (media_id) REFERENCES media(id) ON UPDATE CASCADE ON DELETE RESTRICT
)`,
		`CREATE INDEX idx_download_tasks_media_status ON download_tasks (media_id, status)`,
		`CREATE INDEX idx_download_tasks_status_updated_at ON download_tasks (status, updated_at)`,
		`CREATE INDEX idx_download_tasks_external_id ON download_tasks (external_id)`,
	}}, {Version: 2, Name: "create_actors", Statements: []string{`CREATE TABLE actors (name VARCHAR(255) PRIMARY KEY, photo VARCHAR(2048), limit_date DATE, created_at DATETIME(6), updated_at DATETIME(6))`, `CREATE INDEX idx_actors_limit_date ON actors (limit_date)`}}, {Version: 3, Name: "create_legacy_media_metadata", Statements: []string{`CREATE TABLE legacy_media_metadata (media_id VARCHAR(64) PRIMARY KEY, code VARCHAR(128) NOT NULL, banner_url TEXT, preview_url TEXT, genres TEXT, casts TEXT, producer TEXT, publisher TEXT, series TEXT, still_photo TEXT, local_banner TEXT, local_still_photo TEXT, legacy_status VARCHAR(32) NOT NULL, legacy_mode VARCHAR(32) NOT NULL, legacy_filter TEXT, legacy_star BIGINT, CONSTRAINT fk_legacy_metadata_media FOREIGN KEY (media_id) REFERENCES media(id) ON UPDATE CASCADE ON DELETE CASCADE)`, `CREATE UNIQUE INDEX idx_legacy_media_metadata_code ON legacy_media_metadata (code)`}}, {Version: 4, Name: "create_rank_entries", Statements: []string{`CREATE TABLE rank_entries (rank_type VARCHAR(128) NOT NULL, position INTEGER NOT NULL CHECK (position > 0), code VARCHAR(128) NOT NULL, source_created_at DATETIME(6), PRIMARY KEY (rank_type, position))`, `CREATE INDEX idx_rank_entries_code ON rank_entries (code)`}}}
}

func sqliteMediaTable(name string) string {
	return fmt.Sprintf(`CREATE TABLE %s (
	id TEXT PRIMARY KEY,
	code TEXT NOT NULL,
	title TEXT NOT NULL,
	translated_title TEXT,
	poster_url TEXT,
	release_date TEXT,
	duration_minutes INTEGER CHECK (duration_minutes IS NULL OR duration_minutes >= 0),
	subscription_status TEXT NOT NULL DEFAULT 'none' CHECK (subscription_status IN ('none', 'active', 'canceled')),
	library_status TEXT NOT NULL DEFAULT 'unknown' CHECK (library_status IN ('unknown', 'absent', 'present')),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`, name)
}

func postgresMediaTable(name string) string {
	return fmt.Sprintf(`CREATE TABLE %s (
	id TEXT PRIMARY KEY,
	code TEXT NOT NULL,
	title TEXT NOT NULL,
	translated_title TEXT,
	poster_url TEXT,
	release_date DATE,
	duration_minutes INTEGER CHECK (duration_minutes IS NULL OR duration_minutes >= 0),
	subscription_status TEXT NOT NULL DEFAULT 'none' CHECK (subscription_status IN ('none', 'active', 'canceled')),
	library_status TEXT NOT NULL DEFAULT 'unknown' CHECK (library_status IN ('unknown', 'absent', 'present')),
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
)`, name)
}

func mysqlMediaTable(name string) string {
	return fmt.Sprintf(`CREATE TABLE %s (
	id VARCHAR(64) PRIMARY KEY,
	code VARCHAR(128) NOT NULL,
	title VARCHAR(512) NOT NULL,
	translated_title VARCHAR(512),
	poster_url VARCHAR(2048),
	release_date DATE,
	duration_minutes INTEGER CHECK (duration_minutes IS NULL OR duration_minutes >= 0),
	subscription_status VARCHAR(32) NOT NULL DEFAULT 'none' CHECK (subscription_status IN ('none', 'active', 'canceled')),
	library_status VARCHAR(32) NOT NULL DEFAULT 'unknown' CHECK (library_status IN ('unknown', 'absent', 'present')),
	created_at DATETIME(6) NOT NULL,
	updated_at DATETIME(6) NOT NULL
)`, name)
}

func joinMigrationSQL(plan []Migration) string {
	var builder strings.Builder
	for _, migration := range plan {
		for _, statement := range migration.Statements {
			builder.WriteString(statement)
			builder.WriteString(";\n")
		}
	}
	return builder.String()
}
