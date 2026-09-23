package config_test

import (
	"testing"

	"bytemuse/backend/internal/config"
)

func TestLoadUsesDeploymentDefaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("HTTP_ADDRESS", "")
	t.Setenv("WEB_STATIC_DIR", "")
	t.Setenv("DATABASE_DRIVER", "")
	t.Setenv("DATABASE_DSN", "")
	t.Setenv("DEMO_SEED_ENABLED", "")
	got, err := config.Load()
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if got.HTTPAddress != ":3750" {
		t.Fatalf("default HTTP address = %q, want :3750", got.HTTPAddress)
	}
	if got.WebStaticDir != "/app/web" {
		t.Fatalf("default static dir = %q, want /app/web", got.WebStaticDir)
	}
	if got.DatabaseDriver != "sqlite" || got.DatabaseDSN == "" || got.DemoSeedEnabled {
		t.Fatalf("database defaults = driver=%q dsn=%q seed=%t", got.DatabaseDriver, got.DatabaseDSN, got.DemoSeedEnabled)
	}
}

func TestLoadPrefersHTTPAddrOverLegacyHTTPAddress(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":4100")
	t.Setenv("HTTP_ADDRESS", ":4200")
	got, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.HTTPAddress != ":4100" {
		t.Fatalf("HTTP address = %q, want HTTP_ADDR value", got.HTTPAddress)
	}
}

func TestLoadSupportsStaticDirectoryAndSeedOverrides(t *testing.T) {
	t.Setenv("WEB_STATIC_DIR", `C:\bytemuse\web`)
	t.Setenv("DEMO_SEED_ENABLED", "true")
	got, err := config.Load()
	if err != nil {
		t.Fatalf("load override: %v", err)
	}
	if got.WebStaticDir != `C:\bytemuse\web` || !got.DemoSeedEnabled {
		t.Fatalf("override = static=%q seed=%t", got.WebStaticDir, got.DemoSeedEnabled)
	}
}
