package application

import (
	"context"
	"fmt"
	"sync"

	"bytemuse/backend/internal/ports"
)

// ActorCatalogService 串行同步演员目录或热门榜，每位演员独立事务，失败可幂等重跑。
// 采集仅更新资料与热门快照，不创建订阅或下载。
type ActorCatalogService struct {
	mu      sync.Mutex
	store   ports.ActorCatalogStore
	hot     func(context.Context) ([]ports.ActorProfile, error)
	catalog func(context.Context) ([]ports.ActorProfile, error)
}

// ActorSyncResult 返回实际已提交的演员计数；热门快照发布与资料保存分开记录。
type ActorSyncResult struct {
	Fetched, Inserted, Saved, Failed int
	Published                        bool
}

// NewActorCatalogService 注入资料来源，网络读取完成后才保存，避免错误页清空旧榜。
func NewActorCatalogService(store ports.ActorCatalogStore, hot, catalog func(context.Context) ([]ports.ActorProfile, error)) *ActorCatalogService {
	return &ActorCatalogService{store: store, hot: hot, catalog: catalog}
}

// Sync 导入完整来源目录或更新热门榜；非热门导入不会修改热门状态。
func (s *ActorCatalogService) Sync(ctx context.Context, hot bool) (ActorSyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ActorSyncResult{}
	var firstErr error
	load := s.catalog
	if hot {
		load = s.hot
	}
	items, err := load(ctx)
	if err != nil {
		return result, err
	}
	if len(items) == 0 {
		return result, fmt.Errorf("empty actor catalog")
	}
	result.Fetched = len(items)
	for _, p := range items {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		inserted, e := s.store.SaveActorProfile(ctx, p)
		if e != nil {
			result.Failed++
			if firstErr == nil {
				firstErr = e
			}
			continue
		}
		result.Saved++
		if inserted {
			result.Inserted++
		}
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("actor profiles failed: %d (first: %v)", result.Failed, firstErr)
	}
	if hot {
		if err = s.store.PublishHotActors(ctx, items); err != nil {
			return result, err
		}
		result.Published = true
	}
	return result, nil
}
