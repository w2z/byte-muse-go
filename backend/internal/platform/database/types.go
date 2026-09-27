package database

import (
	"context"
	"database/sql"

	"bytemuse/backend/internal/ports"
)

// Dialect names one supported SQL database family and selects its migration plan.
type Dialect string

const (
	// DialectSQLite selects local SQLite storage with WAL, foreign keys, and busy timeout enabled.
	DialectSQLite Dialect = "sqlite"
	// DialectPostgres selects PostgreSQL 16+ storage.
	DialectPostgres Dialect = "postgres"
	// DialectMySQL selects MySQL 8.0+ storage.
	DialectMySQL Dialect = "mysql"
)

// Config contains database connection settings. SQLite uses SQLitePath; PostgreSQL and MySQL use DSN.
type Config struct {
	Dialect      Dialect
	SQLitePath   string
	DSN          string
	MaxOpenConns int
	MaxIdleConns int
}

// Store exposes concrete database adapters while keeping application services behind ports interfaces.
type Store interface {
	Migrate(ctx context.Context) error
	WithTx(ctx context.Context, fn func(Store) error) error
	Media() ports.MediaRepository
	Subscriptions() ports.SubscriptionRepository
	Downloads() ports.DownloadRepository
	ReadinessProbe() ports.ReadinessProbe
	SQLDB() *sql.DB
	Close() error
}
