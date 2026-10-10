package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"bytemuse/backend/internal/ports"
)

// newTestClient 构造指向 httptest 地址的客户端，测试绝不访问真实外网。
func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client := NewClient("TEST-TOKEN", "", false, nil)
	client.baseURL = strings.TrimRight(baseURL, "/")
	return client
}

// methodOf 从 /bot<token>/<method> 路径中取出 method 名。
func methodOf(path string) string {
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}

// writeJSON 返回一段固定 JSON 响应。
func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func TestSendTextSplitsLongMessageWithoutBreakingRunes(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if method := methodOf(r.URL.Path); method != "sendMessage" {
			t.Errorf("unexpected method %q", method)
		}
		var payload struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.ChatID != "1001" {
			t.Errorf("unexpected chat_id %q", payload.ChatID)
		}
		mu.Lock()
		texts = append(texts, payload.Text)
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	original := strings.Repeat("番号", 3000) // 6000 个 rune，必须切分成两片
	if err := client.SendText(context.Background(), "1001", original); err != nil {
		t.Fatalf("SendText returned error: %v", err)
	}

	mu.Lock()
	chunks := append([]string(nil), texts...)
	mu.Unlock()
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	for index, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %d is not valid UTF-8", index)
		}
		if count := utf8.RuneCountInString(chunk); count > maxMessageRunes {
			t.Fatalf("chunk %d has %d runes, over the %d limit", index, count, maxMessageRunes)
		}
		if strings.ContainsRune(chunk, utf8.RuneError) {
			t.Fatalf("chunk %d contains a replacement rune, meaning a character was cut", index)
		}
	}
	if joined := strings.Join(chunks, ""); joined != original {
		t.Fatalf("joined chunks do not match the original text")
	}
}

