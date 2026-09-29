package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"bytemuse/backend/internal/ports"
)

// TestPan115AccountMigrationCreatesTableAndSetting 验证空库初始化会建绑定表并登记空的离线保存目录。
// 迁移只新增空表与空配置，不写入任何账号数据；令牌列保存应用层密文，表本身不解释内容。
func TestPan115AccountMigrationCreatesTableAndSetting(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "pan115-init.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, e := NewPan115AccountRepository(store.SQLDB(), DialectSQLite).Load(ctx); e != nil || found {
		t.Fatalf("新库应为未绑定: found=%v err=%v", found, e)
	}
	var value string
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT setting_value FROM app_settings WHERE setting_key = ?", "PAN115_SAVE_PATH").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "" {
		t.Fatalf("离线保存目录默认值 = %q，应为空", value)
	}
}

// TestPan115AccountRepositoryRoundTrip 验证令牌密文原样存取、有效期可解析，且覆盖写入只保留一行。
func TestPan115AccountRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "pan115-repo.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repository := NewPan115AccountRepository(store.SQLDB(), DialectSQLite)
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	want := ports.Pan115Account{UserID: "12345", UserName: "115 用户", AccessToken: "cipher-access", RefreshToken: "cipher-refresh", AccessExpiresAt: expires}
	if err = repository.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.Load(ctx)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if got.UserID != want.UserID || got.UserName != want.UserName || got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || !got.AccessExpiresAt.Equal(expires) {
		t.Fatalf("got=%+v", got)
	}
	if err = repository.Save(ctx, ports.Pan115Account{UserID: "999", UserName: "另一个账号", AccessToken: "a", RefreshToken: "b", AccessExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM pan115_account").Scan(&count); err != nil || count != 1 {
		t.Fatalf("绑定行数=%d err=%v", count, err)
	}
	if err = repository.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err = repository.Load(ctx); err != nil || found {
		t.Fatalf("解绑后 found=%v err=%v", found, err)
	}
}

// TestPan115AccountMigrationUpgrade 验证从 25 版升级只新增绑定表与空配置，不改动历史数据，重复执行安全。
func TestPan115AccountMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "pan115-upgrade.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	for _, migration := range MigrationPlan(DialectSQLite) {
		if migration.Version < 26 {
			if err = applyMigration(ctx, store.SQLDB(), DialectSQLite, migration); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 升级前写入的历史配置不得被新迁移覆盖。
	if _, err = store.SQLDB().ExecContext(ctx, "UPDATE app_settings SET setting_value = ? WHERE setting_key = ?", "历史下载器", "PT_DEFAULT_DOWNLOADER"); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var value string
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT setting_value FROM app_settings WHERE setting_key = ?", "PT_DEFAULT_DOWNLOADER").Scan(&value); err != nil || value != "历史下载器" {
		t.Fatalf("历史配置被改写: %q err=%v", value, err)
	}
	var version int64
	if err = store.SQLDB().QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != 26 {
		t.Fatalf("最大迁移版本=%d err=%v", version, err)
	}
	if _, found, e := NewPan115AccountRepository(store.SQLDB(), DialectSQLite).Load(ctx); e != nil || found {
		t.Fatalf("升级后应为未绑定: found=%v err=%v", found, e)
	}
}
