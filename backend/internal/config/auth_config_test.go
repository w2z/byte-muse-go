package config_test

import (
	"strings"
	"testing"

	"bytemuse/backend/internal/config"
)

func TestServeValidationRequiresAdministratorConfiguration(t *testing.T) {
	cfg := config.Config{}
	for _, variable := range []string{"ADMIN_USERNAME", "ADMIN_PASSWORD", "SESSION_SECRET"} {
		err := cfg.ValidateServe()
		if err == nil || !strings.Contains(err.Error(), variable) {
			t.Fatalf("validation error = %v, want missing %s", err, variable)
		}
		switch variable {
		case "ADMIN_USERNAME":
			cfg.AdminUsername = "operator"
		case "ADMIN_PASSWORD":
			cfg.AdminPassword = "correct-password"
		case "SESSION_SECRET":
			cfg.SessionSecret = "0123456789abcdef0123456789abcdef"
		}
	}
	if err := cfg.ValidateServe(); err != nil {
		t.Fatalf("valid serve config: %v", err)
	}
}

func TestLoadDoesNotRequireAdministratorConfigurationForMaintenanceCommands(t *testing.T) {
	t.Setenv("ADMIN_USERNAME", "")
	t.Setenv("ADMIN_PASSWORD", "")
	t.Setenv("SESSION_SECRET", "")
	if _, err := config.Load(); err != nil {
		t.Fatalf("load maintenance config without administrator settings: %v", err)
	}
}
