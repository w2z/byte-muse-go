package application

import "bytemuse/backend/internal/ports"

// CollectionService 统一人工与调度的持久化采集任务，不触发订阅下载。
type CollectionService struct {
	collector  ports.Collector
	queue      ports.CollectionQueue
	translator TranslationClient
}

// Sources 返回当前接入的数据源能力，不代表外站实时可达。
func (s *CollectionService) Sources() []ports.CollectionSource { return s.collector.Sources() }
