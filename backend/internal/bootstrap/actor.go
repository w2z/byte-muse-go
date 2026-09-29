package bootstrap

import (
	"context"
	"fmt"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/ports"
	"bytemuse/backend/internal/scheduler"
)

// ActorSync 供运维执行首次导入或重试，复用定时任务服务与当前代理配置。
func (c *Commands) ActorSync(ctx context.Context, source string) error {
	store, err := c.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		return err
	}
	settings, err := application.NewSettingsService(database.NewSettingsRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), c.config.DatabaseDriver, c.config.SessionSecret)
	if err != nil {
		return err
	}
	values, err := settings.Get(ctx)
	if err != nil {
		return err
	}
	client, err := collector.NewClientWithProxy(values.Values["PROXY"])
	if err != nil {
		return err
	}
	client.ConfigureBypass(values.Values["BYPASS_ENGINE"], values.Values["BYPASS_URL"], values.Values["BYPASS_USE_PROXY"] == "true")
	service := application.NewActorCatalogService(database.NewActorCatalogRepository(store.SQLDB(), database.Dialect(c.config.DatabaseDriver)), func(ctx context.Context) ([]ports.ActorProfile, error) { return collector.HotActors(ctx, client) }, func(ctx context.Context) ([]ports.ActorProfile, error) { return collector.GfriendsActors(ctx, client) })
	result, err := service.Sync(ctx, source == "hot")
	fmt.Printf("来源 %s：获取 %d，新增 %d，保存 %d，失败 %d，热门发布 %t\n", source, result.Fetched, result.Inserted, result.Saved, result.Failed, result.Published)
	return err
}

// actorCatalogJob 将采集计数写入任务完成日志；网络失败不清空旧热门榜。
func actorCatalogJob(service *application.ActorCatalogService, hot bool) func(context.Context) scheduler.JobResult {
	return func(ctx context.Context) scheduler.JobResult {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		result, err := service.Sync(ctx, hot)
		source := "gfriends"
		if hot {
			source = "javdb"
		}
		if err != nil {
			logging.Error(logging.CategoryCollection, "演员资料同步失败", "source", source, "error", err.Error())
		}
		return scheduler.JobResult{"source": source, "fetched": result.Fetched, "saved": result.Saved, "inserted": result.Inserted, "failed": result.Failed, "published": result.Published, "success": err == nil}
	}
}

// actorHotAndFollowJob 同步热门演员后继续处理已有演员订阅，沿用原有 ACTOR_SCHEDULE_TIME。
// 两步互不共享事务；热门源站失败不会阻断已订阅演员的追新队列。
func actorHotAndFollowJob(catalog *application.ActorCatalogService, actors *application.ActorService, collections *application.CollectionService) func(context.Context) scheduler.JobResult {
	return func(ctx context.Context) scheduler.JobResult {
		hot := actorCatalogJob(catalog, true)(ctx)
		follow := actorFollowJob(actors, collections)(ctx)
		return scheduler.JobResult{"source": hot["source"], "fetched": hot["fetched"], "saved": hot["saved"], "inserted": hot["inserted"], "failed": hot["failed"], "published": hot["published"], "follow_enqueued": follow["enqueued"], "follow_created": follow["created"], "success": hot["success"] == true && follow["success"] == true}
	}
}

// actorFollowJob 先为每个已订阅演员入队 AVBase 作品采集，再匹配本地影片建立订阅。
// 采集在持久化队列中异步执行，追新只使用当前已入库影片；单个演员入队失败不阻止其余。
func actorFollowJob(actors *application.ActorService, collections *application.CollectionService) func(context.Context) scheduler.JobResult {
	return func(ctx context.Context) scheduler.JobResult {
		enqueued, failed := 0, 0
		names, listErr := actors.ActiveNames(ctx)
		if listErr != nil {
			logging.Error(logging.CategoryCollection, "读取已订阅演员失败", "error", listErr.Error())
		}
		for _, name := range names {
			if _, e := collections.Enqueue(ctx, ports.CollectionRequest{Source: "avbase", Kind: "actor", Query: name, Page: 1}); e != nil {
				failed++
			} else {
				enqueued++
			}
		}
		created, followErr := actors.Follow(ctx, "")
		return scheduler.JobResult{"enqueued": enqueued, "enqueue_failed": failed, "created": created, "success": listErr == nil && followErr == nil}
	}
}
