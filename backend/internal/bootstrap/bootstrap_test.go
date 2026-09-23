package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"bytemuse/backend/internal/config"
	"bytemuse/backend/internal/ports"
)

func TestMigrationDoctorAndSeedLifecycle(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "bytemuse.db")
	cfg := config.Config{DatabaseDriver: "sqlite", DatabaseDSN: dsn}
	commands := NewCommands(cfg)
	ctx := context.Background()

	if err := commands.MigrationUp(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	status, err := commands.MigrationStatus(ctx)
	if err != nil || status != "current" {
		t.Fatalf("migration status = %q, %v; want current", status, err)
	}
	if err := commands.Doctor(ctx); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	store, err := commands.openStore(ctx)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	page, err := store.Media().List(ctx, ports.MediaListQuery{Limit: 10})
	_ = store.Close()
	count := page.Total
	if err != nil || count != 0 {
		t.Fatalf("default media count = %d, %v; want 0", count, err)
	}

	cfg.DemoSeedEnabled = true
	commands = NewCommands(cfg)
	if err := commands.Prepare(ctx); err != nil {
		t.Fatalf("prepare with seed: %v", err)
	}
	store, err = commands.openStore(ctx)
	if err != nil {
		t.Fatalf("open seeded store: %v", err)
	}
	page, err = store.Media().List(ctx, ports.MediaListQuery{Limit: 10})
	_ = store.Close()
	count = page.Total
	if err != nil || count != 2 {
		t.Fatalf("seeded media count = %d, %v; want 2", count, err)
	}
}
