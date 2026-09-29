package wechat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// fakeWeChat 是本地企业微信替身，只记录请求次数与请求体，不访问外网。
type fakeWeChat struct {
	mu           sync.Mutex
	tokenCalls   int
	sendCalls    int
	payloads     []map[string]any
	tokenErrCode int
	sendErrCodes []int
}

func (f *fakeWeChat) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			f.mu.Lock()
			f.tokenCalls++
			code := f.tokenErrCode
			f.mu.Unlock()
			if code != 0 {
				writeJSON(w, map[string]any{"errcode": code, "errmsg": "invalid corpid"})
				return
			}
			writeJSON(w, map[string]any{"errcode": 0, "errmsg": "ok", "access_token": "token-value", "expires_in": 7200})
		case "/cgi-bin/message/send":
			raw, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)
			f.mu.Lock()
			index := f.sendCalls
			f.sendCalls++
			f.payloads = append(f.payloads, payload)
			code := 0
			if index < len(f.sendErrCodes) {
				code = f.sendErrCodes[index]
			}
			f.mu.Unlock()
			if code != 0 {
				writeJSON(w, map[string]any{"errcode": code, "errmsg": "send failed"})
				return
			}
			writeJSON(w, map[string]any{"errcode": 0, "errmsg": "ok"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (f *fakeWeChat) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls, f.sendCalls
}

func (f *fakeWeChat) snapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any{}, f.payloads...)
}

func newTestClient(t *testing.T, fake *fakeWeChat, photoURL string, banner bool) *Client {
	t.Helper()
	server := httptest.NewServer(fake.handler())
	t.Cleanup(server.Close)
	return NewClient("corp-id", "corp-secret", "1000002", server.URL, photoURL, banner, nil)
}

func textContent(t *testing.T, payload map[string]any) string {
	t.Helper()
	body, ok := payload["text"].(map[string]any)
	if !ok {
		t.Fatalf("请求体缺少 text 字段：%#v", payload)
	}
	content, _ := body["content"].(string)
	return content
}

func TestSendTextCachesAccessToken(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)

	for index := 0; index < 2; index++ {
		if err := client.SendText(context.Background(), "zhangsan", "你好"); err != nil {
			t.Fatalf("第 %d 次发送失败：%v", index+1, err)
		}
	}
	tokenCalls, sendCalls := fake.counts()
	if tokenCalls != 1 {
		t.Fatalf("access_token 应缓存复用，gettoken 调用次数=%d", tokenCalls)
	}
	if sendCalls != 2 {
		t.Fatalf("消息发送次数=%d，期望 2", sendCalls)
	}
	payload := fake.snapshot()[0]
	if payload["touser"] != "zhangsan" || payload["msgtype"] != "text" || payload["agentid"] != "1000002" {
		t.Fatalf("请求体字段不符合契约：%#v", payload)
	}
}

func TestSendTextSplitsLongTextByRune(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)

	text := strings.Repeat("片", 1000)
	if err := client.SendText(context.Background(), "zhangsan", text); err != nil {
		t.Fatalf("发送长文本失败：%v", err)
	}
	_, sendCalls := fake.counts()
	if sendCalls != 2 {
		t.Fatalf("3000 字节文本应分 2 条发送，实际 %d", sendCalls)
	}
	var rebuilt strings.Builder
	for _, payload := range fake.snapshot() {
		content := textContent(t, payload)
		if len(content) > maxTextBytes {
			t.Fatalf("分片长度超出上限：%d", len(content))
		}
		if !utf8.ValidString(content) {
			t.Fatal("分片不是合法 UTF-8，说明切点落在 rune 中间")
		}
		rebuilt.WriteString(content)
	}
	if rebuilt.String() != text {
		t.Fatal("分片拼接结果与原文不一致")
	}
}

func TestSendTextReturnsPlatformError(t *testing.T) {
	fake := &fakeWeChat{sendErrCodes: []int{40013}}
	client := newTestClient(t, fake, "", false)

	err := client.SendText(context.Background(), "zhangsan", "你好")
	if err == nil {
		t.Fatal("errcode 非 0 时必须返回错误")
	}
	if !strings.Contains(err.Error(), "40013") || !strings.Contains(err.Error(), "send failed") {
		t.Fatalf("错误信息缺少平台返回：%v", err)
	}
}

// TestSendTextConcurrentTokenFetch 验证并发首次发送只换取一次 access_token，缓存读写无竞态。
func TestSendTextConcurrentTokenFetch(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)

	const workers = 8
	var waitGroup sync.WaitGroup
	errors := make([]error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(slot int) {
			defer waitGroup.Done()
			errors[slot] = client.SendText(context.Background(), "zhangsan", "你好")
		}(index)
	}
	waitGroup.Wait()
	for index, err := range errors {
		if err != nil {
			t.Fatalf("并发发送第 %d 个失败：%v", index, err)
		}
	}
	tokenCalls, sendCalls := fake.counts()
	if tokenCalls != 1 {
		t.Fatalf("并发首次发送应只换取一次 token，实际 %d 次", tokenCalls)
	}
	if sendCalls != workers {
		t.Fatalf("发送次数=%d，期望 %d", sendCalls, workers)
	}
}

