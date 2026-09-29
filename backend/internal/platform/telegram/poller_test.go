package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// handlerFunc 让测试用函数实现 ports.ChannelMessageHandler。
// 回复投递由处理器自行完成，与生产环境的 Dispatcher 行为一致。
type handlerFunc func(context.Context, ports.InboundMessage) error

func (f handlerFunc) Handle(ctx context.Context, msg ports.InboundMessage) error { return f(ctx, msg) }

// newTestPoller 构造测试用轮询器：丢弃日志并缩短退避，避免测试等待真实 3 秒。
func newTestPoller(client *Client, whitelist []string, handler ports.ChannelMessageHandler) *Poller {
	poller := NewPoller(client, whitelist, handler, slog.New(slog.NewTextHandler(io.Discard, nil)))
	poller.backoff = 5 * time.Millisecond
	return poller
}

// runPoller 在后台启动轮询并返回停止函数与结果通道。
func runPoller(t *testing.T, poller *Poller) (func(), chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- poller.Run(ctx) }()
	return cancel, done
}

// stopPoller 取消上下文并断言 Run 返回 nil。
func stopPoller(t *testing.T, cancel func(), done chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

// waitForReplies 等待收集到指定数量的回复；必须等回复落库后再取消，否则在途请求会被中断。
func waitForReplies(t *testing.T, mu *sync.Mutex, replies *[]string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		snapshot := append([]string(nil), (*replies)...)
		mu.Unlock()
		if len(snapshot) >= want {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("expected at least %d replies, got %v", want, *replies)
	return nil
}

// syncBuffer 是可并发读写的日志缓冲，避免测试读取与轮询器写入互相竞争。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(chunk []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(chunk)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForLog 等待日志中出现指定消息；超时即失败，避免固定 sleep 拖慢测试。
func waitForLog(t *testing.T, buffer *syncBuffer, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buffer.String(), message) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("日志中未出现 %q: %q", message, buffer.String())
}

// updateOnceServer 返回一个假服务器：只有第一次 getUpdates 返回给定更新，之后返回空列表，
// 避免轮询器对同一条消息重复投递。
func updateOnceServer(t *testing.T, update string, replies *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	served := false
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch methodOf(r.URL.Path) {
		case "getUpdates":
			mu.Lock()
			first := !served
			served = true
			mu.Unlock()
			if first {
				writeJSON(w, `{"ok":true,"result":[`+update+`]}`)
				return
			}
			writeJSON(w, `{"ok":true,"result":[]}`)
		case "sendChatAction":
			writeJSON(w, `{"ok":true,"result":true}`)
		case "sendMessage":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendMessage payload: %v", err)
			}
			mu.Lock()
			*replies = append(*replies, payload.Text)
			mu.Unlock()
			writeJSON(w, `{"ok":true,"result":{"message_id":1}}`)
		default:
			t.Errorf("unexpected method %q", methodOf(r.URL.Path))
			writeJSON(w, `{"ok":true,"result":true}`)
		}
	}))
}

