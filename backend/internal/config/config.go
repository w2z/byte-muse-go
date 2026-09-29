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
	// defaultStrmRoot 是容器内 strm 根目录的默认挂载点，与 README、Compose 的 /strm 一致。
	defaultStrmRoot = "/strm"
)

// Config contains non-secret process configuration.
type Config struct {
	HTTPAddress     string
	WebStaticDir    string
	// StrmRoot 是本地 strm 文件根目录；设置页只能浏览该目录以下的内容。
	StrmRoot        string
	ShutdownTimeout time.Duration
	Version         string
	// ReleaseRepo 是发布仓库的 owner/name，检查更新时读取其 version.json。
	ReleaseRepo     string
	AppEnvironment  string
	DatabaseDriver  string
	DatabaseDSN     string
	AdminUsername   string
	AdminPassword   string
	SessionSecret   string
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
	strmRoot := strings.TrimSpace(os.Getenv("STRM_ROOT"))
	if strmRoot == "" {
		strmRoot = defaultStrmRoot
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
		StrmRoot:        strmRoot,
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
