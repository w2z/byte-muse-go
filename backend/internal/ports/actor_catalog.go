package ports

import "context"

// ActorProfile 是演员目录元数据；Aliases 只用于查找，不代表新增订阅。
type ActorProfile struct {
	Name    string
	Photo   string
	Aliases []string
}

// ActorCatalogStore 逐演员幂等保存资料；热门快照仅在完整采集成功后替换。
type ActorCatalogStore interface {
	SaveActorProfile(context.Context, ActorProfile) (bool, error)
	PublishHotActors(context.Context, []ActorProfile) error
}