func TestPollerAdvancesOffsetAndDispatches(t *testing.T) {
	var mu sync.Mutex
	var offsets []int64
	var replies []string
	calls := 0
	secondPoll := make(chan int64, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch methodOf(r.URL.Path) {
		case "getUpdates":
			var payload struct {
				Offset int64 `json:"offset"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode getUpdates payload: %v", err)
			}
			mu.Lock()
			offsets = append(offsets, payload.Offset)
			index := calls
			calls++
			mu.Unlock()
			if index == 0 {
				writeJSON(w, `{"ok":true,"result":[{"update_id":100,"message":{"text":"查询进度","chat":{"id":555},"from":{"id":42}}}]}`)
				return
			}
			if index == 1 {
				select {
				case secondPoll <- payload.Offset:
				default:
				}
			}
			writeJSON(w, `{"ok":true,"result":[]}`)
		case "sendChatAction":
			writeJSON(w, `{"ok":true,"result":true}`)
		case "sendMessage":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendMessage payload: %v", err)
			}
			mu.Lock()
			replies = append(replies, payload.Text)
			mu.Unlock()
			writeJSON(w, `{"ok":true,"result":{"message_id":1}}`)
		default:
			t.Errorf("unexpected method %q", methodOf(r.URL.Path))
			writeJSON(w, `{"ok":true,"result":true}`)
		}
	}))
	defer server.Close()

	handled := make(chan ports.InboundMessage, 1)
	client := newTestClient(t, server.URL)
	poller := newTestPoller(client, nil, handlerFunc(func(ctx context.Context, msg ports.InboundMessage) error {
		handled <- msg
		return client.SendText(ctx, msg.ChatID, "当前进度 50%")
	}))
	cancel, done := runPoller(t, poller)

	select {
	case msg := <-handled:
		if msg.Channel != ports.ChannelTelegram || msg.ChatID != "555" || msg.UserID != "42" || msg.Text != "查询进度" {
			t.Fatalf("unexpected inbound message: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not called")
	}

	select {
	case offset := <-secondPoll:
		if offset != 101 {
			t.Fatalf("expected next offset 101, got %d", offset)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not issue a second getUpdates")
	}
	stopPoller(t, cancel, done)

	mu.Lock()
	defer mu.Unlock()
	if len(offsets) == 0 || offsets[0] != 0 {
		t.Fatalf("first getUpdates offset should be 0, got %v", offsets)
	}
	if len(replies) != 1 || replies[0] != "当前进度 50%" {
		t.Fatalf("unexpected replies: %v", replies)
	}
}

func TestPollerRejectsUserOutsideWhitelist(t *testing.T) {
	var mu sync.Mutex
	var replies []string
	server := updateOnceServer(t, `{"update_id":7,"message":{"text":"SSIS-001","chat":{"id":555},"from":{"id":42}}}`, &replies, &mu)
	defer server.Close()

	called := make(chan struct{}, 1)
	client := newTestClient(t, server.URL)
	poller := newTestPoller(client, []string{"7"}, handlerFunc(func(context.Context, ports.InboundMessage) error {
		called <- struct{}{}
		return nil
	}))
	cancel, done := runPoller(t, poller)

	got := waitForReplies(t, &mu, &replies, 1)
	stopPoller(t, cancel, done)

	select {
	case <-called:
		t.Fatal("handler must not run for a user outside the whitelist")
	default:
	}
	if len(got) != 1 || got[0] != whitelistRejection {
		t.Fatalf("unexpected replies: %v", got)
	}
}

func TestPollerDispatchesWhitelistedUser(t *testing.T) {
	var mu sync.Mutex
	var replies []string
	server := updateOnceServer(t, `{"update_id":7,"message":{"text":"SSIS-001","chat":{"id":555},"from":{"id":42}}}`, &replies, &mu)
	defer server.Close()

	handled := make(chan struct{}, 1)
	client := newTestClient(t, server.URL)
	poller := newTestPoller(client, []string{"42"}, handlerFunc(func(ctx context.Context, msg ports.InboundMessage) error {
		handled <- struct{}{}
		return client.SendText(ctx, msg.ChatID, "已订阅 SSIS-001")
	}))
	cancel, done := runPoller(t, poller)

	got := waitForReplies(t, &mu, &replies, 1)
	stopPoller(t, cancel, done)

	select {
	case <-handled:
	default:
		t.Fatal("handler was not called for a whitelisted user")
	}
	if len(got) != 1 || got[0] != "已订阅 SSIS-001" {
		t.Fatalf("unexpected replies: %v", got)
	}
}

// TestPollerAnswersCallbackAndDispatchesAction 验证按钮点击先回执再交给处理器，
// 且不会像文本消息那样上报 typing 状态。
func TestPollerAnswersCallbackAndDispatchesAction(t *testing.T) {
	var mu sync.Mutex
	answered, typing := 0, 0
	served := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch methodOf(r.URL.Path) {
		case "getUpdates":
			mu.Lock()
			first := !served
			served = true
			mu.Unlock()
			if first {
				writeJSON(w, `{"ok":true,"result":[{"update_id":30,"callback_query":{"id":"cb-1","data":"bm:pause:SSIS-001","from":{"id":42},"message":{"message_id":77,"chat":{"id":555}}}}]}`)
				return
			}
			writeJSON(w, `{"ok":true,"result":[]}`)
		case "answerCallbackQuery":
			var payload struct {
				CallbackID string `json:"callback_query_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode answerCallbackQuery payload: %v", err)
			}
			if payload.CallbackID != "cb-1" {
				t.Errorf("callback_query_id = %q", payload.CallbackID)
			}
			mu.Lock()
			answered++
			mu.Unlock()
			writeJSON(w, `{"ok":true,"result":true}`)
		case "sendChatAction":
			mu.Lock()
			typing++
			mu.Unlock()
			writeJSON(w, `{"ok":true,"result":true}`)
		default:
			t.Errorf("unexpected method %q", methodOf(r.URL.Path))
			writeJSON(w, `{"ok":true,"result":true}`)
		}
	}))
	defer server.Close()

	handled := make(chan ports.InboundMessage, 1)
	client := newTestClient(t, server.URL)
	poller := newTestPoller(client, nil, handlerFunc(func(_ context.Context, msg ports.InboundMessage) error {
		handled <- msg
		return nil
	}))
	cancel, done := runPoller(t, poller)

	select {
	case msg := <-handled:
		if msg.Channel != ports.ChannelTelegram || msg.ChatID != "555" || msg.UserID != "42" {
			t.Fatalf("unexpected routing fields: %+v", msg)
		}
		if msg.Text != "" {
			t.Fatalf("按钮点击不应携带文本: %q", msg.Text)
		}
		if msg.Action == nil || msg.Action.Data != "bm:pause:SSIS-001" || msg.Action.MessageID != "77" || msg.Action.CallbackID != "cb-1" {
			t.Fatalf("unexpected action: %+v", msg.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not called for a callback query")
	}
	stopPoller(t, cancel, done)

	mu.Lock()
	defer mu.Unlock()
	if answered != 1 {
		t.Fatalf("answerCallbackQuery 次数=%d，期望 1", answered)
	}
	if typing != 0 {
		t.Fatalf("按钮点击不应上报 typing，实际 %d 次", typing)
	}
}

func TestPollerRetriesAfterGetUpdatesError(t *testing.T) {
	var mu sync.Mutex
	var replies []string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch methodOf(r.URL.Path) {
		case "getUpdates":
			mu.Lock()
			index := calls
			calls++
			mu.Unlock()
			switch index {
			case 0:
				w.WriteHeader(http.StatusInternalServerError)
			case 1:
				writeJSON(w, `{"ok":true,"result":[{"update_id":20,"message":{"text":"/version","chat":{"id":555},"from":{"id":42}}}]}`)
			default:
				writeJSON(w, `{"ok":true,"result":[]}`)
			}
		case "sendChatAction":
			writeJSON(w, `{"ok":true,"result":true}`)
		case "sendMessage":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendMessage payload: %v", err)
			}
			mu.Lock()
			replies = append(replies, payload.Text)
			mu.Unlock()
			writeJSON(w, `{"ok":true,"result":{"message_id":1}}`)
		default:
			t.Errorf("unexpected method %q", methodOf(r.URL.Path))
			writeJSON(w, `{"ok":true,"result":true}`)
		}
	}))
	defer server.Close()

	handled := make(chan struct{}, 1)
	client := newTestClient(t, server.URL)
	poller := newTestPoller(client, nil, handlerFunc(func(ctx context.Context, msg ports.InboundMessage) error {
		handled <- struct{}{}
		return client.SendText(ctx, msg.ChatID, "当前版本：dev")
	}))
	cancel, done := runPoller(t, poller)

	got := waitForReplies(t, &mu, &replies, 1)
	stopPoller(t, cancel, done)

	select {
	case <-handled:
	default:
		t.Fatal("poller did not dispatch after retrying a failed getUpdates")
	}
	if len(got) != 1 || got[0] != "当前版本：dev" {
		t.Fatalf("unexpected replies: %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatalf("expected at least 2 getUpdates calls, got %d", calls)
	}
}

