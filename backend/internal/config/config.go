// Package config loads validated runtime settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultWebStaticDir = "/app/web"
	// defaultCoverRoot 是容器内影片封面缓存目录，位于用户自行挂载的 /data 之下。
	defaultCoverRoot = "/data/cover"
)

// Config contains non-secret process configuration.
type Config struct {
	HTTPAddress  string
	WebStaticDir string
	// CoverRoot 是影片封面缓存目录；封面缓存固定开启，目录不可写时图片回退源站地址。
	CoverRoot       string
	ShutdownTimeout time.Duration
	Version         string
	// ReleaseRepo 是发布仓库的 owner/name，检查更新时读取其 version.json。
	ReleaseRepo    string
	AppEnvironment string
	DatabaseDriver string
	DatabaseDSN    string
	AdminUsername  string
	AdminPassword  string
	SessionSecret  string
}

// Load reads environment variables and applies production-safe defaults.
func Load() (Config, error) {
	shutdownTimeout := 10 * time.Second
	if raw := strings.TrimSpace(os.Getenv("SHUTDOWN_TIMEOUT_SECONDS")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds < 1 {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT_SECONDS must be a positive integer")
		}
		shutdownTimeout = time.Duration(seconds) * time.Second
	}
	staticDir := strings.TrimSpace(os.Getenv("WEB_STATIC_DIR"))
	if staticDir == "" {
		staticDir = defaultWebStaticDir
	}
	coverRoot := strings.TrimSpace(os.Getenv("COVER_ROOT"))
	if coverRoot == "" {
		coverRoot = defaultCoverRoot
	}
	driver := envOrDefault("DATABASE_DRIVER", "sqlite")
	if driver != "sqlite" && driver != "postgres" && driver != "mysql" {
		return Config{}, fmt.Errorf("unsupported DATABASE_DRIVER %q", driver)
	}
	httpAddress := envOrDefault("HTTP_ADDR", "")
	if httpAddress == "" {
		httpAddress = envOrDefault("HTTP_ADDRESS", ":3750")
	}
	databaseDSN := strings.TrimSpace(os.Getenv("DATABASE_DSN"))
	if databaseDSN == "" && driver == "sqlite" {
		databaseDSN = "file:/data/bytemuse.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	if databaseDSN == "" {
		return Config{}, fmt.Errorf("DATABASE_DSN is required for %s", driver)
	}
	return Config{
		HTTPAddress:     httpAddress,
		WebStaticDir:    staticDir,
		CoverRoot:       coverRoot,
		ShutdownTimeout: shutdownTimeout,
		Version:         envOrDefault("BYTEMUSE_VERSION", "dev"),
		ReleaseRepo:     envOrDefault("BYTEMUSE_RELEASE_REPO", "w2z/byte-muse-go"),
		AppEnvironment:  envOrDefault("APP_ENV", "development"),
		DatabaseDriver:  driver,
		DatabaseDSN:     databaseDSN,
		AdminUsername:   strings.TrimSpace(os.Getenv("ADMIN_USERNAME")),
		AdminPassword:   os.Getenv("ADMIN_PASSWORD"),
		SessionSecret:   os.Getenv("SESSION_SECRET"),
	}, nil
}

// ValidateServe rejects missing administrator settings before serve performs external work.
func (c Config) ValidateServe() error {
	if strings.TrimSpace(c.AdminUsername) == "" {
		return fmt.Errorf("ADMIN_USERNAME is required for serve")
	}
	if c.AdminPassword == "" {
		return fmt.Errorf("ADMIN_PASSWORD is required for serve")
	}
	if len(c.SessionSecret) < 32 {
		return fmt.Errorf("SESSION_SECRET must contain at least 32 bytes for serve")
	}
	return nil
}

// SessionCookieSecure reports whether browser sessions require HTTPS transport.
func (c Config) SessionCookieSecure() bool {
	return strings.EqualFold(strings.TrimSpace(c.AppEnvironment), "production")
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
