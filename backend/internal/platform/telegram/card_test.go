package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"bytemuse/backend/internal/ports"
)

// apiCall 记录一次 Bot API 调用的方法名与请求体。
type apiCall struct {
	method string
	body   map[string]any
}

// cardFixture 是卡片类测试的假 Bot API：记录全部调用并按方法返回预置响应。
type cardFixture struct {
	mu    sync.Mutex
	calls []apiCall
}

// server 启动假服务；respond 返回该方法要回写的 JSON，返回空串时使用成功响应。
func (f *cardFixture) server(t *testing.T, respond func(method string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := methodOf(r.URL.Path)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s payload: %v", method, err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, apiCall{method: method, body: body})
		f.mu.Unlock()
		if respond != nil {
			if custom := respond(method); custom != "" {
				writeJSON(w, custom)
				return
			}
		}
		writeJSON(w, `{"ok":true,"result":{"message_id":9}}`)
	}))
}

func (f *cardFixture) snapshot() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}

// inlineRows 从请求体中取出内联键盘的行结构。
func inlineRows(t *testing.T, body map[string]any) []any {
	t.Helper()
	markup, ok := body["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("请求体缺少 reply_markup：%#v", body)
	}
	rows, ok := markup["inline_keyboard"].([]any)
	if !ok {
		t.Fatalf("reply_markup 缺少 inline_keyboard：%#v", markup)
	}
	return rows
}

func TestSendCardSendsPhotoCaptionAndInlineKeyboard(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()

	client := newTestClient(t, server.URL)
	card := ports.OutboundCard{
		PhotoURL: "https://example.test/SSIS-001.jpg",
		Title:    "SSIS-001",
		Text:     "中文标题\n状态：未订阅",
		Buttons: []ports.ActionButton{
			{Label: "订阅", Data: "bm:sub:SSIS-001"},
			{Label: "取消订阅", Data: "bm:unsub:SSIS-001"},
			{Label: "暂停", Data: "bm:pause:SSIS-001"},
		},
	}
	if err := client.SendCard(context.Background(), "555", card); err != nil {
		t.Fatalf("SendCard returned error: %v", err)
	}

	calls := fixture.snapshot()
	if len(calls) != 1 || calls[0].method != "sendPhoto" {
		t.Fatalf("期望只调用 sendPhoto，实际 %+v", calls)
	}
	body := calls[0].body
	if body["chat_id"] != "555" || body["photo"] != card.PhotoURL {
		t.Fatalf("sendPhoto 请求体=%#v", body)
	}
	if caption, _ := body["caption"].(string); caption != "SSIS-001\n中文标题\n状态：未订阅" {
		t.Fatalf("caption=%q", caption)
	}
	rows := inlineRows(t, body)
	if len(rows) != 2 {
		t.Fatalf("每行两个按钮应排成两行，实际 %d 行", len(rows))
	}
	first, _ := rows[0].([]any)
	if len(first) != 2 {
		t.Fatalf("第一行按钮数=%d，期望 2", len(first))
	}
	button, _ := first[0].(map[string]any)
	if button["text"] != "订阅" || button["callback_data"] != "bm:sub:SSIS-001" {
		t.Fatalf("第一个按钮=%#v", button)
	}
	last, _ := rows[1].([]any)
	if len(last) != 1 {
		t.Fatalf("奇数个按钮时最后一个应独占一行，实际 %d 个", len(last))
	}
}

// TestSendCardAppliesSpoilerSetting 验证「图片防剧透」对番号卡片同样生效：
// 卡片封面与推送封面共用同一条规则，开启时附带 has_spoiler，关闭时不带该字段。
func TestSendCardAppliesSpoilerSetting(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		fixture := &cardFixture{}
		server := fixture.server(t, nil)

		client := newTestClient(t, server.URL)
		client.spoiler = enabled
		card := ports.OutboundCard{
			PhotoURL: "https://example.test/SSIS-001.jpg",
			Title:    "SSIS-001",
			Text:     "状态：未订阅",
			Buttons:  []ports.ActionButton{{Label: "订阅", Data: "bm:sub:SSIS-001"}},
		}
		if err := client.SendCard(context.Background(), "555", card); err != nil {
			server.Close()
			t.Fatalf("spoiler=%v 时 SendCard 返回错误: %v", enabled, err)
		}
		calls := fixture.snapshot()
		server.Close()

		if len(calls) != 1 || calls[0].method != "sendPhoto" {
			t.Fatalf("spoiler=%v 时期望只调用 sendPhoto，实际 %+v", enabled, calls)
		}
		got := calls[0].body["has_spoiler"]
		if enabled {
			if got != true {
				t.Fatalf("防剧透开启时卡片封面应打码，has_spoiler=%#v", got)
			}
			continue
		}
		if got != nil {
			t.Fatalf("防剧透关闭时卡片不应携带 has_spoiler，实际 %#v", got)
		}
	}
}

