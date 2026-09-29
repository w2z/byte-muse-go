package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"bytemuse/backend/internal/ports"
)

// recordingSender 记录渠道实际投递出去的消息，用于验证端到端回复内容。
type recordingSender struct {
	mu          sync.Mutex
	messages    []string
	cards       []ports.OutboundCard
	refreshes   []string
	sendCardErr error
}

func (s *recordingSender) SendText(_ context.Context, chatID, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, chatID+"|"+text)
	return nil
}

func (s *recordingSender) SendPhoto(context.Context, string, string, string, string) error {
	return nil
}

// SendCard 记录卡片，并把「标题+正文」计入消息流，便于同时断言卡片与文本形态。
func (s *recordingSender) SendCard(_ context.Context, chatID string, card ports.OutboundCard) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendCardErr != nil {
		return s.sendCardErr
	}
	s.cards = append(s.cards, card)
	s.messages = append(s.messages, chatID+"|"+card.Title+"\n"+card.Text)
	return nil
}

// ReplaceButtons 记录按钮刷新请求，不修改已发送的卡片快照。
func (s *recordingSender) ReplaceButtons(_ context.Context, chatID, messageID string, _ []ports.ActionButton) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshes = append(s.refreshes, chatID+"|"+messageID)
	return nil
}

func (s *recordingSender) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.messages...)
}

// lastCard 返回最近一次发送的卡片。
func (s *recordingSender) lastCard() (ports.OutboundCard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cards) == 0 {
		return ports.OutboundCard{}, false
	}
	return s.cards[len(s.cards)-1], true
}

// refreshCount 返回按钮刷新次数。
func (s *recordingSender) refreshCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.refreshes)
}

// fakeChannels 是 ChannelResolver 的测试替身：只解析已声明的渠道。
type fakeChannels struct{ sender ports.ChannelSender }

func (c fakeChannels) Sender(channel string) (ports.ChannelSender, bool) {
	if c.sender == nil || (channel != ports.ChannelTelegram && channel != ports.ChannelWeChat) {
		return nil, false
	}
	return c.sender, true
}

// fakeUpstream 返回一个 OpenAI 兼容服务，第一轮要求调用工具，第二轮返回最终文本。
func fakeUpstream(t *testing.T, calls *[]map[string]any) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("意外的上游请求 %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("解析上游请求失败: %v", err)
		}
		mu.Lock()
		*calls = append(*calls, payload)
		round := len(*calls)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if round == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_version","arguments":"{}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"当前版本是 v9.9.9-test。"}}]}`))
	}))
}

// agentSettings 返回一套完整的 Agent 配置，使对话链路真实可用。
func agentSettings(endpoint string) SettingsProvider {
	return func(context.Context) (map[string]string, error) {
		return map[string]string{
			"AGENT_ENABLE":   "true",
			"OPENAI_URL":     endpoint + "/v1",
			"OPENAI_MODEL":   "test-model",
			"OPENAI_API_KEY": "test-key",
		}, nil
	}
}

// TestDispatcherAgentConversationCallsToolAndReplies 验证「入站消息 → 模型 → 工具 → 最终回复 → 渠道投递」整条链路。
func TestDispatcherAgentConversationCallsToolAndReplies(t *testing.T) {
	var calls []map[string]any
	upstream := fakeUpstream(t, &calls)
	defer upstream.Close()

	sender := &recordingSender{}
	deps := Deps{Version: "v9.9.9-test", Channels: fakeChannels{sender: sender}}
	orchestrator := NewOrchestrator(deps, agentSettings(upstream.URL), NewRegistry(BuiltinTools(deps)...))
	dispatcher := NewDispatcher(NewRouter(orchestrator, deps), fakeChannels{sender: sender})

	message := ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "42", UserID: "7", Text: "现在是什么版本？"}
	if err := dispatcher.Handle(context.Background(), message); err != nil {
		t.Fatalf("处理失败: %v", err)
	}

	got := sender.all()
	if len(got) != 1 || got[0] != "42|当前版本是 v9.9.9-test。" {
		t.Fatalf("渠道投递内容不符: %#v", got)
	}
	if len(calls) != 2 {
		t.Fatalf("期望两轮模型调用，实际 %d", len(calls))
	}
	second, _ := json.Marshal(calls[1])
	if !strings.Contains(string(second), `"tool_call_id":"call_1"`) || !strings.Contains(string(second), "v9.9.9-test") {
		t.Fatalf("工具结果未回填给模型: %s", second)
	}
	if tools, ok := calls[0]["tools"].([]any); !ok || len(tools) == 0 {
		t.Fatalf("首轮未携带工具定义")
	}
}

// TestDispatcherSendMessageToolDeliversToChannel 验证 send_message 工具复用同一渠道解析器主动推送。
func TestDispatcherSendMessageToolDeliversToChannel(t *testing.T) {
	var mu sync.Mutex
	round := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		round++
		current := round
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if current == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"send_message","arguments":"{\"message\":\"订阅已完成\"}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"好的，已推送。"}}]}`))
	}))
	defer upstream.Close()

	sender := &recordingSender{}
	deps := Deps{Version: "v1", Channels: fakeChannels{sender: sender}}
	orchestrator := NewOrchestrator(deps, agentSettings(upstream.URL), NewRegistry(BuiltinTools(deps)...))
	dispatcher := NewDispatcher(NewRouter(orchestrator, deps), fakeChannels{sender: sender})

	message := ports.InboundMessage{Channel: ports.ChannelWeChat, ChatID: "zhangsan", UserID: "zhangsan", Text: "帮我推送一条消息"}
	if err := dispatcher.Handle(context.Background(), message); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	got := sender.all()
	if len(got) != 2 || got[0] != "zhangsan|订阅已完成" || got[1] != "zhangsan|好的，已推送。" {
		t.Fatalf("主动推送与最终回复不符: %#v", got)
	}
}

