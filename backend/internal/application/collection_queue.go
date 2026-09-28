package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/ports"
)

// NewQueuedCollectionService 装配持久化队列和可选翻译器；nil 翻译器表示翻译未启用。
func NewQueuedCollectionService(c ports.Collector, q ports.CollectionQueue, t TranslationClient) *CollectionService {
	return &CollectionService{collector: c, queue: q, translator: t}
}

// SetTranslator 仅允许在后台消费者启动前装配，nil 表示不登记新翻译任务。
func (s *CollectionService) SetTranslator(t TranslationClient) { s.translator = t }

// Enqueue 校验后仅写入持久化队列，后台生命周期不依赖 HTTP 请求。
func (s *CollectionService) Enqueue(ctx context.Context, req ports.CollectionRequest) (ports.CollectionRun, error) {
	if s.queue == nil {
		return ports.CollectionRun{}, errors.New("collection_queue_unavailable")
	}
	if req.Page == 0 {
		req.Page = 1
	}
	if req.Kind == "rank" && req.Page != 1 {
		return ports.CollectionRun{}, collector.ErrInvalidRequest
	}
	validator := collector.NewRegistry(nil)
	if e := validator.Validate(req); e != nil {
		return ports.CollectionRun{}, e
	}
	return s.queue.CreateRun(ctx, req)
}

// RunStatus 返回持久化进度，供 HTTP 与任务日志共同使用。
func (s *CollectionService) RunStatus(ctx context.Context, id string) (ports.CollectionRun, error) {
	if s.queue == nil {
		return ports.CollectionRun{}, errors.New("collection_queue_unavailable")
	}
	return s.queue.GetRun(ctx, id)
}

// Work 启动三个有界阶段消费者；页面、单视频入库和翻译互不阻塞，取消时等待退出。
func (s *CollectionService) Work(ctx context.Context) {
	var wg sync.WaitGroup
	for _, kind := range []string{"page", "video", "translation"} {
		wg.Add(1)
		go func(kind string) {
			defer wg.Done()
			for ctx.Err() == nil {
				worked, e := s.ProcessOne(ctx, kind, time.Now())
				if e != nil {
					logging.Error(logging.CategoryCollection, "队列处理失败", "stage", kind)
				}
				if !worked || e != nil {
					timer := time.NewTimer(250 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
		}(kind)
	}
	wg.Wait()
}

// ProcessOne 处理一个已领取阶段任务；网络调用在事务外，单次有界，失败可恢复。
func (s *CollectionService) ProcessOne(ctx context.Context, kind string, now time.Time) (bool, error) {
	w, e := s.queue.Claim(ctx, kind, now)
	if e != nil || w == nil {
		return false, e
	}
	jobCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	switch w.Kind {
	case "page":
		req := w.Request
		req.Page = w.Page
		var b ports.CollectionBatch
		b, e = s.collector.Collect(jobCtx, req)
		if e == nil {
			e = s.queue.SavePage(jobCtx, *w, b)
		}
	case "video":
		e = s.queue.SaveVideo(jobCtx, *w, s.translator != nil)
	case "translation":
		var title string
		var skip bool
		title, skip, e = s.queue.TranslationInput(jobCtx, *w)
		if e == nil {
			value := ""
			if !skip {
				if s.translator == nil {
					e = errors.New("translation_disabled")
				} else {
					value, e = s.translator.Translate(jobCtx, TranslationRequest{Text: title, TargetLanguage: "ZH-CN"})
					if e == nil && strings.TrimSpace(value) == "" {
						e = errors.New("translation_empty")
					}
				}
			}
			if e == nil {
				e = s.queue.SaveTranslation(jobCtx, *w, value)
			}
		}
	}
	if e != nil {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		code := "processing_failed"
		switch {
		case errors.Is(e, collector.ErrBlocked):
			code = "source_blocked"
		case errors.Is(e, collector.ErrParse):
			code = "source_format_changed"
		case errors.Is(e, collector.ErrRateLimited):
			code = "source_rate_limited"
		case errors.Is(e, context.DeadlineExceeded):
			code = "stage_timeout"
		}
		if w.Kind == "translation" {
			code = "translation_failed"
		}
		if e.Error() == "pagination_loop" || e.Error() == "empty_page_with_next" {
			code = e.Error()
		}
		if fail := s.queue.FailWork(ctx, *w, code, time.Now()); fail != nil {
			return true, fail
		}
		logging.Error(logging.CategoryCollection, "采集队列任务失败", "run_id", w.RunID, "stage", w.Kind, "page", w.Page, "attempt", w.Attempt, "error_code", code)
		return true, nil
	}
	run, e := s.queue.GetRun(ctx, w.RunID)
	if e != nil {
		return true, e
	}
	logging.Info(logging.CategoryCollection, "采集队列任务完成", "run_id", w.RunID, "source", w.Request.Source, "period", w.Request.Period, "stage", w.Kind, "page", w.Page, "status", run.Status, "fetched", run.Discovered, "saved", run.Saved, "failed", run.Failed, "new", run.Counts.New, "old", run.Counts.Existing, "inserted", run.Counts.Inserted)
	return true, nil
}
