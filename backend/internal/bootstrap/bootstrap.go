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
	"sync"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/application/agent"
	"bytemuse/backend/internal/auth"
	"bytemuse/backend/internal/config"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/platform/covercache"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/platform/downloadclient"
	"bytemuse/backend/internal/platform/release"
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

// VideoTypes backfills media.video_type from evidence already stored in the database.
func (c *Commands) VideoTypes(ctx context.Context) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	result, err := database.BackfillVideoTypes(ctx, store, database.Dialect(c.config.DatabaseDriver))
	if err != nil {
		return err
	}
	fmt.Printf("扫描未分类影片: %d，写入分类: %d，仍无法判定: %d\n", result.Scanned, result.Classified, result.Unclassified)
	return nil
}

// TranslationCleanup clears persisted titles that fail the shared translation validation rule.
func (c *Commands) TranslationCleanup(ctx context.Context) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	result, err := database.ResetInvalidTranslations(ctx, store, database.Dialect(c.config.DatabaseDriver), func(title, translated string) bool {
		_, ok := application.SanitizeTranslatedTitle(title, translated)
		return ok
	})
	if err != nil {
		return err
	}
	fmt.Printf("扫描已有译文: %d，清除无效译文: %d\n", result.Scanned, result.Reset)
	return nil
}

// translationConfigFromSettings 由设置快照构造翻译配置。
// 翻译模型与对话 Agent 各自持有独立的 OpenAI 兼容配置，两者不共享接口、模型或密钥。
func translationConfigFromSettings(values map[string]string) application.TranslationConfig {
	return application.TranslationConfig{
		Engine:            application.TranslationEngine(values["TRANSLATION_ENGINE"]),
		OpenAIURL:         values["TRANSLATION_OPENAI_URL"],
		OpenAIModel:       values["TRANSLATION_OPENAI_MODEL"],
		OpenAIAPIKey:      values["TRANSLATION_OPENAI_API_KEY"],
		TranslationPrompt: values["TRANSLATION_PROMPT"],
		GoogleAPIKey:      values["GOOGLE_API_KEY"],
		BaiduAppID:        values["BAIDU_APP_ID"],
		BaiduAPIKey:       values["BAIDU_API_KEY"],
		DeepLXURL:         values["DEEPLX_URL"],
	}
}

// TranslationFill translates titles that still have no translation using the configured engine.
func (c *Commands) TranslationFill(ctx context.Context, limit int) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	settingsService, err := application.NewSettingsService(database.NewSettingsRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), c.config.DatabaseDriver, c.config.SessionSecret)
	if err != nil {
		return fmt.Errorf("create settings service: %w", err)
	}
	values, err := settingsService.Get(ctx)
	if err != nil {
		return fmt.Errorf("read translation settings: %w", err)
	}
	service, err := application.NewTranslationService(translationConfigFromSettings(values.Values), nil)
	if err != nil {
		return fmt.Errorf("create translation service: %w", err)
	}
	result, err := database.BackfillTranslations(ctx, store, database.Dialect(c.config.DatabaseDriver), limit, func(jobCtx context.Context, title string) (string, error) {
		value, e := service.Translate(jobCtx, application.TranslationRequest{Text: title, TargetLanguage: "ZH-CN"})
		if e != nil {
			return "", e
		}
		sanitized, ok := application.SanitizeTranslatedTitle(title, value)
		if !ok {
			return "", errors.New("translation_invalid")
		}
		return sanitized, nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("尝试翻译: %d，成功: %d，失败: %d\n", result.Attempted, result.Translated, result.Failed)
	return nil
}

// TagNormalize 把库内已有标签统一为权威简体中文名并去重，供升级后一次性收敛历史标签。
// 这里用 Normalize 而不是 Resolve：对标站字典本身是繁体，只有把字典里的既有名称也翻译一遍，
// 繁体、日文与英文写法才能收敛到同一个简体权威名。翻译引擎不可用时直接失败，不静默跳过。
func (c *Commands) TagNormalize(ctx context.Context) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	settingsService, err := application.NewSettingsService(database.NewSettingsRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), c.config.DatabaseDriver, c.config.SessionSecret)
	if err != nil {
		return fmt.Errorf("create settings service: %w", err)
	}
	values, err := settingsService.Get(ctx)
	if err != nil {
		return fmt.Errorf("read translation settings: %w", err)
	}
	translator, err := application.NewTranslationService(translationConfigFromSettings(values.Values), nil)
	if err != nil {
		return fmt.Errorf("create translation service: %w", err)
	}
	tagNames := application.NewTagNameService(database.NewTagNameRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), translator)
	if err := tagNames.Probe(ctx); err != nil {
		return fmt.Errorf("翻译引擎不可用，未改写任何标签: %w", err)
	}
	result, err := database.NormalizeStoredTags(ctx, store, database.Dialect(c.config.DatabaseDriver), tagNames.Normalize)
	if err != nil {
		return err
	}
	fmt.Printf("扫描标签名: %d，字典重命名/合并: %d，字典补齐: %d，影片标签行: %d，追新规则: %d，追新台账: %d\n", result.Names, result.CatalogMerged, result.CatalogAdded, result.GenreRows, result.Rules, result.Matches)
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