// TestDispatcherSlashCommands 验证斜杠命令菜单、版本、清空上下文与未知命令提示。
func TestDispatcherSlashCommands(t *testing.T) {
	sender := &recordingSender{}
	deps := Deps{Version: "v9.9.9-test", Channels: fakeChannels{sender: sender}}
	dispatcher := NewDispatcher(NewRouter(NewOrchestrator(deps, nil, NewRegistry()), deps), fakeChannels{sender: sender})

	for _, tc := range []struct {
		text string
		want string
	}{
		{"/start", "欢迎使用 ByteMuse 助手"},
		{"/start", "/download_subscribe"},
		{"/help", "直接发送番号返回影片卡片"},
		{"/help", "使用 /start 查看欢迎信息"},
		{"/version", "当前版本：v9.9.9-test"},
		{"/clear", "当前对话上下文已清空"},
		{"/unknown", "未知命令：/unknown"},
		{"/version@byte_muse_bot", "当前版本：v9.9.9-test"},
	} {
		before := len(sender.all())
		msg := ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "42", UserID: "7", Text: tc.text}
		if err := dispatcher.Handle(context.Background(), msg); err != nil {
			t.Fatalf("%s 处理失败: %v", tc.text, err)
		}
		got := sender.all()
		if len(got) != before+1 {
			t.Fatalf("%s 期望投递一条回复，实际 %d 条", tc.text, len(got)-before)
		}
		if reply := strings.TrimPrefix(got[len(got)-1], "42|"); !strings.Contains(reply, tc.want) {
			t.Fatalf("%s 回复未包含 %q: %s", tc.text, tc.want, reply)
		}
	}
}

