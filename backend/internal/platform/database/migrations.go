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
	switch dialect {
	case DialectSQLite:
		return sqliteMigrations()
	case DialectPostgres:
		return postgresMigrations()
	case DialectMySQL:
		return mysqlMigrations()
	default:
		return nil
	}
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
	}}, {Version: 2, Name: "create_actors", Statements: []string{`CREATE TABLE actors (name TEXT PRIMARY KEY, photo TEXT, limit_date TEXT, created_at TEXT, updated_at TEXT)`, `CREATE INDEX idx_actors_limit_date ON actors (limit_date)`}}}
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
	}}, {Version: 2, Name: "create_actors", Statements: []string{`CREATE TABLE actors (name TEXT PRIMARY KEY, photo TEXT, limit_date DATE, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ)`, `CREATE INDEX idx_actors_limit_date ON actors (limit_date)`}}}
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
	}}, {Version: 2, Name: "create_actors", Statements: []string{`CREATE TABLE actors (name VARCHAR(255) PRIMARY KEY, photo VARCHAR(2048), limit_date DATE, created_at DATETIME(6), updated_at DATETIME(6))`, `CREATE INDEX idx_actors_limit_date ON actors (limit_date)`}}}
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