// coverProxyTTL 是封面缓存重新读取代理设置的间隔：保存后 30 秒内生效，避免每张封面都读一次全量设置。
const coverProxyTTL = 30 * time.Second

// coverProxyReader 返回「读取当前爬虫代理」的函数，封面下载在请求时取值，改代理不需要重启进程。
// 设置读取失败时沿用上一次成功的值，配置读取故障不阻断封面。
func coverProxyReader(settings *application.SettingsService) func(context.Context) string {
	var mutex sync.Mutex
	proxy, expires := "", time.Time{}
	return func(ctx context.Context) string {
		mutex.Lock()
		defer mutex.Unlock()
		if now := time.Now(); now.Before(expires) {
			return proxy
		}
		current, err := settings.Get(ctx)
		if err != nil {
			return proxy
		}
		proxy, expires = current.Values["PROXY"], time.Now().Add(coverProxyTTL)
		return proxy
	}
}

// settingsValues 把设置服务适配成执行时读取配置的函数；调用方每次读取最新值，不缓存凭据。
func settingsValues(settings *application.SettingsService) func(context.Context) (map[string]string, error) {
	return func(ctx context.Context) (map[string]string, error) {
		current, err := settings.Get(ctx)
		if err != nil {
			return nil, err
		}
		return current.Values, nil
	}
}

