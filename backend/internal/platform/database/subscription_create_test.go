package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// TestSubscriptionCreateReturnsMediaSnapshot 验证创建订阅后返回的订阅带出影片快照。
// 订阅成功通知的标题与推送封面都取自这份快照，缺少它时通知只剩番号且没有配图。
func TestSubscriptionCreateReturnsMediaSnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "subscription-create.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,poster_url,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", "m1", "SSIS-001", "原标题", "https://img.example/poster.jpg", "none", "absent", created, created); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO legacy_media_metadata (media_id,code,banner_url,legacy_status,legacy_mode) VALUES (?,?,?,?,?)", "m1", "SSIS-001", "https://img.example/banner.jpg", "SUBSCRIBE", "strict"); err != nil {
		t.Fatal(err)
	}
	item, createdNow, err := store.Subscriptions().Create(ctx, ports.CreateSubscription{
		IdempotencyKey: "channel:SSIS-001",
		MediaID:        "m1",
		Mode:           domain.SubscriptionModeStrict,
		Filter:         map[string]any{},
	})
	if err != nil || !createdNow {
		t.Fatalf("创建订阅失败: err=%v created=%v", err, createdNow)
	}
	if item.Media == nil {
		t.Fatal("创建订阅后应带出影片快照")
	}
	if item.Media.Code != "SSIS-001" || item.Media.Title != "原标题" {
		t.Fatalf("影片快照文案 = %#v", item.Media)
	}
	if item.Media.BannerURL == nil || *item.Media.BannerURL != "https://img.example/banner.jpg" {
		t.Fatalf("影片快照封面 = %#v", item.Media.BannerURL)
	}
	for _, sample := range []struct {
		name        string
		translation any
		want        string
	}{
		{"已翻译", "翻译后的标题", "翻译后的标题"},
		{"未翻译", nil, "原标题"},
		{"空白译文", "  ", "原标题"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			if _, err := store.SQLDB().ExecContext(ctx, "UPDATE media SET translated_title=? WHERE id=?", sample.translation, "m1"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.Subscriptions().Cancel(ctx, item.ID); err != nil {
				t.Fatal(err)
			}
			notifier := &flowNotifier{}
			service := application.NewSubscriptionService(store.Subscriptions())
			service.SetNotifier(notifier)
			var createErr error
			item, _, createErr = service.Create(ctx, application.CreateSubscriptionCommand{IdempotencyKey: "notify-" + sample.name, MediaID: "m1", Mode: domain.SubscriptionModeStrict})
			if createErr != nil {
				t.Fatal(createErr)
			}
			if len(notifier.messages) != 1 {
				t.Fatalf("通知数量 = %d", len(notifier.messages))
			}
			message := notifier.messages[0]
			if message.Title != "标题: "+sample.want || message.Text != "番号: SSIS-001\n状态: 已加入订阅列表\n描述: "+sample.want || message.CoverURL != "https://img.example/banner.jpg" {
				t.Fatalf("数据库影片通知 = %+v", message)
			}
		})
	}
}

// failingMediaQueryExecutor 让影片快照查询失败、其余语句正常透传，用于覆盖快照读取失败的降级路径。
type failingMediaQueryExecutor struct {
	sqlExecutor
}

func (e failingMediaQueryExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "FROM media m") {
		return nil, errors.New("影片库暂时不可读")
	}
	return e.sqlExecutor.QueryContext(ctx, query, args...)
}

// TestSubscriptionSnapshotFailureLogsWithoutInternalID 约束快照读取失败时只说明影片信息获取失败。
// 番号正是这次没读到的东西，日志里出现内部 media_id 对用户没有任何意义。
func TestSubscriptionSnapshotFailureLogsWithoutInternalID(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Dialect: DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "snapshot-failure.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.SQLDB().ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "m1", "SSIS-001", "原标题", "none", "absent", created, created); err != nil {
		t.Fatal(err)
	}

	buffer := &bytes.Buffer{}
	previous := logging.Default
	logging.Default = logging.New(buffer)
	defer func() { logging.Default = previous }()

	repository := &sqlSubscriptionRepository{dialect: DialectSQLite, exec: failingMediaQueryExecutor{sqlExecutor: store.SQLDB()}, db: store.SQLDB()}
	item, createdNow, err := repository.Create(ctx, ports.CreateSubscription{IdempotencyKey: "channel:SSIS-001", MediaID: "m1", Mode: domain.SubscriptionModeStrict, Filter: map[string]any{}})
	if err != nil || !createdNow {
		t.Fatalf("快照读取失败不应把已提交的创建报成失败: err=%v created=%v", err, createdNow)
	}
	if item.Media != nil {
		t.Fatalf("快照读取失败时不应带出影片: %#v", item.Media)
	}

	output := buffer.String()
	if !strings.Contains(output, `"msg":"订阅已保存，影片信息获取失败"`) || !strings.Contains(output, `"error":"影片库暂时不可读"`) {
		t.Fatalf("快照失败日志 = %s", output)
	}
	if strings.Contains(output, "media_id") {
		t.Fatalf("快照失败日志不应出现内部 media_id：%s", output)
	}
}
