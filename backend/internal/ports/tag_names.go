package ports

import "context"

// TagNameResolver 把来源站原始标签统一为库内权威标签名。
// 采集入库、单影片入库与历史数据归一化共用该入口，同一输入必须得到同一结果。
type TagNameResolver interface {
	Resolve(ctx context.Context, names []string) (map[string]string, error)
}

// TagNameDictionary 保存权威标签名与来源别名映射，是「标签是否已存在」的唯一判定来源。
type TagNameDictionary interface {
	// Canonical 在标签字典中精确命中权威名时返回 true。
	Canonical(ctx context.Context, name string) (string, bool, error)
	// Alias 命中来源别名时返回对应权威标签名。
	Alias(ctx context.Context, alias string) (string, bool, error)
	// Register 幂等登记权威标签名与来源写法；权威名缺失时以未分类写入标签字典。
	// alias 与 canonical 相同时表示该写法本身就是权威名，登记后再次归一不必重复翻译。
	Register(ctx context.Context, alias, canonical string) error
}
