// Package bootstrap is the composition root for database, HTTP, scheduler, and application services.
package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/config"
	"bytemuse/backend/internal/platform/database"
	runtimeapp "bytemuse/backend/internal/runtime"
	"bytemuse/backend/internal/scheduler"
	"bytemuse/backend/internal/transport/httpapi"
)

// Commands implements the operational actions exposed by the bytemuse CLI.
type Commands struct {
	config config.Config
}

// NewCommands binds validated process configuration to all operational commands.
func NewCommands(cfg config.Config) *Commands { return &Commands{config: cfg} }

// Prepare applies all migrations and optionally inserts exactly two development media records.
func (c *Commands) Prepare(ctx context.Context) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if c.config.DemoSeedEnabled {
		if err := database.SeedDevelopment(ctx, store); err != nil {
			return fmt.Errorf("seed development media: %w", err)
		}
	}
	return nil
}

// MigrationUp applies pending migrations without starting the service.
func (c *Commands) MigrationUp(ctx context.Context) error { return c.Prepare(ctx) }

// LegacyActors imports actor history from a read-only legacy SQLite database.
func (c *Commands) LegacyActors(ctx context.Context, legacyPath string) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	result, err := database.ImportLegacyActors(ctx, store, legacyPath)
	if err != nil {
		return err
	}
	fmt.Printf("导入演员: %d，跳过: %d\n", result.Imported, result.Skipped)
	return nil
}

// MigrationStatus reports current only when the latest known migration has been recorded.
func (c *Commands) MigrationStatus(ctx context.Context) (string, error) {
	store, err := c.openStore(ctx)
	if err != nil {
		return "", err
	}
	defer store.Close()
	var version sql.NullInt64
	if err := store.SQLDB().QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		if missingMigrationTable(err) {
			return "pending", nil
		}
		return "", fmt.Errorf("read migration status: %w", err)
	}
	plan := database.MigrationPlan(database.Dialect(c.config.DatabaseDriver))
	if len(plan) == 0 || !version.Valid || version.Int64 < plan[len(plan)-1].Version {
		return "pending", nil
	}
	return "current", nil
}

// Doctor checks database connectivity and the migrated schema for container health checks.
func (c *Commands) Doctor(ctx context.Context) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.ReadinessProbe().Ready(ctx); err != nil {
		return fmt.Errorf("database not ready: %w", err)
	}
	return nil
}

// Serve prepares persistent state, then runs HTTP, SPA, and the process scheduler together.
func (c *Commands) Serve(ctx context.Context) error {
	if err := c.config.ValidateServe(); err != nil {
		return err
	}
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if c.config.DemoSeedEnabled {
		if err := database.SeedDevelopment(ctx, store); err != nil {
			return fmt.Errorf("seed development media: %w", err)
		}
	}
	manager, err := scheduler.New(nil)
	if err != nil {
		return fmt.Errorf("create scheduler: %w", err)
	}
	startedAt := time.Now().UTC()
	authService, err := auth.New(auth.Config{Username: c.config.AdminUsername, Password: c.config.AdminPassword, Secret: c.config.SessionSecret, Secure: c.config.SessionCookieSecure()})
	if err != nil {
		return fmt.Errorf("create auth service: %w", err)
	}
	handler := httpapi.New(httpapi.Dependencies{
		Auth:          authService,
		Catalog:       application.NewCatalogService(store.Media()),
		Actors:        application.NewActorService(database.NewActorRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver))),
		Subscriptions: application.NewSubscriptionService(store.Subscriptions()),
		Downloads:     application.NewDownloadService(store.Downloads()),
		Dashboard:     application.NewDashboardService(store.Media(), store.Subscriptions(), store.Downloads(), healthyIntegrationCounter{}),
		System:        application.NewSystemService(c.config.Version, c.config.DatabaseDriver, c.config.DemoSeedEnabled, startedAt, manager.Running),
		Readiness:     store.ReadinessProbe(),
		StaticDir:     c.config.WebStaticDir,
	})
	server := &http.Server{Addr: c.config.HTTPAddress, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	return runtimeapp.New(server, manager, c.config.ShutdownTimeout).Run(ctx)
}

type healthyIntegrationCounter struct{}

func (healthyIntegrationCounter) CountHealthy(context.Context) (int, error) { return 0, nil }

func (c *Commands) openStore(ctx context.Context) (database.Store, error) {
	dialect := database.Dialect(c.config.DatabaseDriver)
	store, err := database.Open(ctx, database.Config{Dialect: dialect, DSN: c.config.DatabaseDSN, MaxOpenConns: 10, MaxIdleConns: 5})
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", c.config.DatabaseDriver, err)
	}
	return store, nil
}

func missingMigrationTable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return errors.Is(err, sql.ErrNoRows) || strings.Contains(message, "no such table") || strings.Contains(message, "does not exist") || strings.Contains(message, "doesn't exist")
}
