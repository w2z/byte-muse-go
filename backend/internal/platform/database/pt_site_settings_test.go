package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPTSiteSettingsMigrationPreservesLegacyCookie(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, DSN: filepath.Join(t.TempDir(), "settings.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	for _, migration := range MigrationPlan(DialectSQLite) {
		if migration.Version >= 18 {
			continue
		}
		if err := applyMigration(ctx, store.SQLDB(), DialectSQLite, migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SQLDB().ExecContext(ctx, "UPDATE app_settings SET setting_value='Rousi' WHERE setting_key='MAIN_SITE'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQLDB().ExecContext(ctx, "UPDATE app_settings SET setting_value='legacy-ciphertext' WHERE setting_key='ROUSI_COOKIE'"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
	for key, want := range map[string]string{"MAIN_SITE": "ALL", "ROUSI_COOKIE": "legacy-ciphertext", "PTFANS_COOKIE": "", "ROUSIPRO_COOKIE": ""} {
		var got string
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT setting_value FROM app_settings WHERE setting_key=?", key).Scan(&got); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got != want {
			t.Fatalf("%s=%q, want %q", key, got, want)
		}
	}
}

func TestSiteAuthSettingsMigrationPreservesCredentialsAndDefaultsToKey(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, DSN: filepath.Join(t.TempDir(), "auth.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := ensureMigrationTable(ctx, store.SQLDB(), DialectSQLite); err != nil {
		t.Fatal(err)
	}
	for _, migration := range MigrationPlan(DialectSQLite) {
		if migration.Version >= 19 {
			continue
		}
		if err := applyMigration(ctx, store.SQLDB(), DialectSQLite, migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SQLDB().ExecContext(ctx, "UPDATE app_settings SET setting_value='legacy-encrypted-cookie' WHERE setting_key='PTFANS_COOKIE'"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"PTT", "PTFANS", "ROUSIPRO", "NICEPT"} {
		var value string
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT setting_value FROM app_settings WHERE setting_key=?", prefix+"_AUTH_TYPE").Scan(&value); err != nil || value != "key" {
			t.Fatalf("%s must default to key: %v", prefix, err)
		}
	}
	for _, key := range []string{"PTT_PASSKEY", "PTFANS_API_KEY", "ROUSIPRO_API_KEY", "NICEPT_API_KEY"} {
		var value string
		var secret bool
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT setting_value, is_secret FROM app_settings WHERE setting_key=?", key).Scan(&value, &secret); err != nil || value != "" || !secret {
			t.Fatalf("%s must be an empty secret: %v", key, err)
		}
	}
	if _, err := store.SQLDB().ExecContext(ctx, "UPDATE app_settings SET setting_value='cookie' WHERE setting_key='PTFANS_AUTH_TYPE'"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"PTFANS_COOKIE": "legacy-encrypted-cookie", "PTFANS_AUTH_TYPE": "cookie", "PTT_UID": ""} {
		var got string
		if err := store.SQLDB().QueryRowContext(ctx, "SELECT setting_value FROM app_settings WHERE setting_key=?", key).Scan(&got); err != nil || got != want {
			t.Fatalf("%s changed on migration: %v", key, err)
		}
	}
}
