package application

import "strings"

// ScheduleDefinition 统一任务名称、持久化键及旧版本缺失配置时的默认计划。
type ScheduleDefinition struct {
	Name    string
	Key     string
	Default string
}

// ScheduleDefinitions 返回独立副本；空字符串表示暂停定时执行，缺失键才使用默认值。
func ScheduleDefinitions() []ScheduleDefinition {
	return []ScheduleDefinition{
		{"同步榜单", "RANK_SCHEDULE_TIME", ""},
		{"同步热门演员", "ACTOR_SCHEDULE_TIME", ""},
		{"标签追新", "TAG_SCHEDULE_TIME", ""},
		{"订阅下载", "DOWNLOAD_SCHEDULE_TIME", ""},
		{"同步上新", "RELEASE_SCHEDULE_TIME", "0 3 * * *"},
		{"同步演员目录", "ACTOR_CATALOG_SCHEDULE_TIME", "0 4 * * *"},
		{"清理系统日志", "LOG_CLEANUP_SCHEDULE_TIME", "0 0 * * *"},
	}
}

// ScheduleSpecs 返回完整期望计划，启动和保存设置共用，空计划保留手动执行入口。
func ScheduleSpecs(values map[string]string) map[string]string {
	specs := make(map[string]string)
	for _, definition := range ScheduleDefinitions() {
		spec, exists := values[definition.Key]
		if !exists {
			spec = definition.Default
		}
		if spec = strings.TrimSpace(spec); spec != "" {
			specs[definition.Name] = spec
		}
	}
	return specs
}