func TestSendCardFallsBackToTextMessageWhenPhotoRejected(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, func(method string) string {
		if method == "sendPhoto" {
			return `{"ok":false,"description":"wrong file identifier"}`
		}
		return ""
	})
	defer server.Close()

	client := newTestClient(t, server.URL)
	card := ports.OutboundCard{
		PhotoURL: "https://example.test/bad.jpg",
		Title:    "SSIS-001",
		Text:     "状态：未订阅",
		Buttons:  []ports.ActionButton{{Label: "订阅", Data: "bm:sub:SSIS-001"}},
	}
	if err := client.SendCard(context.Background(), "555", card); err != nil {
		t.Fatalf("SendCard returned error: %v", err)
	}

	calls := fixture.snapshot()
	if len(calls) != 2 || calls[0].method != "sendPhoto" || calls[1].method != "sendMessage" {
		t.Fatalf("期望 sendPhoto 失败后降级 sendMessage，实际 %+v", calls)
	}
	body := calls[1].body
	if body["text"] != "SSIS-001\n状态：未订阅" {
		t.Fatalf("降级文本=%q", body["text"])
	}
	if rows := inlineRows(t, body); len(rows) != 1 {
		t.Fatalf("降级后仍必须保留按钮，实际 %d 行", len(rows))
	}
}

func TestSendCardRequiresButtons(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()

	client := newTestClient(t, server.URL)
	if err := client.SendCard(context.Background(), "555", ports.OutboundCard{Title: "SSIS-001"}); err == nil {
		t.Fatal("缺少按钮时必须返回错误")
	}
	if calls := fixture.snapshot(); len(calls) != 0 {
		t.Fatalf("缺少按钮时不应发起请求，实际 %+v", calls)
	}
}

func TestSendCardTruncatesCaptionToPlatformLimit(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()

	client := newTestClient(t, server.URL)
	card := ports.OutboundCard{
		PhotoURL: "https://example.test/SSIS-001.jpg",
		Title:    "SSIS-001",
		Text:     strings.Repeat("番", maxCaptionRunes+50),
		Buttons:  []ports.ActionButton{{Label: "订阅", Data: "bm:sub:SSIS-001"}},
	}
	if err := client.SendCard(context.Background(), "555", card); err != nil {
		t.Fatalf("SendCard returned error: %v", err)
	}

	calls := fixture.snapshot()
	caption, _ := calls[0].body["caption"].(string)
	if count := utf8.RuneCountInString(caption); count > maxCaptionRunes {
		t.Fatalf("caption 长度=%d，超过上限 %d", count, maxCaptionRunes)
	}
	if !strings.HasSuffix(caption, "…") {
		t.Fatalf("超长 caption 应以省略号结尾：%q", caption)
	}
}

func TestReplaceButtonsUpdatesInlineKeyboard(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()

	client := newTestClient(t, server.URL)
	buttons := []ports.ActionButton{{Label: "继续", Data: "bm:resume:SSIS-001"}}
	if err := client.ReplaceButtons(context.Background(), "555", "9", buttons); err != nil {
		t.Fatalf("ReplaceButtons returned error: %v", err)
	}

	calls := fixture.snapshot()
	if len(calls) != 1 || calls[0].method != "editMessageReplyMarkup" {
		t.Fatalf("期望调用 editMessageReplyMarkup，实际 %+v", calls)
	}
	body := calls[0].body
	if body["chat_id"] != "555" || body["message_id"] != float64(9) {
		t.Fatalf("请求体=%#v", body)
	}
	rows := inlineRows(t, body)
	if len(rows) != 1 {
		t.Fatalf("行数=%d，期望 1", len(rows))
	}
	row, _ := rows[0].([]any)
	button, _ := row[0].(map[string]any)
	if button["text"] != "继续" || button["callback_data"] != "bm:resume:SSIS-001" {
		t.Fatalf("按钮=%#v", button)
	}
}