// notifyTransferTransitions 把下载器报告的终态跃迁转成下载完成/失败通知。
// 只有 transfer_status 真正变化的任务才会出现在 transitions 中，因此同一次跃迁只通知一次。
func notifyTransferTransitions(ctx context.Context, notifier application.Notifier, transitions []ports.TransferTransition) {
	if notifier == nil {
		return
	}
	for _, item := range transitions {
		switch item.Status {
		case "completed":
			notifier.Notify(ctx, application.NotificationDownloadComplete, application.NotificationMessage{
				Title:    application.NotificationHeadline(item.Code, "已完成下载"),
				Text:     item.Title,
				CoverURL: item.Cover,
				Code:     item.Code,
			})
		case "failed":
			notifier.Notify(ctx, application.NotificationDownloadFailed, application.NotificationMessage{
				Title:    application.NotificationHeadline(item.Code, "下载失败"),
				Text:     "原因：下载器报告任务失败",
				CoverURL: item.Cover,
				Code:     item.Code,
			})
		}
	}
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
	// 115 网盘服务：扫码登录、账号绑定、目录浏览与离线下载受理；
	// 设置页与订阅下载链路复用同一实例，令牌刷新只有一处实现。
	pan115Service, err := application.NewPan115Service(database.NewPan115AccountRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), settingsValues(settingsService), nil, c.config.SessionSecret)
	if err != nil {
		return fmt.Errorf("create 115 service: %w", err)
	}
	defer pan115Service.Close()
	// strm 服务：把 115 与 CloudDrive2 的网盘目录镜像成本地 strm 文件，并解析播放地址。
	// CloudDrive2 连接参数来自设置，因此传入按设置懒构造的适配器，改配置后无需重启进程。
	// 本地目录浏览与生成统一使用系统固定的 /strm 根目录。
	strmService, err := application.NewStrmService(pan115Service,
		application.NewCloudDriveSettings(settingsValues(settingsService)), settingsValues(settingsService))
	if err != nil {
		return fmt.Errorf("create strm service: %w", err)
	}
	// 115 扫描入库服务：把设置页的扫描目录递归扫描后登记到媒体库。
	// 与生成 strm 共用同一套 115 递归与失败隔离规则，媒体库登记只写 library_status。
	pan115LibraryService, err := application.NewPan115LibraryService(pan115Service, store.MediaLibrary(), settingsValues(settingsService))
	if err != nil {
		return fmt.Errorf("create 115 library service: %w", err)
	}
	// 对话回复与业务通知共用同一个渠道解析器，不另建第二套发送逻辑。
	channels := newChannelRegistry(settingsService)
	notifier := application.NewNotificationService(settingsValues(settingsService), channels)
	translationSettings, err := settingsService.Get(ctx)
	if err != nil {
		return fmt.Errorf("read translation settings: %w", err)
	}
	jobs := configuredJobs()
	tagService := application.NewTagService(database.NewTagRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)))
	actorService := application.NewActorService(database.NewActorRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)))
	actorService.SetSettingsLoader(func(loadCtx context.Context) (map[string]string, error) {
		current, e := settingsService.Get(loadCtx)
		return current.Values, e
	})
	collectionClient, err := collector.NewClientWithProxy(translationSettings.Values["PROXY"])
	if err != nil {
		return fmt.Errorf("采集代理配置无效")
	}
	collectionClient.ConfigureBypass(translationSettings.Values["BYPASS_ENGINE"], translationSettings.Values["BYPASS_URL"], translationSettings.Values["BYPASS_USE_PROXY"] == "true")
	collectionRepository := database.NewCollectionRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver))
	collectionService := application.NewQueuedCollectionService(collector.NewRegistry(collectionClient), collectionRepository, nil)
	actorCatalog := application.NewActorCatalogService(database.NewActorCatalogRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)),
		func(loadCtx context.Context) ([]ports.ActorProfile, error) {
			return collector.HotActors(loadCtx, collectionClient)
		},
		func(loadCtx context.Context) ([]ports.ActorProfile, error) {
			return collector.GfriendsActors(loadCtx, collectionClient)
		})
	downloadRepository := database.NewSubscriptionDownloadRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver))
	downloadService := application.NewSubscriptionDownloadService(downloadRepository, nil, nil, settingsValues(settingsService))
	downloadService.SetNotifier(notifier)
	downloadService.SetRuntimeFactory(func(values map[string]string) (application.ResourceSearcher, application.PrivateTorrentSource, map[string]application.MagnetDownloader) {
		searcher, privateSources := newResourceSearcher(values)
		var private application.PrivateTorrentSource
		if len(privateSources) > 0 {
			private = application.PrivateTorrentSources{Sources: privateSources}
		}
		downloaders := map[string]application.MagnetDownloader{
			"qbittorrent": downloadclient.NewQbittorrent(values["QBITTORRENT_URL"], values["QBITTORRENT_USERNAME"], values["QBITTORRENT_PASSWORD"], values["QBITTORRENT_DOWNLOAD_PATH"], values["QBITTORRENT_CATEGORY"], nil),
			"thunder":     downloadclient.NewThunder(values["THUNDER_URL"], values["THUNDER_FILE_ID"], values["THUNDER_AUTHORIZATION"], nil),
		}
		// 115 只在已绑定账号时注册：未登录时让调度明确报「默认下载器未配置」，
		// 而不是把每个订阅任务都记成一次失败的 115 调用。
		if linked, e := pan115Service.Linked(context.Background()); e != nil {
			logging.Error(logging.CategorySystem, "读取 115 绑定状态失败，本次不注册 115 下载器")
		} else if linked {
			downloaders["pan115"] = pan115Service.Downloader(values["PAN115_SAVE_PATH"])
		}
		return searcher, private, downloaders
	})
	for i := range jobs {
		if jobs[i].Name == "标签追新" {
			jobs[i].Run = func(jobCtx context.Context) scheduler.JobResult {
				n, e := tagService.Follow(jobCtx, "")
				return scheduler.JobResult{"created": n, "success": e == nil}
			}
		}
		if jobs[i].Name == "同步榜单" {
			jobs[i].Run = collectionRankJob(collectionService)
		}
		if jobs[i].Name == "订阅下载" {
			jobs[i].Run = func(jobCtx context.Context) scheduler.JobResult {
				// 定时任务批量扫描是系统行为：只登记搜索，有资源才建立下载任务，没找到资源不推送通知。
				count, e := downloadService.RunActiveScans(jobCtx)
				if e != nil {
					logging.Error(logging.CategoryDownload, "订阅资源搜索执行失败", "error", e.Error())
				}
				return scheduler.JobResult{"queued": count}
			}
		}
		if jobs[i].Name == "同步热门演员" {
			jobs[i].Run = actorHotAndFollowJob(actorCatalog, actorService, collectionService)
		}
	}
	jobs = append(jobs, scheduler.Job{Name: "同步演员目录", Spec: actorCatalogSpec, Run: actorCatalogJob(actorCatalog, false)})
	// 日志清理按服务所在时区每天零点执行，保留天数仍在执行时读取。
	jobs = append(jobs, scheduler.Job{Name: logCleanupTaskName, Spec: logCleanupSpec, Run: func(jobCtx context.Context) scheduler.JobResult {
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
	// 定时任务表达式来自设置；保存设置时由 scheduleApplier 即时重排，不需要重启后端。
	if err := manager.Apply(scheduleSpecs(translationSettings.Values)); err != nil {
		return fmt.Errorf("注册定时任务: %w", err)
	}
	settingsService.SetScheduleApplier(func(values map[string]string) error {
		return manager.Apply(scheduleSpecs(values))
	})
	translationService, err := application.NewTranslationService(translationConfigFromSettings(translationSettings.Values), nil)
	if err != nil {
		return fmt.Errorf("create translation service: %w", err)
	}
	mediaRepository := store.Media()
	// 标签入库先查字典，缺失时翻译成简体中文再登记；翻译未启用时只做字典命中，不写入未翻译的权威名。
	var tagTranslator application.TranslationClient = translationService
	if translationSettings.Values["TRANSLATION_ENGINE"] != "" && translationSettings.Values["TRANSLATION_ENGINE"] != "none" {
		collectionService.SetTranslator(translationService)
	} else {
		tagTranslator = nil
	}
	collectionRepository.SetTagResolver(application.NewTagNameService(database.NewTagNameRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), tagTranslator))
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	workersDone := make(chan struct{})
	go func() { defer close(workersDone); collectionService.Work(workerCtx) }()
	defer func() { cancelWorkers(); <-workersDone }()
	downloadWorkerDone := make(chan struct{})
	syncTransfers := func(syncCtx context.Context) {
		observedAt := time.Now().UTC()
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
			updates = append(updates, ports.TransferState{Hash: state.Hash, Status: state.Status, AddedAt: state.AddedAt, CompletedAt: state.CompletedAt, ObservedAt: observedAt})
		}
		transitions, err := downloadRepository.SaveTransferStates(syncCtx, updates)
		if err != nil {
			logging.Error(logging.CategoryDownload, "下载状态保存失败", "error", err.Error())
			return
		}
		notifyTransferTransitions(syncCtx, notifier, transitions)
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
	downloadQueries := application.NewDownloadService(store.Downloads())
	downloadQueries.SetControls(downloadRepository, func(ctx context.Context) (map[string]ports.DownloadController, error) {
		current, err := settingsService.Get(ctx)
		if err != nil {
			return nil, err
		}
		values := current.Values
		return map[string]ports.DownloadController{"qbittorrent": downloadclient.NewQbittorrent(values["QBITTORRENT_URL"], values["QBITTORRENT_USERNAME"], values["QBITTORRENT_PASSWORD"], values["QBITTORRENT_DOWNLOAD_PATH"], values["QBITTORRENT_CATEGORY"], nil)}, nil
	})
	catalogService := application.NewCatalogService(mediaRepository)
	catalogQueries := application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)))
	subscriptionService := application.NewSubscriptionService(store.Subscriptions())
	subscriptionService.SetNotifier(notifier)
	dashboardService := application.NewDashboardService(store.Media(), store.Subscriptions(), store.Downloads())
	// 渠道 Agent 复用与 HTTP 完全相同的应用服务，不另建业务规则。
	agentDeps := agent.Deps{
		Catalog:               catalogService,
		Queries:               catalogQueries,
		Actors:                actorService,
		Tags:                  tagService,
		Subscriptions:         subscriptionService,
		SubscriptionDownloads: downloadService,
		Downloads:             downloadQueries,
		Dashboard:             dashboardService,
		Scheduler:             manager,
		Logs:                  logging.Default,
		Torrents:              settingsResourceSearcher{settings: settingsService},
		Version:               c.config.Version,
		Channels:              channels,
		Notifier:              notifier,
	}
	registry := agent.NewRegistry(agent.BuiltinTools(agentDeps)...)
	orchestrator := agent.NewOrchestrator(agentDeps, settingsValues(settingsService), registry)
	router := agent.NewRouter(orchestrator, agentDeps)
	// 轮询与 HTTP 回调共用同一个渠道处理器：业务分流与回复投递（文本、卡片、按钮刷新）只实现一次。
	dispatcher := agent.NewDispatcher(router, channels)
	channelWorkerCtx, cancelChannelWorker := context.WithCancel(ctx)
	channelWorkerDone := make(chan struct{})
	go func() {
		defer close(channelWorkerDone)
		RunChannelSupervisor(channelWorkerCtx, settingsService, dispatcher)
	}()
	defer func() { cancelChannelWorker(); <-channelWorkerDone }()
	// 事件监听与 serve 共用生命周期；保存设置后下一轮读取，无需重启。
	pan115EventService := application.NewPan115EventService(pan115Service, strmService,
		database.NewSettingsRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)),
		settingsValues(settingsService), pan115Service.EventAccountID)
	eventCtx, cancelEvents := context.WithCancel(ctx)
	eventsDone := make(chan struct{})
	go func() { defer close(eventsDone); pan115EventService.Run(eventCtx) }()
	defer func() { cancelEvents(); <-eventsDone }()
	// 封面缓存固定开启：目录是容器内 /data/cover（随 /data 一起挂载），代理沿用设置页的爬虫代理。
	coverCache := covercache.New(c.config.CoverRoot, coverProxyReader(settingsService))
	handler := httpapi.New(httpapi.Dependencies{
		Collection:            collectionService,
		Auth:                  authService,
		Catalog:               catalogService,
		CatalogQueries:        catalogQueries,
		Actors:                actorService,
		Tags:                  tagService,
		Subscriptions:         subscriptionService,
		SubscriptionDownloads: downloadService,
		Downloads:             downloadQueries,
		Dashboard:             dashboardService,
		Settings:              settingsService,
		// 版本检查复用运行版本与发布仓库记录，顶栏标签与 Agent 运行环境提示取同一来源。
		Version:         application.NewVersionService(c.config.Version, release.NewGitHubSource(c.config.ReleaseRepo, nil)),
		Pan115:          pan115Service,
		Pan115Library:   pan115LibraryService,
		Scheduler:       manager,
		Logs:            logging.Default,
		Readiness:       store.ReadinessProbe(),
		WeChatCallback:  newWeChatCallback(settingsService),
		ChannelMessages: dispatcher,
		Strm:            strmService,
		Covers:          coverCache,
		StaticDir:       c.config.WebStaticDir,
	})
	server := &http.Server{Addr: c.config.HTTPAddress, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	return runtimeapp.New(server, manager, c.config.ShutdownTimeout, logCleanupTaskName).Run(ctx)
}

