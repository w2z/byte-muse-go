package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"bytemuse/backend/internal/ports"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// Open creates a database store, verifies connectivity, and applies dialect-specific connection settings.
func Open(ctx context.Context, cfg Config) (Store, error) {
	dialect := cfg.Dialect
	if dialect == "" {
		dialect = detectDialect(cfg)
	}
	if dialect == "" {
		return nil, fmt.Errorf("database dialect is required")
	}

	driver, dsn, err := driverAndDSN(dialect, cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	store := &sqlStore{db: db, dialect: dialect, exec: db, root: db}
	if err := configurePool(ctx, db, dialect, cfg); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func detectDialect(cfg Config) Dialect {
	if cfg.SQLitePath != "" {
		return DialectSQLite
	}
	lower := strings.ToLower(cfg.DSN)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"), strings.Contains(lower, "sslmode="):
		return DialectPostgres
	case strings.Contains(lower, "@tcp("), strings.Contains(lower, "parseTime="):
		return DialectMySQL
	default:
		return ""
	}
}

func driverAndDSN(dialect Dialect, cfg Config) (string, string, error) {
	switch dialect {
	case DialectSQLite:
		path := cfg.SQLitePath
		if path == "" {
			path = cfg.DSN
		}
		if path == "" {
			return "", "", fmt.Errorf("sqlite path is required")
		}
		return "sqlite", path, nil
	case DialectPostgres:
		if cfg.DSN == "" {
			return "", "", fmt.Errorf("postgres dsn is required")
		}
		return "postgres", cfg.DSN, nil
	case DialectMySQL:
		if cfg.DSN == "" {
			return "", "", fmt.Errorf("mysql dsn is required")
		}
		return "mysql", ensureMySQLParseTime(cfg.DSN), nil
	default:
		return "", "", fmt.Errorf("unsupported database dialect %q", dialect)
	}
}

func ensureMySQLParseTime(dsn string) string {
	if strings.Contains(strings.ToLower(dsn), "parsetime=") {
		return dsn
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "parseTime=true&loc=UTC"
}

func configurePool(ctx context.Context, db *sql.DB, dialect Dialect, cfg Config) error {
	switch dialect {
	case DialectSQLite:
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		for _, pragma := range []string{
			"PRAGMA busy_timeout = 5000",
			"PRAGMA foreign_keys = ON",
			"PRAGMA journal_mode = WAL",
		} {
			if _, err := db.ExecContext(ctx, pragma); err != nil {
				return fmt.Errorf("configure sqlite %s: %w", pragma, err)
			}
		}
	default:
		if cfg.MaxOpenConns > 0 {
			db.SetMaxOpenConns(cfg.MaxOpenConns)
		}
		if cfg.MaxIdleConns > 0 {
			db.SetMaxIdleConns(cfg.MaxIdleConns)
		}
	}
	return nil
}

type sqlStore struct {
	db      *sql.DB
	dialect Dialect
	exec    sqlExecutor
	root    *sql.DB
}

type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *sqlStore) Migrate(ctx context.Context) error {
	return migrate(ctx, s.root, s.dialect)
}

func (s *sqlStore) WithTx(ctx context.Context, fn func(Store) error) error {
	if _, ok := s.exec.(*sql.Tx); ok {
		return fn(s)
	}
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txStore := &sqlStore{db: s.db, dialect: s.dialect, exec: tx, root: s.root}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) Media() ports.MediaRepository {
	return &sqlMediaRepository{dialect: s.dialect, exec: s.exec}
}

// MediaLibrary 返回「已在媒体库」的登记入口；写操作需要独立事务，因此直接持有连接池。
func (s *sqlStore) MediaLibrary() ports.MediaLibraryWriter {
	return &sqlMediaLibraryRepository{dialect: s.dialect, db: s.root}
}

func (s *sqlStore) Subscriptions() ports.SubscriptionRepository {
	return &sqlSubscriptionRepository{dialect: s.dialect, exec: s.exec, db: s.db}
}

func (s *sqlStore) Downloads() ports.DownloadRepository {
	return &sqlDownloadRepository{dialect: s.dialect, exec: s.exec}
}

func (s *sqlStore) ReadinessProbe() ports.ReadinessProbe {
	return &sqlReadinessProbe{dialect: s.dialect, exec: s.exec}
}

func (s *sqlStore) SQLDB() *sql.DB {
	return s.root
}

func (s *sqlStore) Close() error {
	if _, ok := s.exec.(*sql.Tx); ok {
		return nil
	}
	return s.root.Close()
}

func placeholder(dialect Dialect, index int) string {
	if dialect == DialectPostgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func placeholders(dialect Dialect, count int, start int) string {
	values := make([]string, count)
	for i := range count {
		values[i] = placeholder(dialect, start+i)
	}
	return strings.Join(values, ", ")
}

func mysqlQuoteIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func addQueryParam(raw string, key string, value string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return raw
	}
	query := parsed.Query()
	if query.Get(key) == "" {
		query.Set(key, value)
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}
