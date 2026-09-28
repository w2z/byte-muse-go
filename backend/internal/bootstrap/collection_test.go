package bootstrap

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/collector"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/ports"
)

// TestCollectionRankJobIgnoresSubscriptionSelection 防止订阅筛选造成漏采或空配置不采。
func TestCollectionRankJobIgnoresSubscriptionSelection(t *testing.T) {
	for _, selection := range []string{"", "daily", "daily,weekly,monthly"} {
		t.Run(selection, func(t *testing.T) {
			ctx := context.Background()
			store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "ranks.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			repo := database.NewSettingsRepository(store.SQLDB(), database.DialectSQLite)
			if err = repo.Upsert(ctx, []ports.StoredSetting{{Key: "RANK_TYPE", Value: selection}}); err != nil {
				t.Fatal(err)
			}
			service := application.NewQueuedCollectionService(collector.NewRegistry(nil), database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite), nil)
			result := collectionRankJob(service)(ctx)
			if result["success"] != true || result["enqueued_ranks"] != 3 || result["enqueue_failed"] != 0 {
				t.Fatalf("unexpected enqueue result: %#v", result)
			}
			rows, err := store.SQLDB().Query(`SELECT r.period FROM collection_runs r JOIN collection_work w ON w.run_id=r.id WHERE r.source='javdb' AND r.kind='rank' AND w.kind='page' AND w.page_number=1 AND w.state='queued' ORDER BY r.period`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var periods []string
			for rows.Next() {
				var period string
				if err = rows.Scan(&period); err != nil {
					t.Fatal(err)
				}
				periods = append(periods, period)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(periods, []string{"daily", "monthly", "weekly"}) {
				t.Fatalf("queued periods: %v", periods)
			}
		})
	}
}

// TestCollectionRankJobContinuesAfterEnqueueFailure 验证单榜登记失败不阻止其余周期入队。
func TestCollectionRankJobContinuesAfterEnqueueFailure(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "failure.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// 仅在独立测试库注入写入故障，保留真实事务和队列行为。
	if _, err = store.SQLDB().Exec(`CREATE TRIGGER reject_daily BEFORE INSERT ON collection_runs WHEN NEW.period='daily' BEGIN SELECT RAISE(FAIL, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := application.NewQueuedCollectionService(collector.NewRegistry(nil), database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite), nil)
	result := collectionRankJob(service)(ctx)
	if result["success"] != false || result["enqueued_ranks"] != 2 || result["enqueue_failed"] != 1 {
		t.Fatalf("unexpected partial result: %#v", result)
	}
	var count int
	if err = store.SQLDB().QueryRow(`SELECT COUNT(*) FROM collection_runs WHERE period IN ('weekly','monthly')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("remaining ranks: %d, %v", count, err)
	}
}