// scheduleDefinitions 是设置键与调度任务名的唯一映射，注册、重排与说明都据此对齐。
var scheduleDefinitions = []struct{ name, key string }{
	{"同步榜单", "RANK_SCHEDULE_TIME"},
	{"同步热门演员", "ACTOR_SCHEDULE_TIME"},
	{"标签追新", "TAG_SCHEDULE_TIME"},
	{"订阅下载", "DOWNLOAD_SCHEDULE_TIME"},
}

// 日志清理是系统固定任务，表达式不来自设置。它必须始终出现在调度期望集合中，
// 否则 Apply 会把它当作“未配置”而取消排期。
const (
	logCleanupTaskName = "清理系统日志"
	logCleanupSpec     = "0 0 * * *"
	actorCatalogSpec   = "0 4 * * *"
)

// configuredJobs registers every configurable cron task. Domain executors are added as their
// integrations land; until then a trigger is still observable and never silently discarded.
// Cron expressions are deliberately absent here: they live in settings and reach the
// scheduler through scheduleSpecs plus Manager.Apply, so saving settings reschedules a task
// immediately instead of on the next process start.
func configuredJobs() []scheduler.Job {
	jobs := make([]scheduler.Job, 0, len(scheduleDefinitions))
	for _, definition := range scheduleDefinitions {
		name := definition.name
		jobs = append(jobs, scheduler.Job{Name: name, Run: func(context.Context) scheduler.JobResult {
			logging.Info(logging.CategoryOther, "定时任务已触发，业务执行器尚未接入", "task", name)
			return nil
		}})
	}
	return jobs
}

// scheduleSpecs 返回调度器的完整期望排期：四个可配置任务取自设置（空值表示不排期），
// 日志清理是固定任务，始终排期。Apply 以“未出现在 map 中即取消排期”为准，
// 因此这里必须给出完整集合，不能只给可配置任务。
func scheduleSpecs(values map[string]string) map[string]string {
	specs := make(map[string]string, len(scheduleDefinitions)+1)
	for _, definition := range scheduleDefinitions {
		if spec := strings.TrimSpace(values[definition.key]); spec != "" {
			specs[definition.name] = spec
		}
	}
	specs[logCleanupTaskName] = logCleanupSpec
	specs["同步演员目录"] = actorCatalogSpec
	return specs
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