func TestSendTextRefreshesExpiredToken(t *testing.T) {
	fake := &fakeWeChat{sendErrCodes: []int{42001, 0}}
	client := newTestClient(t, fake, "", false)

	if err := client.SendText(context.Background(), "zhangsan", "你好"); err != nil {
		t.Fatalf("凭证刷新后应发送成功：%v", err)
	}
	tokenCalls, sendCalls := fake.counts()
	if tokenCalls != 2 || sendCalls != 2 {
		t.Fatalf("凭证失效应刷新一次并重发，gettoken=%d send=%d", tokenCalls, sendCalls)
	}
}

func TestSendPhotoPictureSelection(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "https://img.example/fixed.png", false)
	if err := client.SendPhoto(context.Background(), "zhangsan", "https://img.example/dynamic.png", "标题", "正文"); err != nil {
		t.Fatalf("发送图文失败：%v", err)
	}
	payload := fake.snapshot()[0]
	if payload["msgtype"] != "news" {
		t.Fatalf("msgtype=%v，期望 news", payload["msgtype"])
	}
	article := newsArticleOf(t, payload)
	if article["picurl"] != "https://img.example/fixed.png" {
		t.Fatalf("banner 关闭时应使用固定配图，实际 %v", article["picurl"])
	}
	// 企业微信图文消息的 title 是必填项，空标题会被平台拒绝，因此标题必须按业务传入值原样下发。
	if article["title"] != "标题" || article["description"] != "正文" {
		t.Fatalf("news.articles[0] 标题/正文 = %v/%v，期望 标题/正文", article["title"], article["description"])
	}

	fakeBanner := &fakeWeChat{}
	bannerClient := newTestClient(t, fakeBanner, "https://img.example/fixed.png", true)
	if err := bannerClient.SendPhoto(context.Background(), "zhangsan", "https://img.example/dynamic.png", "标题", "正文"); err != nil {
		t.Fatalf("发送图文失败：%v", err)
	}
	bannerArticle := newsArticleOf(t, fakeBanner.snapshot()[0])
	if bannerArticle["picurl"] != "https://img.example/dynamic.png" {
		t.Fatalf("banner 开启时应使用本次封面，实际 %v", bannerArticle["picurl"])
	}
}

func newsArticleOf(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	news, ok := payload["news"].(map[string]any)
	if !ok {
		t.Fatalf("请求体缺少 news 字段：%#v", payload)
	}
	articles, ok := news["articles"].([]any)
	if !ok || len(articles) != 1 {
		t.Fatalf("news.articles 结构不符合契约：%#v", news)
	}
	article, ok := articles[0].(map[string]any)
	if !ok {
		t.Fatalf("news.articles[0] 不是对象：%#v", articles[0])
	}
	return article
}

func TestSendPhotoFallsBackToText(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)

	if err := client.SendPhoto(context.Background(), "zhangsan", "", "标题", "正文"); err != nil {
		t.Fatalf("降级发送失败：%v", err)
	}
	payload := fake.snapshot()[0]
	if payload["msgtype"] != "text" {
		t.Fatalf("picurl 为空时应降级为 text，实际 %v", payload["msgtype"])
	}
	if content := textContent(t, payload); content != "标题\n正文" {
		t.Fatalf("降级文本应保留标题，实际 %q", content)
	}
}

func TestSendMethodsRequireConfiguration(t *testing.T) {
	fake := &fakeWeChat{}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := NewClient("", "", "", server.URL, "", false, nil)

	if client.Configured() {
		t.Fatal("缺少配置时 Configured 必须为 false")
	}
	if err := client.SendText(context.Background(), "zhangsan", "你好"); err == nil {
		t.Fatal("未配置时发送必须返回错误")
	}
	if err := client.SendPhoto(context.Background(), "zhangsan", "https://img.example/a.png", "标题", "正文"); err == nil {
		t.Fatal("未配置时发送图文必须返回错误")
	}
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("未配置时健康检查应返回 nil：%v", err)
	}
	tokenCalls, sendCalls := fake.counts()
	if tokenCalls != 0 || sendCalls != 0 {
		t.Fatalf("未配置时不应发起请求，gettoken=%d send=%d", tokenCalls, sendCalls)
	}
}

func TestHealthReportsTokenFailure(t *testing.T) {
	fake := &fakeWeChat{tokenErrCode: 40013}
	client := newTestClient(t, fake, "", false)

	err := client.Health(context.Background())
	if err == nil {
		t.Fatal("gettoken 失败时健康检查必须返回错误")
	}
	if !strings.Contains(err.Error(), "40013") {
		t.Fatalf("健康检查错误缺少平台码：%v", err)
	}
}

func TestClientDoesNotFollowRedirect(t *testing.T) {
	var targetHits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		writeJSON(w, map[string]any{"errcode": 0, "errmsg": "ok"})
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	client := NewClient("corp-id", "corp-secret", "1000002", redirector.URL, "", false, nil)
	if err := client.SendText(context.Background(), "zhangsan", "你好"); err == nil {
		t.Fatal("重定向响应必须视为失败")
	}
	if targetHits != 0 {
		t.Fatalf("不应跟随重定向，目标命中 %d 次", targetHits)
	}
}

func TestNewClientHostDefaults(t *testing.T) {
	client := NewClient("corp-id", "corp-secret", "1000002", "", "", false, nil)
	if client.apiHost != defaultAPIHost {
		t.Fatalf("空 apiHost 应回退官方基址，实际 %q", client.apiHost)
	}
	trimmed := NewClient("corp-id", "corp-secret", "1000002", "https://proxy.example.com/", "", false, nil)
	if trimmed.apiHost != "https://proxy.example.com" {
		t.Fatalf("apiHost 应去掉尾部斜杠，实际 %q", trimmed.apiHost)
	}
}