func TestReplaceButtonsRemovesKeyboardWhenEmpty(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()

	client := newTestClient(t, server.URL)
	if err := client.ReplaceButtons(context.Background(), "555", "9", nil); err != nil {
		t.Fatalf("ReplaceButtons returned error: %v", err)
	}
	calls := fixture.snapshot()
	if rows := inlineRows(t, calls[0].body); len(rows) != 0 {
		t.Fatalf("空按钮应移除键盘，实际 %d 行", len(rows))
	}
}

func TestReplaceButtonsRejectsInvalidMessageID(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()

	client := newTestClient(t, server.URL)
	if err := client.ReplaceButtons(context.Background(), "555", "abc", nil); err == nil {
		t.Fatal("非数字消息 ID 必须返回错误")
	}
	if calls := fixture.snapshot(); len(calls) != 0 {
		t.Fatalf("无效消息 ID 不应发起请求，实际 %+v", calls)
	}
}

func TestAnswerCallbackSendsReceipt(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()

	client := newTestClient(t, server.URL)
	if err := client.AnswerCallback(context.Background(), "cb-1", ""); err != nil {
		t.Fatalf("AnswerCallback returned error: %v", err)
	}
	if err := client.AnswerCallback(context.Background(), "cb-2", "已暂停"); err != nil {
		t.Fatalf("AnswerCallback returned error: %v", err)
	}
	if err := client.AnswerCallback(context.Background(), " ", ""); err == nil {
		t.Fatal("缺少 callback id 时必须返回错误")
	}

	calls := fixture.snapshot()
	if len(calls) != 2 {
		t.Fatalf("期望两次回执，实际 %d 次", len(calls))
	}
	if calls[0].method != "answerCallbackQuery" || calls[0].body["callback_query_id"] != "cb-1" {
		t.Fatalf("第一次回执=%+v", calls[0])
	}
	if _, exists := calls[0].body["text"]; exists {
		t.Fatalf("空提示不应携带 text 字段：%#v", calls[0].body)
	}
	if calls[1].body["text"] != "已暂停" {
		t.Fatalf("第二次回执=%#v", calls[1].body)
	}
}

func TestDecodeUpdatesParsesCallbackQuery(t *testing.T) {
	raw := json.RawMessage(`[
		{"update_id":5,"callback_query":{"id":"cb-1","data":"bm:pause:SSIS-001","from":{"id":42},"message":{"message_id":77,"chat":{"id":555}}}},
		{"update_id":6,"callback_query":{"id":"cb-2","data":"bm:pause:SSIS-001","from":{"id":42}}},
		{"update_id":7,"callback_query":{"id":"cb-3","data":"","from":{"id":42},"message":{"message_id":78,"chat":{"id":555}}}},
		{"update_id":8,"message":{"text":"SSIS-001","chat":{"id":555},"from":{"id":42}}}
	]`)
	updates, err := decodeUpdates(raw)
	if err != nil {
		t.Fatalf("decodeUpdates returned error: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("期望 2 条可处理更新，实际 %d：%+v", len(updates), updates)
	}
	action := updates[0]
	if action.Text != "" {
		t.Fatalf("按钮点击不应携带文本：%q", action.Text)
	}
	if action.ChatID != "555" || action.UserID != "42" || action.UpdateID != 5 {
		t.Fatalf("路由字段不符：%+v", action)
	}
	if action.Action == nil || action.Action.Data != "bm:pause:SSIS-001" || action.Action.MessageID != "77" || action.Action.CallbackID != "cb-1" {
		t.Fatalf("按钮动作不符：%+v", action.Action)
	}
	if updates[1].Text != "SSIS-001" || updates[1].Action != nil {
		t.Fatalf("文本消息解析不符：%+v", updates[1])
	}
}
