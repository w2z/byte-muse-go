package bootstrap

import (
	"context"
	"strings"
	"testing"

	"bytemuse/backend/internal/config"
)

func TestServeRejectsMissingAdministratorConfigBeforeOpeningDatabase(t *testing.T) {
	commands := NewCommands(config.Config{DatabaseDriver: "sqlite", DatabaseDSN: "invalid-parent/bytemuse.db"})
	err := commands.Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ADMIN_USERNAME") {
		t.Fatalf("serve error = %v, want ADMIN_USERNAME validation before database open", err)
	}
}
