package bootstrap

import (
	"context"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
	"bytemuse/backend/internal/scheduler"
)

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
