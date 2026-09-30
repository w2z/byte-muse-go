package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMigrationPlanVersionsAreUniqueAndOrdered 约束每个方言的迁移版本号唯一且严格递增。
// 迁移执行器按版本号判断是否已应用，重复版本号会被静默跳过并导致结构缺失，
// 因此新增迁移必须先取当前最大版本的下一个编号，本测试作为回归护栏。
func TestMigrationPlanVersionsAreUniqueAndOrdered(t *testing.T) {
	for _, dialect := range []Dialect{DialectSQLite, DialectPostgres, DialectMySQL} {
		plan := MigrationPlan(dialect)
		if len(plan) == 0 {
			t.Fatalf("%s: migration plan is empty", dialect)
		}
		seen := make(map[int64]string, len(plan))
		var previous int64
		for index, migration := range plan {
			if migration.Name == "" {
				t.Fatalf("%s: migration %d has empty name", dialect, migration.Version)
			}
			if existing, ok := seen[migration.Version]; ok {
				t.Fatalf("%s: version %d is reused by %q and %q", dialect, migration.Version, existing, migration.Name)
			}
			seen[migration.Version] = migration.Name
			if index > 0 && migration.Version <= previous {
				t.Fatalf("%s: version %d appears after %d, versions must strictly increase", dialect, migration.Version, previous)
			}
			previous = migration.Version
		}
	}
}

// TestDropUnusedJavdbHostSettingMigration 验证迁移 30 清理已废弃的 JAVDB_HOST 配置行：
// 迁移 7 会为所有库种下该键，旧库升级后该行必须消失，空库走完整迁移计划后也不得残留。
// 该键没有任何消费者，清理只删除这一行历史配置，不触碰其他设置键。
func TestDropUnusedJavdbHostSettingMigration(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "drop-javdb-host.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	// 先升级到迁移 30 之前，模拟旧版本实例。
	for _, migration := range MigrationPlan(DialectSQLite) {
		if migration.Version < 30 {
			if err = applyMigration(ctx, store.SQLDB(), DialectSQLite, migration); err != nil {
				t.Fatal(err)
			}
		}
	}
	countJavdbHost := func() int {
		var count int
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM app_settings WHERE setting_key = ?", "JAVDB_HOST").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := countJavdbHost(); count != 1 {
		t.Fatalf("升级前 JAVDB_HOST 行数 = %d，期望 1", count)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatalf("迁移重复执行应安全: %v", err)
	}
	if count := countJavdbHost(); count != 0 {
		t.Fatalf("升级后 JAVDB_HOST 行数 = %d，期望 0", count)
	}
}

// TestDropUnusedPhotoCacheSettingMigration 验证迁移 32 清理已废弃的 ENABLE_PHOTO_CACHE 配置行：
// 迁移 7 会为所有库种下该键，旧库升级后该行必须消失，空库走完整迁移计划后也不得残留。
// 该键没有任何消费者，清理只删除这一行历史配置，不触碰其他设置键。
func TestDropUnusedPhotoCacheSettingMigration(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "drop-photo-cache.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	// 先升级到迁移 32 之前，模拟旧版本实例。
	for _, migration := range MigrationPlan(DialectSQLite) {
		if migration.Version < 32 {
			if err = applyMigration(ctx, store.SQLDB(), DialectSQLite, migration); err != nil {
				t.Fatal(err)
			}
		}
	}
	countPhotoCache := func() int {
		var count int
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM app_settings WHERE setting_key = ?", "ENABLE_PHOTO_CACHE").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := countPhotoCache(); count != 1 {
		t.Fatalf("升级前 ENABLE_PHOTO_CACHE 行数 = %d，期望 1", count)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatalf("迁移重复执行应安全: %v", err)
	}
	if count := countPhotoCache(); count != 0 {
		t.Fatalf("升级后 ENABLE_PHOTO_CACHE 行数 = %d，期望 0", count)
	}
}