// TestStartWelcomeFollowsAgentAvailability 验证 /start 欢迎信息随 Agent 配置变化，并与 /help 内容区分。
func TestStartWelcomeFollowsAgentAvailability(t *testing.T) {
	deliver := func(settings SettingsProvider) string {
		t.Helper()
		sender := &recordingSender{}
		deps := Deps{Version: "v1", Channels: fakeChannels{sender: sender}}
		dispatcher := NewDispatcher(NewRouter(NewOrchestrator(deps, settings, NewRegistry()), deps), fakeChannels{sender: sender})
		msg := ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "42", UserID: "7", Text: "/start"}
		if err := dispatcher.Handle(context.Background(), msg); err != nil {
			t.Fatalf("处理失败: %v", err)
		}
		got := sender.all()
		if len(got) != 1 {
			t.Fatalf("期望一条欢迎信息，实际 %#v", got)
		}
		return strings.TrimPrefix(got[0], "42|")
	}

	disabled := deliver(func(context.Context) (map[string]string, error) {
		return map[string]string{"AGENT_ENABLE": "false"}, nil
	})
	if !strings.Contains(disabled, "在设置页启用「对话 Agent」后") {
		t.Fatalf("未启用 Agent 的欢迎信息缺少指引: %s", disabled)
	}
	if strings.Contains(disabled, "也可以直接用中文提问") {
		t.Fatalf("未启用 Agent 时不应承诺中文提问: %s", disabled)
	}
	if !strings.Contains(disabled, "欢迎使用 ByteMuse 助手") || !strings.Contains(disabled, "/subscribe - 获取当前订阅列表") {
		t.Fatalf("欢迎信息缺少问候或命令清单: %s", disabled)
	}

	enabled := deliver(func(context.Context) (map[string]string, error) {
		return map[string]string{
			"AGENT_ENABLE":   "true",
			"OPENAI_URL":     "https://api.openai.com/v1",
			"OPENAI_MODEL":   "gpt-4o-mini",
			"OPENAI_API_KEY": "test-key",
		}, nil
	})
	if !strings.Contains(enabled, "也可以直接用中文提问") {
		t.Fatalf("启用 Agent 的欢迎信息应说明中文提问能力: %s", enabled)
	}

	// /help 是使用帮助，不再复用欢迎语，避免两个命令回同一段文本。
	sender := &recordingSender{}
	deps := Deps{Version: "v1", Channels: fakeChannels{sender: sender}}
	dispatcher := NewDispatcher(NewRouter(NewOrchestrator(deps, nil, NewRegistry()), deps), fakeChannels{sender: sender})
	msg := ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "42", UserID: "7", Text: "/help"}
	if err := dispatcher.Handle(context.Background(), msg); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	help := strings.TrimPrefix(sender.all()[0], "42|")
	if strings.Contains(help, "欢迎使用 ByteMuse 助手") {
		t.Fatalf("/help 不应返回欢迎语: %s", help)
	}
}

// TestDispatcherAgentDisabledFallsBackToHelp 验证未启用 Agent 时中文提问返回可执行指引，且不发起模型请求。
func TestDispatcherAgentDisabledFallsBackToHelp(t *testing.T) {
	sender := &recordingSender{}
	deps := Deps{Version: "v1", Channels: fakeChannels{sender: sender}}
	settings := func(context.Context) (map[string]string, error) {
		return map[string]string{"AGENT_ENABLE": "false"}, nil
	}
	orchestrator := NewOrchestrator(deps, settings, NewRegistry(BuiltinTools(deps)...))
	dispatcher := NewDispatcher(NewRouter(orchestrator, deps), fakeChannels{sender: sender})

	msg := ports.InboundMessage{Channel: ports.ChannelWeChat, ChatID: "lisi", UserID: "lisi", Text: "今天有什么新片？"}
	if err := dispatcher.Handle(context.Background(), msg); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	got := sender.all()
	if len(got) != 1 || !strings.Contains(got[0], "启用「对话 Agent」") {
		t.Fatalf("未启用时未给出指引: %#v", got)
	}
}

// TestDispatcherUnconfiguredChannelReportsError 验证渠道未配置时返回明确错误，便于调用方记日志。
func TestDispatcherUnconfiguredChannelReportsError(t *testing.T) {
	deps := Deps{Version: "v1"}
	dispatcher := NewDispatcher(NewRouter(NewOrchestrator(deps, nil, NewRegistry()), deps), fakeChannels{})
	msg := ports.InboundMessage{Channel: ports.ChannelTelegram, ChatID: "42", Text: "/version"}
	err := dispatcher.Handle(context.Background(), msg)
	if err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("期望渠道未配置错误，实际 %v", err)
	}
}
