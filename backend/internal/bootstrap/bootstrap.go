// Package bootstrap is the composition root for database, HTTP, scheduler, and application services.
package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/config"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/platform/downloadclient"
	"bytemuse/backend/internal/platform/torrentsearch"
	"bytemuse/backend/internal/ports"
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

// Prepare applies all pending database migrations.
func (c *Commands) Prepare(ctx context.Context) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

// MigrationUp applies pending migrations without starting the service.
func (c *Commands) MigrationUp(ctx context.Context) error { return c.Prepare(ctx) }

// LegacyCatalog imports catalog metadata and subscription intent from a read-only legacy SQLite database.
func (c *Commands) LegacyCatalog(ctx context.Context, legacyPath string) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	result, err := database.ImportLegacyCatalog(ctx, store, database.Dialect(c.config.DatabaseDriver), legacyPath)
	if err != nil {
		return err
	}
	fmt.Printf("导入影片: %d，跳过: %d，活动订阅: %d，已取消订阅: %d\n", result.Imported, result.Skipped, result.ActiveSubscriptions, result.CanceledSubscriptions)
	return nil
}

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

// LegacyRanks imports the latest ordered snapshot for every legacy rank type.
func (c *Commands) LegacyRanks(ctx context.Context, legacyPath string) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	result, err := database.ImportLegacyRanks(ctx, store, legacyPath)
	if err != nil {
		return err
	}
	fmt.Printf("导入榜单类型: %d，榜单条目: %d\n", result.RankTypes, result.Entries)
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
	// 迁移完成后立即绑定数据库，确保后续启动、调度和业务日志都可跨重启查询。
	logging.Default.SetStore(database.NewLogRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)))
	authService, err := auth.New(auth.Config{Username: c.config.AdminUsername, Password: c.config.AdminPassword, Secret: c.config.SessionSecret, Secure: c.config.SessionCookieSecure()})
	if err != nil {
		return fmt.Errorf("create auth service: %w", err)
	}
	settingsService, err := application.NewSettingsService(database.NewSettingsRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), c.config.DatabaseDriver, c.config.SessionSecret)
	if err != nil {
		return fmt.Errorf("create settings service: %w", err)
	}
	translationSettings, err := settingsService.Get(ctx)
	if err != nil {
		return fmt.Errorf("read translation settings: %w", err)
	}
	jobs := configuredJobs(translationSettings.Values)
	collectionClient, err := collector.NewClientWithProxy(translationSettings.Values["PROXY"])
	if err != nil {
		return fmt.Errorf("采集代理配置无效")
	}
	collectionService := application.NewQueuedCollectionService(collector.NewRegistry(collectionClient), database.NewCollectionRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), nil)
	downloadRepository := database.NewSubscriptionDownloadRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver))
	downloadService := application.NewSubscriptionDownloadService(downloadRepository, nil, nil, func(ctx context.Context) (map[string]string, error) {
		settings, e := settingsService.Get(ctx)
		return settings.Values, e
	})
	downloadService.SetRuntimeFactory(func(values map[string]string) (application.ResourceSearcher, application.PrivateTorrentSource, map[string]application.MagnetDownloader) {
		sources := []application.ResourceSearcher{torrentsearch.NewNyaaSearcher(nil, "")}
		privateSearchers, privateSources := configuredPrivateSites(values, nil)
		sources = append(sources, privateSearchers...)
		var searcher application.ResourceSearcher = application.MultiResourceSearcher{Sources: sources}
		var private application.PrivateTorrentSource
		if len(privateSources) > 0 {
			private = application.PrivateTorrentSources{Sources: privateSources}
		}
		return searcher, private, map[string]application.MagnetDownloader{
			"qbittorrent": downloadclient.NewQbittorrent(values["QBITTORRENT_URL"], values["QBITTORRENT_USERNAME"], values["QBITTORRENT_PASSWORD"], values["QBITTORRENT_DOWNLOAD_PATH"], values["QBITTORRENT_CATEGORY"], nil),
		}
	})
	for i := range jobs {
		if jobs[i].Name == "同步榜单" {
			jobs[i].Run = collectionRankJob(collectionService)
		}
		if jobs[i].Name == "订阅下载" {
			jobs[i].Run = func(jobCtx context.Context) scheduler.JobResult {
				count, e := downloadService.RunActive(jobCtx)
				if e != nil {
					logging.Error(logging.CategoryDownload, "订阅下载执行失败", "error", e.Error())
				}
				return scheduler.JobResult{"queued": count}
			}
		}
	}
	// 日志清理按服务所在时区每天零点执行，保留天数仍在执行时读取。
	jobs = append(jobs, scheduler.Job{Name: "清理系统日志", Spec: "0 0 * * *", Run: func(jobCtx context.Context) scheduler.JobResult {
		settings, err := settingsService.Get(jobCtx)
		if err != nil {
			logging.Error(logging.CategorySystem, "读取日志保留设置失败", "error", err.Error())
			return nil
		}
		raw := strings.TrimSpace(settings.Values["LOG_RETENTION_DAYS"])
		if raw == "" || raw == "0" {
			return nil
		}
		days, err := strconv.Atoi(raw)
		if err != nil || days < 0 {
			logging.Error(logging.CategorySystem, "日志保留天数配置无效", "value", raw)
			return nil
		}
		if days == 0 {
			return nil
		}
		deleted, err := logging.Default.DeleteBefore(jobCtx, time.Now().UTC().Add(-time.Duration(days)*24*time.Hour))
		if err != nil {
			logging.Error(logging.CategorySystem, "定时清理日志失败", "error", err.Error())
			return nil
		}
		return scheduler.JobResult{"deleted": deleted, "retention_days": days}
	}})
	manager, err := scheduler.New(jobs)
	if err != nil {
		return fmt.Errorf("create scheduler: %w", err)
	}
	translationService, err := application.NewTranslationService(application.TranslationConfig{
		Engine:    application.TranslationEngine(translationSettings.Values["TRANSLATION_ENGINE"]),
		OpenAIURL: translationSettings.Values["OPENAI_URL"], OpenAIModel: translationSettings.Values["OPENAI_MODEL"], OpenAIAPIKey: translationSettings.Values["OPENAI_API_KEY"],
		TranslationPrompt: translationSettings.Values["TRANSLATION_PROMPT"],
		GoogleAPIKey:      translationSettings.Values["GOOGLE_API_KEY"], BaiduAppID: translationSettings.Values["BAIDU_APP_ID"], BaiduAPIKey: translationSettings.Values["BAIDU_API_KEY"], DeepLXURL: translationSettings.Values["DEEPLX_URL"],
	}, nil)
	if err != nil {
		return fmt.Errorf("create translation service: %w", err)
	}
	mediaRepository := store.Media()
	if translationSettings.Values["TRANSLATION_ENGINE"] != "" && translationSettings.Values["TRANSLATION_ENGINE"] != "none" {
		collectionService.SetTranslator(translationService)
	}
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	workersDone := make(chan struct{})
	go func() { defer close(workersDone); collectionService.Work(workerCtx) }()
	defer func() { cancelWorkers(); <-workersDone }()
	downloadWorkerDone := make(chan struct{})
	syncTransfers := func(syncCtx context.Context) {
		current, err := settingsService.Get(syncCtx)
		if err != nil {
			logging.Error(logging.CategoryDownload, "读取下载器设置失败", "error", err.Error())
			return
		}
		values := current.Values
		if values["QBITTORRENT_URL"] == "" || values["QBITTORRENT_USERNAME"] == "" || values["QBITTORRENT_PASSWORD"] == "" {
			return
		}
		client := downloadclient.NewQbittorrent(values["QBITTORRENT_URL"], values["QBITTORRENT_USERNAME"], values["QBITTORRENT_PASSWORD"], values["QBITTORRENT_DOWNLOAD_PATH"], values["QBITTORRENT_CATEGORY"], nil)
		states, err := client.ListTransferStates(syncCtx)
		if err != nil {
			logging.Error(logging.CategoryDownload, "qBittorrent 状态同步失败", "error", err.Error())
			return
		}
		updates := make([]ports.TransferState, 0, len(states))
		for _, state := range states {
			updates = append(updates, ports.TransferState{Hash: state.Hash, Status: state.Status, AddedAt: state.AddedAt, CompletedAt: state.CompletedAt})
		}
		if err := downloadRepository.SaveTransferStates(syncCtx, updates); err != nil {
			logging.Error(logging.CategoryDownload, "下载状态保存失败", "error", err.Error())
		}
	}
	go func() {
		defer close(downloadWorkerDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		lastSync := time.Time{}
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if e := downloadService.Process(workerCtx, 10); e != nil {
					logging.Error(logging.CategoryDownload, "订阅下载任务处理失败", "error", e.Error())
				}
				if time.Since(lastSync) >= time.Minute {
					syncTransfers(workerCtx)
					lastSync = time.Now()
				}
			}
		}
	}()
	defer func() { cancelWorkers(); <-downloadWorkerDone }()
	handler := httpapi.New(httpapi.Dependencies{
		Collection:            collectionService,
		Auth:                  authService,
		Catalog:               application.NewCatalogService(mediaRepository),
		CatalogQueries:        application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver))),
		Actors:                application.NewActorService(database.NewActorRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver))),
		Tags: application.NewTagService(database.NewTagRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver))),
		Subscriptions:         application.NewSubscriptionService(store.Subscriptions()),
		SubscriptionDownloads: downloadService,
		Downloads:             application.NewDownloadService(store.Downloads()),
		Dashboard:             application.NewDashboardService(store.Media(), store.Subscriptions(), store.Downloads()),
		Settings:              settingsService,
		Scheduler:             manager,
		Logs:                  logging.Default,
		Readiness:             store.ReadinessProbe(),
		StaticDir:             c.config.WebStaticDir,
	})
	server := &http.Server{Addr: c.config.HTTPAddress, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	return runtimeapp.New(server, manager, c.config.ShutdownTimeout).Run(ctx)
}

// configuredJobs registers every configured cron entry. Domain executors are added as their
// integrations land; until then a trigger is still observable and never silently discarded.
func configuredJobs(values map[string]string) []scheduler.Job {
	definitions := []struct{ name, key string }{
		{"同步榜单", "RANK_SCHEDULE_TIME"},
		{"同步热门演员", "ACTOR_SCHEDULE_TIME"},
		{"标签追新", "TAG_SCHEDULE_TIME"},
		{"订阅下载", "DOWNLOAD_SCHEDULE_TIME"},
	}
	jobs := make([]scheduler.Job, 0, len(definitions))
	for _, definition := range definitions {
		spec := strings.TrimSpace(values[definition.key])
		if spec == "" {
			continue
		}
		name := definition.name
		jobs = append(jobs, scheduler.Job{Name: name, Spec: spec, Run: func(context.Context) scheduler.JobResult {
			logging.Info(logging.CategoryOther, "定时任务已触发，业务执行器尚未接入", "task", name)
			return nil
		}})
	}
	return jobs
}

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
