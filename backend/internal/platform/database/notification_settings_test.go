package database

import (
	"strings"
	"testing"

	"bytemuse/backend/internal/application"
)

// TestNotificationSettingsMigrationSeedsDeclaredSwitches 约束迁移 24 与 application 中的通知开关声明一致。
// 三个方言必须种下同样的键且不多不少：漏键会让设置页的复选框没有默认值，
// 多键说明声明与迁移出现漂移。
func TestNotificationSettingsMigrationSeedsDeclaredSwitches(t *testing.T) {
	declared := application.NotificationSettingKeys()
	for _, dialect := range []Dialect{DialectSQLite, DialectPostgres, DialectMySQL} {
		migration := notificationSettingsMigration(dialect)
		if migration.Version != 24 || migration.Name == "" {
			t.Fatalf("%s: 迁移版本/名称异常: %d %q", dialect, migration.Version, migration.Name)
		}
		sql := joinMigrationSQL([]Migration{migration})
		for _, key := range declared {
			if !strings.Contains(sql, "'"+key+"'") {
				t.Errorf("%s: 迁移未种下 %s", dialect, key)
			}
		}
		if got := strings.Count(sql, "_NOTIFY_"); got != len(declared) {
			t.Errorf("%s: 迁移种下 %d 个通知开关，声明 %d 个", dialect, got, len(declared))
		}
		if strings.Contains(sql, "ON CONFLICT") != (dialect != DialectMySQL) {
			t.Errorf("%s: 幂等写法不符合方言约定: %s", dialect, sql)
		}
	}
}

// TestNotificationSettingsMigrationDefaults 约束推送类通知默认关闭、Agent 对话默认开启。
// 推送是新增行为，默认开启会在升级后立即向渠道群发；Agent 对话是已有能力，默认关闭等于静默降级。
func TestNotificationSettingsMigrationDefaults(t *testing.T) {
	sql := joinMigrationSQL([]Migration{notificationSettingsMigration(DialectSQLite)})
	for _, key := range application.NotificationSettingKeys() {
		want := "false"
		if strings.HasSuffix(key, "_NOTIFY_AGENT_CHAT") {
			want = "true"
		}
		if !strings.Contains(sql, "('"+key+"', '"+want+"'") {
			t.Errorf("通知开关 %s 的默认值应为 %s", key, want)
		}
	}
}
