package database

import "testing"

// TestMigrationPlanVersionsAreUniqueAndOrdered 约束每个方言的迁移版本号唯一且严格递增。
// 迁移执行器按版本号判断是否已应用，重复版本号会被静默跳过并导致结构缺失，
// 因此新增迁移必须先取当前最大版本的下一个编号，本测试作为回归护栏。
func TestMigrationPlanVersionsAreUniqueAndOrdered(t *testing.T) {
	for _, dialect := range []Dialect{DialectSQLite, DialectPostgres, DialectMySQL} {
		plan := MigrationPlan(dialect)
		if len(plan) == 0 {
			t.Fatalf("%s: migration plan is empty", dialect)
		}
		seen := make(map[int64]string, len(plan))
		var previous int64
		for index, migration := range plan {
			if migration.Name == "" {
				t.Fatalf("%s: migration %d has empty name", dialect, migration.Version)
			}
			if existing, ok := seen[migration.Version]; ok {
				t.Fatalf("%s: version %d is reused by %q and %q", dialect, migration.Version, existing, migration.Name)
			}
			seen[migration.Version] = migration.Name
			if index > 0 && migration.Version <= previous {
				t.Fatalf("%s: version %d appears after %d, versions must strictly increase", dialect, migration.Version, previous)
			}
			previous = migration.Version
		}
	}
}
