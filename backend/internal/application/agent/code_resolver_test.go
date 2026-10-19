package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/database"
	"bytemuse/backend/internal/ports"
)

// channelCodeCollector 仅替代外站；路由、采集入库、订阅和状态回查使用真实实现。
type channelCodeCollector struct{ calls int }

func (c *channelCodeCollector) Sources() []ports.CollectionSource {
	return []ports.CollectionSource{{ID: "javdb", Kinds: []string{"search"}}}
}

func (c *channelCodeCollector) Collect(context.Context, ports.CollectionRequest) (ports.CollectionBatch, error) {
	c.calls++
	return ports.CollectionBatch{Items: []ports.CollectedMedia{{SourceID: "test", Code: "TEST-001", Title: "Test", URL: "https://example.test/test"}}}, nil
}

// TestRemoteCodeCanSubscribeAcrossChannelEntrypoints 防止只有服务可用但渠道未接入、未显示按钮或未登记下载。
func TestRemoteCodeCanSubscribeAcrossChannelEntrypoints(t *testing.T) {
	for _, entry := range []string{"card", "tool", "subscribe"} {
		t.Run(entry, func(t *testing.T) {
			ctx := context.Background()
			store, err := database.Open(ctx, database.Config{Dialect: database.DialectSQLite, SQLitePath: filepath.Join(t.TempDir(), "channel.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			queries := application.NewCatalogQueryService(database.NewCatalogQueryRepository(store.SQLDB(), database.DialectSQLite))
			remote := &channelCodeCollector{}
			downloads := &recordingDownloadRepository{}
			deps := Deps{
				Queries:               queries,
				Codes:                 application.NewCodeResolver(queries, remote, database.NewCollectionRepository(store.SQLDB(), database.DialectSQLite)),
				Subscriptions:         application.NewSubscriptionService(store.Subscriptions()),
				SubscriptionDownloads: application.NewSubscriptionDownloadService(downloads, nil, nil, nil),
			}
			// 配置一个可用但绝不应被读取的 AI，证明本地未知番号也走确定性查询。
			orchestrator := NewOrchestrator(deps, func(context.Context) (map[string]string, error) {
				t.Error("番号不应访问 AI 配置")
				return nil, nil
			}, NewRegistry())
			router := NewRouter(orchestrator, deps)
			if entry == "card" {
				reply, err := router.Handle(ctx, ports.InboundMessage{Channel: ports.ChannelTelegram, Text: "test 001"})
				if err != nil || reply.Card == nil || len(reply.Card.Buttons) != 1 || reply.Card.Buttons[0].Data != "bm:sub:TEST-001" {
					t.Fatalf("reply=%+v err=%v", reply, err)
				}
			} else if entry == "tool" {
				out := NewRegistry(BuiltinTools(deps)...).Call(ctx, "search_code", map[string]any{"code": "test 001"})
				if !strings.Contains(out, `"code":"TEST-001"`) {
					t.Fatalf("out=%s", out)
				}
			}
			before, err := deps.Subscriptions.List(ctx, 1, 20, domain.SubscriptionStatusActive)
			if err != nil || before.Total != 0 || len(downloads.subscriptionIDs) != 0 {
				t.Fatalf("查询不应自动订阅/下载: %+v %v", before, err)
			}
			if entry == "card" {
				reply, err := router.Handle(ctx, ports.InboundMessage{Action: &ports.InboundAction{Data: "bm:sub:TEST-001", MessageID: "1"}})
				if err != nil || !strings.Contains(reply.Text, "已加入订阅") || reply.Refresh == nil {
					t.Fatalf("subscribe=%+v err=%v", reply, err)
				}
			} else {
				if out := SubscribeByCode(ctx, deps, "test 001"); !strings.Contains(out, "已加入订阅") {
					t.Fatal(out)
				}
			}
			after, err := deps.Subscriptions.List(ctx, 1, 20, domain.SubscriptionStatusActive)
			if err != nil || after.Total != 1 || after.Items[0].Media.Code != "TEST-001" {
				t.Fatalf("subscriptions=%+v err=%v", after, err)
			}
			if len(downloads.subscriptionIDs) != 1 || downloads.origins[0] != ports.DownloadOriginUser {
				t.Fatalf("downloads=%+v", downloads)
			}
			if out := SubscribeByCode(ctx, deps, "TEST-001"); !strings.Contains(out, "已在订阅中") {
				t.Fatal(out)
			}
			after, err = deps.Subscriptions.List(ctx, 1, 20, domain.SubscriptionStatusActive)
			if err != nil || after.Total != 1 || remote.calls != 1 {
				t.Fatalf("重复订阅: %+v calls=%d err=%v", after, remote.calls, err)
			}
		})
	}
}
