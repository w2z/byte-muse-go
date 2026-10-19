package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// CodeResolver 统一按番号查资料：本地优先，未命中才采集精确匹配项；不创建订阅或下载。
// 与采集队列共用 Collector 和 CollectionRepository，保留既有媒体字段及来源去重规则。
type CodeResolver struct {
	queries    *CatalogQueryService
	collector  ports.Collector
	repository ports.CollectionRepository
	mu         sync.Mutex
	flights    map[string]*codeResolution
}

// codeResolution 合并同一番号的并发查询；等待方取消不会发起重复采集。
type codeResolution struct {
	done  chan struct{}
	media domain.Media
	err   error
}

// NewCodeResolver 装配共用采集器与事务入库仓储，不引入另一套解析或数据写入规则。
func NewCodeResolver(queries *CatalogQueryService, collector ports.Collector, repository ports.CollectionRepository) *CodeResolver {
	return &CodeResolver{queries: queries, collector: collector, repository: repository, flights: make(map[string]*codeResolution)}
}

// Resolve 返回已持久化的精确番号资料；远程失败和未找到分开报告，不透传上游敏感响应。
// 同一番号并发共享一次有界查询，后续调用命中本地后不再访问外站。
func (s *CodeResolver) Resolve(ctx context.Context, raw string) (domain.Media, error) {
	code := domain.NormalizeCode(raw)
	if !domain.LooksLikeCode(code) {
		return domain.Media{}, errors.New("番号无效，请发送类似 SSIS-001 的番号")
	}
	s.mu.Lock()
	if flight, ok := s.flights[code]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return domain.Media{}, errors.New("番号查询已取消或超时，请稍后重试")
		case <-flight.done:
			return flight.media, flight.err
		}
	}
	flight := &codeResolution{done: make(chan struct{})}
	s.flights[code] = flight
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.flights, code)
		close(flight.done)
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	flight.media, flight.err = s.resolve(ctx, code)
	return flight.media, flight.err
}

// local 精确筛选全部本地匹配页，避免相似标题占满第一页而漏掉真实番号。
func (s *CodeResolver) local(ctx context.Context, code string) (domain.Media, bool, error) {
	if s.queries == nil {
		return domain.Media{}, false, errors.New("影片查询服务不可用")
	}
	media, found, err := s.queries.FindCode(ctx, code)
	if err != nil {
		return domain.Media{}, false, errors.New("本地影片查询失败，请稍后重试")
	}
	return media, found, nil
}

// resolve 依次尝试已启用的搜索来源；仅持久化精确匹配项，不导入相似番号或整页演员。
func (s *CodeResolver) resolve(ctx context.Context, code string) (domain.Media, error) {
	media, found, err := s.local(ctx, code)
	if err != nil || found {
		return media, err
	}
	if s.collector == nil || s.repository == nil {
		return domain.Media{}, errors.New("番号 " + code + " 不在媒体库中，远程采集服务不可用")
	}
	failed, searched := false, false
	for _, source := range s.collector.Sources() {
		if !slices.Contains(source.Kinds, "search") {
			continue
		}
		searched = true
		// 搜索限制十页；达到上限仍有下一页属于未查完，不能当成不存在。
		for page := 1; page <= 10; page++ {
			if ctx.Err() != nil {
				return domain.Media{}, errors.New("番号查询已取消或超时，请稍后重试")
			}
			req := ports.CollectionRequest{Source: source.ID, Kind: "search", Query: code, Page: page}
			batch, err := s.collector.Collect(ctx, req)
			if err != nil {
				failed = true
				break
			}
			for _, item := range batch.Items {
				if domain.NormalizeCode(item.Code) != code {
					continue
				}
				item.Code = code
				if _, err := s.repository.SaveCollection(ctx, req, ports.CollectionBatch{Items: []ports.CollectedMedia{item}}); err != nil {
					return domain.Media{}, errors.New("番号 " + code + " 已找到，但资料入库失败，请稍后重试")
				}
				media, found, err := s.local(ctx, code)
				if err != nil {
					return domain.Media{}, err
				}
				if !found {
					return domain.Media{}, errors.New("番号 " + code + " 资料入库后未能回查，请稍后重试")
				}
				return media, nil
			}
			if !batch.HasMore {
				break
			}
			if len(batch.Items) == 0 || page == 10 {
				failed = true
				break
			}
		}
	}
	if !searched {
		return domain.Media{}, errors.New("远程采集服务不可用")
	}
	if failed {
		return domain.Media{}, fmt.Errorf("番号 %s 远程查询失败或未完成，暂时无法确认是否存在，请稍后重试", code)
	}
	return domain.Media{}, fmt.Errorf("未找到番号 %s，本地及远程来源均无精确匹配", code)
}
