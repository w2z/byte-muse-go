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
	return append(plan, settingsMigration(dialect), activeSubscriptionMigration(dialect), defaultSettingsMigration(dialect), systemLogsMigration(dialect), logRetentionSettingMigration(dialect), cleanupCanceledSubscriptionsMigration(dialect))
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
		{key: "IMAGE_MODE", value: "BLUR"}, {key: "PROXY"}, {key: "EXTERNAL_DOMAIN"}, {key: "BYPASS_URL"}, {key: "JAVDB_HOST", value: "https://apidd.czssdgz.com"},
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