func TestGetUpdatesParsesMessagesAndSendsProtocolPayload(t *testing.T) {
	var mu sync.Mutex
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if method := methodOf(r.URL.Path); method != "getUpdates" {
			t.Errorf("unexpected method %q", method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		mu.Lock()
		payload = body
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":[
			{"update_id":11,"message":{"text":"你好","chat":{"id":555},"from":{"id":42}}},
			{"update_id":12,"message":{"text":"   ","chat":{"id":555},"from":{"id":42}}},
			{"update_id":13,"message":{"chat":{"id":555},"from":{"id":42}}},
			{"update_id":14,"edited_message":{"text":"编辑","chat":{"id":555}}}
		]}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	updates, err := client.GetUpdates(context.Background(), 7, 5*time.Second)
	if err != nil {
		t.Fatalf("GetUpdates returned error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 usable update, got %d", len(updates))
	}
	got := updates[0]
	if got.UpdateID != 11 || got.ChatID != "555" || got.UserID != "42" || got.Text != "你好" {
		t.Fatalf("unexpected update: %+v", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if offset, _ := payload["offset"].(float64); offset != 7 {
		t.Fatalf("offset payload = %v", payload["offset"])
	}
	if timeout, _ := payload["timeout"].(float64); timeout != 5 {
		t.Fatalf("timeout payload = %v", payload["timeout"])
	}
	allowed, _ := payload["allowed_updates"].([]any)
	if len(allowed) != 2 || allowed[0] != "message" || allowed[1] != "callback_query" {
		t.Fatalf("allowed_updates payload = %v", payload["allowed_updates"])
	}
}

func TestSetMyCommandsRegistersCommandMenu(t *testing.T) {
	var mu sync.Mutex
	var payload struct {
		Commands []struct {
			Command     string `json:"command"`
			Description string `json:"description"`
		} `json:"commands"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if method := methodOf(r.URL.Path); method != "setMyCommands" {
			t.Errorf("unexpected method %q", method)
		}
		var body struct {
			Commands []struct {
				Command     string `json:"command"`
				Description string `json:"description"`
			} `json:"commands"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		mu.Lock()
		payload = body
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":true}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	commands := []ports.BotCommand{
		{Command: "start", Description: "欢迎与用法"},
		{Command: "  ", Description: "应被跳过"},
	}
	if err := client.SetMyCommands(context.Background(), commands); err != nil {
		t.Fatalf("SetMyCommands returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payload.Commands) != 1 {
		t.Fatalf("expected 1 registered command, got %d", len(payload.Commands))
	}
	if payload.Commands[0].Command != "start" || payload.Commands[0].Description != "欢迎与用法" {
		t.Fatalf("unexpected command payload: %+v", payload.Commands[0])
	}
}

func TestSendPhotoFallsBackToText(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var texts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := methodOf(r.URL.Path)
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		switch method {
		case "sendPhoto":
			writeJSON(w, `{"ok":false,"description":"PHOTO_INVALID"} `)
		case "sendMessage":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode payload: %v", err)
			}
			mu.Lock()
			texts = append(texts, payload.Text)
			mu.Unlock()
			writeJSON(w, `{"ok":true,"result":{"message_id":1}}`)
		default:
			t.Errorf("unexpected method %q", method)
			writeJSON(w, `{"ok":true,"result":true}`)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	if err := client.SendPhoto(context.Background(), "9", "", "标题", "正文"); err != nil {
		t.Fatalf("SendPhoto with empty URL returned error: %v", err)
	}
	if err := client.SendPhoto(context.Background(), "9", "https://example.com/a.jpg", "标题", "正文"); err != nil {
		t.Fatalf("SendPhoto fallback returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 2 {
		t.Fatalf("expected 2 text messages, got %d", len(texts))
	}
	for _, text := range texts {
		if text != "标题\n正文" {
			t.Fatalf("unexpected caption %q", text)
		}
	}
	photoAttempts := 0
	for _, method := range methods {
		if method == "sendPhoto" {
			photoAttempts++
		}
	}
	if photoAttempts != 1 {
		t.Fatalf("expected exactly 1 sendPhoto attempt, got %d", photoAttempts)
	}
}

// TestSendNotificationInlineCopy 验证文本、图片说明和图片失败回退均使用 code 实体而不是按钮。
func TestSendNotificationInlineCopy(t *testing.T) {
	for _, mode := range []string{"text", "photo", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			fixture := &cardFixture{}
			server := fixture.server(t, func(method string) string {
				if mode == "fallback" && method == "sendPhoto" {
					return "{\"ok\":false}"
				}
				return ""
			})
			defer server.Close()
			client := newTestClient(t, server.URL)
			client.spoiler = true
			link := "magnet:?xt=urn:btih:abc&dn=<例>" + strings.Repeat("x", 257)
			body := "状态: 🎬完成\n下载链接: " + link + "\n标题: 测试\n描述: EXAMPLE-001"
			photo := ""
			if mode != "text" {
				photo = "https://example.com/a.jpg"
			}
			fields := []ports.CopyTextField{{Label: "番号", Text: "EXAMPLE-001"}, {Label: "下载链接", Text: link}}
			if err := client.SendNotification(context.Background(), "1001", "番号: EXAMPLE-001", body, photo, fields); err != nil {
				t.Fatal(err)
			}
			calls := fixture.snapshot()
			wantCalls := 1
			if mode == "fallback" {
				wantCalls = 2
			}
			if len(calls) != wantCalls {
				t.Fatalf("请求数量: %d", len(calls))
			}
			for _, call := range calls {
				key, entityKey := "text", "entities"
				if call.method == "sendPhoto" {
					key, entityKey = "caption", "caption_entities"
					if call.body["has_spoiler"] != true {
						t.Fatal("封面防剧透丢失")
					}
				}
				if call.body[key] != "番号: EXAMPLE-001\n"+body {
					t.Fatalf("正文发生变化: %#v", call.body)
				}
				if _, exists := call.body["reply_markup"]; exists {
					t.Fatal("通知不应包含复制按钮")
				}
				entities, ok := call.body[entityKey].([]any)
				if !ok || len(entities) != 2 {
					t.Fatalf("缺少两个行内 code 实体: %#v", call.body)
				}
				for index, raw := range entities {
					entity := raw.(map[string]any)
					encoded := utf16.Encode([]rune(call.body[key].(string)))
					offset, length := int(entity["offset"].(float64)), int(entity["length"].(float64))
					if entity["type"] != "code" || string(utf16.Decode(encoded[offset:offset+length])) != fields[index].Text {
						t.Fatalf("实体范围错误: %#v", entity)
					}
				}
			}
		})
	}
}

// TestSendNotificationPreservesLongText 验证超长图文通知降级时保留全文并按平台上限分片。
func TestSendNotificationPreservesLongText(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()
	client := newTestClient(t, server.URL)
	body := strings.Repeat("正文", maxMessageRunes)
	if err := client.SendNotification(context.Background(), "1001", "番号: EXAMPLE-001", body, "https://example.com/a.jpg", nil); err != nil {
		t.Fatal(err)
	}
	var combined strings.Builder
	for _, call := range fixture.snapshot() {
		if call.method != "sendMessage" {
			t.Fatalf("超长通知不应截断为图片说明: %s", call.method)
		}
		chunk := call.body["text"].(string)
		if utf8.RuneCountInString(chunk) > maxMessageRunes {
			t.Fatal("消息超过平台长度上限")
		}
		combined.WriteString(chunk)
	}
	if combined.String() != "番号: EXAMPLE-001\n"+body {
		t.Fatal("通知全文丢失")
	}
}

// TestSendNotificationChunkEntities 验证长链接整体移入下一片，超平台上限的字段分片后仍保留完整内容和正确实体。
func TestSendNotificationChunkEntities(t *testing.T) {
	for _, size := range []int{300, 5000} {
		fixture := &cardFixture{}
		server := fixture.server(t, nil)
		defer server.Close()
		client := newTestClient(t, server.URL)
		link := "https://example.com/" + strings.Repeat("🎬", size)
		body := strings.Repeat("字", 4000) + "\n下载链接: " + link + "\n描述: 完成"
		fields := []ports.CopyTextField{{Label: "下载链接", Text: link}}
		if err := client.SendNotification(context.Background(), "1001", "", body, "", fields); err != nil {
			t.Fatal(err)
		}
		var combined, copied strings.Builder
		entityCount := 0
		for _, call := range fixture.snapshot() {
			chunk := call.body["text"].(string)
			encoded := utf16.Encode([]rune(chunk))
			if len(encoded) > maxMessageRunes || strings.ContainsRune(chunk, '�') {
				t.Fatal("消息分片超限或破坏表情")
			}
			combined.WriteString(chunk)
			if _, ok := call.body["reply_markup"]; ok {
				t.Fatal("不应发送复制按钮")
			}
			entities, _ := call.body["entities"].([]any)
			for _, raw := range entities {
				entity := raw.(map[string]any)
				offset, length := int(entity["offset"].(float64)), int(entity["length"].(float64))
				if entity["type"] != "code" || offset < 0 || offset+length > len(encoded) {
					t.Fatalf("实体越界: %#v", entity)
				}
				copied.WriteString(string(utf16.Decode(encoded[offset : offset+length])))
				entityCount++
			}
		}
		if combined.String() != body || copied.String() != link {
			t.Fatal("消息或可复制内容丢失")
		}
		if size == 300 && entityCount != 1 {
			t.Fatal("可放入单条消息的链接不应拆分")
		}
	}
}

func TestSendNotificationOmitsEmptyCopyFields(t *testing.T) {
	fixture := &cardFixture{}
	server := fixture.server(t, nil)
	defer server.Close()
	client := newTestClient(t, server.URL)
	fields := []ports.CopyTextField{{Label: " ", Text: "EXAMPLE-001"}, {Label: "番号", Text: ""}, {Label: "番号", Text: "不匹配"}}
	if err := client.SendNotification(context.Background(), "1001", "番号: 暂无", "描述: EXAMPLE-001", "", fields); err != nil {
		t.Fatal(err)
	}
	calls := fixture.snapshot()
	if len(calls) != 1 {
		t.Fatalf("请求数量: %d", len(calls))
	}
	for _, key := range []string{"reply_markup", "entities", "parse_mode"} {
		if _, ok := calls[0].body[key]; ok {
			t.Fatalf("无有效可复制字段不应发送 %s", key)
		}
	}
}

// TestSendPhotoAppliesSpoilerSetting 验证「图片防剧透」只影响图文消息：开启时附带 has_spoiler，
// 关闭时不带该字段，两种情况下都不影响文本降级路径。
func TestSendPhotoAppliesSpoilerSetting(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		var mu sync.Mutex
		var spoiler any
		var seen bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if method := methodOf(r.URL.Path); method != "sendPhoto" {
				t.Errorf("unexpected method %q", method)
				writeJSON(w, `{"ok":true,"result":true}`)
				return
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode payload: %v", err)
			}
			mu.Lock()
			spoiler, seen = payload["has_spoiler"], true
			mu.Unlock()
			writeJSON(w, `{"ok":true,"result":{"message_id":1}}`)
		}))

		client := newTestClient(t, server.URL)
		client.spoiler = enabled
		if err := client.SendPhoto(context.Background(), "9", "https://example.com/a.jpg", "标题", "正文"); err != nil {
			t.Fatalf("spoiler=%v 时 SendPhoto 返回错误: %v", enabled, err)
		}
		server.Close()

		mu.Lock()
		got, ok := spoiler, seen
		mu.Unlock()
		if !ok {
			t.Fatalf("spoiler=%v 时未发出 sendPhoto 请求", enabled)
		}
		if enabled {
			if got != true {
				t.Fatalf("防剧透开启时 has_spoiler = %#v，期望 true", got)
			}
			continue
		}
		if got != nil {
			t.Fatalf("防剧透关闭时不应携带 has_spoiler，实际 %#v", got)
		}
	}
}