// TestPollerWritesStructuredLogs 验证轮询日志走注入的结构化日志器，与管理页日志保持同一种输出格式。
func TestPollerWritesStructuredLogs(t *testing.T) {
	var mu sync.Mutex
	var replies []string
	server := updateOnceServer(t, `{"update_id":9,"message":{"text":"/start","chat":{"id":42},"from":{"id":7}}}`, &replies, &mu)
	defer server.Close()

	client := NewClient("TEST-TOKEN", "", false, nil)
	client.baseURL = server.URL

	var buffer syncBuffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil)).With("category", string(logging.CategoryNotification))
	poller := NewPoller(client, nil, handlerFunc(func(context.Context, ports.InboundMessage) error {
		return errors.New("渠道不可用")
	}), logger)
	poller.backoff = 5 * time.Millisecond

	cancel, done := runPoller(t, poller)
	waitForLog(t, &buffer, "telegram 消息处理失败")
	stopPoller(t, cancel, done)

	var delivered map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("轮询日志不是结构化 JSON: %q", line)
		}
		if record["msg"] == "telegram 消息处理失败" {
			delivered = record
		}
	}
	if delivered == nil {
		t.Fatalf("未记录消息处理失败: %q", buffer.String())
	}
	if delivered["category"] != string(logging.CategoryNotification) {
		t.Fatalf("日志缺少通知分类: %v", delivered)
	}
	if delivered["level"] != "ERROR" {
		t.Fatalf("日志级别不符: %v", delivered)
	}
	if delivered["update_id"] != float64(9) {
		t.Fatalf("日志缺少 update_id: %v", delivered)
	}
}
