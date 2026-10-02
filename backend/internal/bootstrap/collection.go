package bootstrap

import (
	"context"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/ports"
	"bytemuse/backend/internal/scheduler"
)

// collectionReleaseTodayJob queues today's AVBase release page for the collection workers.
// The queue owns fetching, persistence, deduplication, and retry; this scheduler job only
// accepts one date-scoped request and never writes media directly.
func collectionReleaseTodayJob(service *application.CollectionService) func(context.Context) scheduler.JobResult {
	return func(ctx context.Context) scheduler.JobResult {
		enqueued, enqueueFailed := 0, 0
		_, err := service.Enqueue(ctx, ports.CollectionRequest{
			Source: "avbase",
			Kind:   "date",
			Query:  time.Now().Format("2006-01-02"),
			Page:   1,
		})
		if err != nil {
			enqueueFailed++
		} else {
			enqueued++
		}
		return scheduler.JobResult{"success": err == nil, "enqueued": enqueued, "enqueue_failed": enqueueFailed}
	}
}

// collectionRankJob 采集全部已接入的 JavDB 榜单周期，与旧自动订阅配置 RANK_TYPE 无关。
// 每个周期独立入队；一个入队失败不阻止其余周期，完成状态由持久化队列报告。
func collectionRankJob(service *application.CollectionService) func(context.Context) scheduler.JobResult {
	return func(ctx context.Context) scheduler.JobResult {
		succeeded, failed := 0, 0
		for _, period := range collector.JavDBRankPeriods() {
			_, err := service.Enqueue(ctx, ports.CollectionRequest{Source: "javdb", Kind: "rank", Period: period, Page: 1})
			if err != nil {
				failed++
			} else {
				succeeded++
			}
		}
		return scheduler.JobResult{"success": failed == 0, "enqueued_ranks": succeeded, "enqueue_failed": failed}
	}
}
