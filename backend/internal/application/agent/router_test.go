package agent

import (
	"context"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// gateNotifier 是只实现渠道开关查询的通知替身，避免测试依赖真实渠道配置。
type gateNotifier struct{ enabled bool }

func (g gateNotifier) Notify(context.Context, application.NotificationEvent, application.NotificationMessage) {
}

func (g gateNotifier) ChannelEventEnabled(context.Context, string, application.NotificationEvent) bool {
	return g.enabled
}

// TestRouterAgentChatEnabledDefaultsToOpenWithoutNotifier 验证未注入通知服务时保持渠道对话的历史行为。
func TestRouterAgentChatEnabledDefaultsToOpenWithoutNotifier(t *testing.T) {
	if !NewRouter(nil, Deps{}).agentChatEnabled(context.Background(), ports.ChannelTelegram) {
		t.Fatal("未注入通知服务时应放行对话")
	}
	if NewRouter(nil, Deps{Notifier: gateNotifier{enabled: false}}).agentChatEnabled(context.Background(), ports.ChannelTelegram) {
		t.Fatal("开关关闭时应拦截对话")
	}
}

// TestRouterBlocksAgentChatWhenChannelSwitchOff 验证关闭渠道开关后不再进入对话编排，
// 同时番号仍然返回可操作卡片。
func TestRouterBlocksAgentChatWhenChannelSwitchOff(t *testing.T) {
	settings := func(context.Context) (map[string]string, error) {
		return map[string]string{
			"AGENT_ENABLE":   "true",
			"OPENAI_URL":     "http://127.0.0.1:9/v1",
			"OPENAI_MODEL":   "test-model",
			"OPENAI_API_KEY": "test-key",
		}, nil
	}
	orchestrator := NewOrchestrator(Deps{}, settings, NewRegistry())
	if !orchestrator.Enabled(context.Background()) {
		t.Fatal("测试前提：Agent 配置应可用")
	}
	media := libraryMedia("m1", "SSIS-001", "First")
	router := NewRouter(orchestrator, Deps{
		Notifier:      gateNotifier{enabled: false},
		Queries:       application.NewCatalogQueryService(&fakeCatalogQueryRepository{media: []domain.Media{media}}),
		Subscriptions: application.NewSubscriptionService(newFakeSubscriptionRepository(media)),
	})

	reply, err := router.Handle(context.Background(), ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "1", Text: "今天有什么新片"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != agentChatDisabled {
		t.Fatalf("reply = %q, 期望 %q", reply.Text, agentChatDisabled)
	}

	reply, err = router.Handle(context.Background(), ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "1", Text: "SSIS-001"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Card == nil {
		t.Fatalf("关闭对话开关不应影响番号卡片，reply = %+v", reply)
	}
	if len(reply.Card.Buttons) != 1 || reply.Card.Buttons[0].Data != cardData(cardActionSubscribe, "SSIS-001") {
		t.Fatalf("番号卡片按钮不符: %+v", reply.Card.Buttons)
	}
}

// TestRouterCodeSkipsAgentWhenChatEnabled 验证已入库番号在 Agent 开启时也优先返回卡片，
// 不进入模型对话，保证番号操作是确定性业务且不消耗模型额度。
func TestRouterCodeSkipsAgentWhenChatEnabled(t *testing.T) {
	var calls int
	settings := func(context.Context) (map[string]string, error) {
		return map[string]string{
			"AGENT_ENABLE":   "true",
			"OPENAI_URL":     "http://127.0.0.1:9/v1",
			"OPENAI_MODEL":   "test-model",
			"OPENAI_API_KEY": "test-key",
		}, nil
	}
	media := libraryMedia("m1", "SSIS-001", "First")
	deps := Deps{
		Notifier:      gateNotifier{enabled: true},
		Queries:       application.NewCatalogQueryService(&fakeCatalogQueryRepository{media: []domain.Media{media}}),
		Subscriptions: application.NewSubscriptionService(newFakeSubscriptionRepository(media)),
	}
	orchestrator := NewOrchestrator(deps, settings, NewRegistry(BuiltinTools(deps)...))
	orchestrator.settings = func(ctx context.Context) (map[string]string, error) {
		calls++
		return settings(ctx)
	}
	router := NewRouter(orchestrator, deps)

	reply, err := router.Handle(context.Background(), ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "1", Text: "ssis 001"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Card == nil || len(reply.Card.Buttons) == 0 {
		t.Fatalf("已入库番号应返回卡片，reply = %+v", reply)
	}
	if reply.Card.Title != "SSIS-001" {
		t.Fatalf("卡片标题应为归一化番号，实际 %q", reply.Card.Title)
	}

	// 未入库的番号保持原有分流：交给 Agent 对话，而不是回一张没有按钮的卡片。
	reply, err = router.Handle(context.Background(), ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "1", Text: "SSIS-999"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Card != nil {
		t.Fatalf("未入库番号不应返回卡片，reply = %+v", reply)
	}
	if calls == 0 {
		t.Fatal("未入库番号应交给 Agent 对话处理")
	}
}
