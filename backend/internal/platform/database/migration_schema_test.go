package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"bytemuse/backend/internal/ports"
)

func TestMigrationDefinitionsShareVersionAndLogicalSchema(t *testing.T) {
	t.Parallel()
	sqlite := MigrationPlan(DialectSQLite)
	postgres := MigrationPlan(DialectPostgres)
	mysql := MigrationPlan(DialectMySQL)

	if len(sqlite) == 0 {
		t.Fatal("sqlite migration plan is empty")
	}
	if len(sqlite) != len(postgres) || len(sqlite) != len(mysql) {
		t.Fatalf("migration plan lengths sqlite/postgres/mysql = %d/%d/%d", len(sqlite), len(postgres), len(mysql))
	}
	for i := range sqlite {
		if sqlite[i].Version != postgres[i].Version || sqlite[i].Version != mysql[i].Version {
			t.Fatalf("migration version[%d] sqlite/postgres/mysql = %d/%d/%d", i, sqlite[i].Version, postgres[i].Version, mysql[i].Version)
		}
		if sqlite[i].Name != postgres[i].Name || sqlite[i].Name != mysql[i].Name {
			t.Fatalf("migration name[%d] sqlite/postgres/mysql = %q/%q/%q", i, sqlite[i].Name, postgres[i].Name, mysql[i].Name)
		}
	}

	wantTokens := []string{
		"media", "movies", "subscriptions", "download_tasks", "actors", "idx_actors_limit_date",
		"subscription_status", "library_status", "status",
		"idempotency_key", "idempotency_hash",
		"idx_media_code", "idx_subscriptions_media_status", "idx_download_tasks_status_updated_at",
	}
	for _, plan := range []struct {
		name    string
		dialect Dialect
	}{
		{name: "sqlite", dialect: DialectSQLite},
		{name: "postgres", dialect: DialectPostgres},
		{name: "mysql", dialect: DialectMySQL},
	} {
		ddl := strings.ToLower(joinMigrationSQL(MigrationPlan(plan.dialect)))
		for _, token := range wantTokens {
			if !strings.Contains(ddl, token) {
				t.Fatalf("%s ddl missing %q:\n%s", plan.name, token, ddl)
			}
		}
		for _, enumValue := range []string{"none", "active", "canceled", "queued", "searching", "submitted", "downloading", "completed", "failed", "unknown", "absent", "present"} {
			if !strings.Contains(ddl, "'"+enumValue+"'") {
				t.Fatalf("%s ddl missing enum value %q:\n%s", plan.name, enumValue, ddl)
			}
		}
	}
}

func TestSQLiteMigrationStructureAndPragmas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openMigratedSQLiteStore(t, ctx)
	t.Cleanup(func() { _ = store.Close() })

	db := store.SQLDB()
	assertSQLitePragma(t, db, "foreign_keys", "1")
	assertSQLitePragma(t, db, "journal_mode", "wal")

	for table, columns := range map[string][]string{
		"media":          {"id", "code", "title", "translated_title", "poster_url", "release_date", "duration_minutes", "subscription_status", "library_status", "created_at", "updated_at"},
		"movies":         {"id", "code", "title", "translated_title", "poster_url", "release_date", "duration_minutes", "subscription_status", "library_status", "created_at", "updated_at"},
		"subscriptions":  {"id", "media_id", "status", "mode", "filter_json", "idempotency_key", "idempotency_hash", "created_at", "updated_at", "version"},
		"download_tasks": {"id", "media_id", "status", "external_id", "error_message", "created_at", "updated_at"},
		"actors":         {"name", "photo", "limit_date", "created_at", "updated_at"},
	} {
		gotColumns := sqliteTableColumns(t, db, table)
		for _, want := range columns {
			if !gotColumns[want] {
				t.Fatalf("%s table missing column %q; columns=%v", table, want, gotColumns)
			}
		}
	}

	for table, indexes := range map[string][]string{
		"media":          {"idx_media_code", "idx_media_subscription_status", "idx_media_updated_at"},
		"movies":         {"idx_movies_code"},
		"subscriptions":  {"idx_subscriptions_idempotency_key", "idx_subscriptions_media_status", "idx_subscriptions_status_updated_at"},
		"download_tasks": {"idx_download_tasks_media_status", "idx_download_tasks_status_updated_at", "idx_download_tasks_external_id"},
		"actors":         {"idx_actors_limit_date"},
	} {
		gotIndexes := sqliteIndexes(t, db, table)
		for _, want := range indexes {
			if !gotIndexes[want] {
				t.Fatalf("%s table missing index %q; indexes=%v", table, want, gotIndexes)
			}
		}
	}
}

func TestPostgresMySQLIntegrationMigrationWhenConfigured(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name    string
		dialect Dialect
		dsnEnv  string
	}{
		{name: "postgres", dialect: DialectPostgres, dsnEnv: "BYTEMUSE_TEST_POSTGRES_DSN"},
		{name: "mysql", dialect: DialectMySQL, dsnEnv: "BYTEMUSE_TEST_MYSQL_DSN"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dsn := os.Getenv(tc.dsnEnv)
			if dsn == "" {
				t.Skipf("%s not set; skipping configurable %s integration migration", tc.dsnEnv, tc.name)
			}
			store, err := Open(ctx, Config{Dialect: tc.dialect, DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 1})
			if err != nil {
				t.Fatalf("open %s store: %v", tc.name, err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.Migrate(ctx); err != nil {
				t.Fatalf("migrate %s store: %v", tc.name, err)
			}
			if err := SeedDevelopment(ctx, store); err != nil {
				t.Fatalf("seed %s store: %v", tc.name, err)
			}
			page, err := store.Media().List(ctx, ports.MediaListQuery{Limit: 10})
			if err != nil {
				t.Fatalf("list seeded %s media: %v", tc.name, err)
			}
			if page.Total != 2 {
				t.Fatalf("seeded %s media total = %d, want 2", tc.name, page.Total)
			}
			if err := store.ReadinessProbe().Ready(ctx); err != nil {
				t.Fatalf("%s readiness: %v", tc.name, err)
			}
		})
	}
}

func assertSQLitePragma(t *testing.T, db *sql.DB, name string, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("query pragma %s: %v", name, err)
	}
	if strings.ToLower(got) != want {
		t.Fatalf("pragma %s = %q, want %q", name, got, want)
	}
}

func sqliteTableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	return columns
}

func sqliteIndexes(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		t.Fatalf("index_list(%s): %v", table, err)
	}
	defer rows.Close()
	indexes := make(map[string]bool)
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list(%s): %v", table, err)
		}
		indexes[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_list(%s): %v", table, err)
	}
	return indexes
}

func assertTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