// TestStrmRootSettingMigration 验证迁移 33 只登记 STRM_ROOT 默认值：
// 空库走完整迁移计划后该键存在且为空串（表示沿用进程默认根目录），
// 旧库升级不覆盖已有配置，重复执行安全。
func TestStrmRootSettingMigration(t *testing.T) {
	ctx := context.Background()
	readRoot := func(store Store) string {
		t.Helper()
		var value string
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT setting_value FROM app_settings WHERE setting_key = ?", "STRM_ROOT").Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	fresh, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "strm-root-fresh.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if value := readRoot(fresh); value != "" {
		t.Fatalf("空库默认值 = %q，期望空串", value)
	}

	upgraded, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "strm-root-upgrade.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if err = ensureMigrationTable(ctx, upgraded.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	// 先升级到迁移 33 之前，模拟旧版本实例。
	for _, migration := range MigrationPlan(DialectSQLite) {
		if migration.Version < 33 {
			if err = applyMigration(ctx, upgraded.SQLDB(), DialectSQLite, migration); err != nil {
				t.Fatal(err)
			}
		}
	}
	statement := "INSERT INTO app_settings (setting_key, setting_value, is_secret, updated_at) VALUES (" +
		sqlLiteral("STRM_ROOT") + ", " + sqlLiteral("/media/strm") + ", FALSE, " + currentTimestampExpression(DialectSQLite) + ")"
	if _, err = upgraded.SQLDB().ExecContext(ctx, statement); err != nil {
		t.Fatal(err)
	}
	if err = upgraded.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = upgraded.Migrate(ctx); err != nil {
		t.Fatalf("迁移重复执行应安全: %v", err)
	}
	if value := readRoot(upgraded); value != "/media/strm" {
		t.Fatalf("升级不得覆盖已有配置，得到 %q", value)
	}
}

// TestSubscriptionScanMigration 验证迁移 34 把资源搜索从下载任务里独立出来：
// 建表 subscription_scans，并清理两类历史噪音——从未提交下载器的搜索队列项与没有 info_hash 的失败任务。
// 真正提交过下载器（有 info_hash）的任务必须保留，空库初始化与重复执行都安全。
func TestSubscriptionScanMigration(t *testing.T) {
	ctx := context.Background()

	fresh, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "scan-fresh.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var table int
	if err = fresh.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='subscription_scans'").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != 1 {
		t.Fatal("空库迁移后缺少 subscription_scans 表")
	}

	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "scan-upgrade.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	// 先升级到迁移 34 之前，模拟旧版本实例。
	for _, migration := range MigrationPlan(DialectSQLite) {
		if migration.Version < 34 {
			if err = applyMigration(ctx, store.SQLDB(), DialectSQLite, migration); err != nil {
				t.Fatal(err)
			}
		}
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "TEST-1", "film", "active", "absent", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	insert := func(id, status, hash string) {
		t.Helper()
		var value any
		if hash != "" {
			value = hash
		}
		if _, err := store.SQLDB().ExecContext(ctx, "INSERT INTO download_tasks (id,media_id,status,info_hash,created_at,updated_at) VALUES (?,?,?,?,?,?)", id, "m1", status, value, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	insert("noise-queued", "queued", "")
	insert("noise-searching", "searching", "")
	insert("noise-failed", "failed", "")
	insert("keep-failed", "failed", strings.Repeat("a", 40))
	insert("keep-submitted", "submitted", strings.Repeat("b", 40))
	countTasks := func() int {
		var count int
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM download_tasks").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := countTasks(); count != 5 {
		t.Fatalf("升级前任务行数 = %d，期望 5", count)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatalf("迁移重复执行应安全: %v", err)
	}
	if count := countTasks(); count != 2 {
		t.Fatalf("升级后任务行数 = %d，期望 2（只保留有 info_hash 的任务）", count)
	}
	for _, id := range []string{"keep-failed", "keep-submitted"} {
		var found int
		if err = store.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM download_tasks WHERE id = ?", id).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found != 1 {
			t.Fatalf("有 info_hash 的任务 %s 被误删", id)
		}
	}
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO subscription_scans (id,subscription_id,origin,status,created_at,updated_at) VALUES (?,?,?,?,?,?)", "scan-1", "sub1", "user", "queued", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO subscription_scans (id,subscription_id,origin,status,created_at,updated_at) VALUES (?,?,?,?,?,?)", "scan-2", "sub1", "schedule", "queued", stamp, stamp); err == nil {
		t.Fatal("同一订阅重复登记搜索应被唯一索引拒绝")
	}
}
