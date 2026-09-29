package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// deeplxTestService 构造 DeepLX 引擎服务，地址使用测试服务器。
func deeplxTestService(t *testing.T, rawURL string) *TranslationService {
	t.Helper()
	service, err := NewTranslationService(TranslationConfig{Engine: TranslationEngineDeepLX, DeepLXURL: rawURL}, nil)
	if err != nil {
		t.Fatalf("构造 DeepLX 翻译服务失败: %v", err)
	}
	return service
}

func TestDeepLXUsesTranslatePathAndUppercaseTarget(t *testing.T) {
	var gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"code":200,"data":"初次拍摄"}`)
	}))
	defer server.Close()
	service := deeplxTestService(t, server.URL)
	value, err := service.Translate(context.Background(), TranslationRequest{Text: "初撮り", TargetLanguage: "zh-CN"})
	if err != nil {
		t.Fatalf("DeepLX 翻译失败: %v", err)
	}
	if value != "初次拍摄" {
		t.Fatalf("译文 = %q，期望 初次拍摄", value)
	}
	if gotPath != "/translate" {
		t.Fatalf("请求路径 = %q，期望 /translate", gotPath)
	}
	var payload map[string]any
	if json.Unmarshal(gotBody, &payload) != nil {
		t.Fatalf("请求体不是合法 JSON: %s", gotBody)
	}
	if payload["source_lang"] != "auto" || payload["target_lang"] != "ZH" || payload["text"] != "初撮り" {
		t.Fatalf("请求参数不符合 DeepLX 协议: %s", gotBody)
	}
}

func TestDeepLXAcceptsFullTranslateURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		io.WriteString(w, `{"code":200,"data":"你好"}`)
	}))
	defer server.Close()
	service := deeplxTestService(t, server.URL+"/translate")
	if _, err := service.Translate(context.Background(), TranslationRequest{Text: "hello", TargetLanguage: "zh-CN"}); err != nil {
		t.Fatalf("DeepLX 翻译失败: %v", err)
	}
	if gotPath != "/translate" {
		t.Fatalf("请求路径 = %q，填写完整地址时不应重复拼接", gotPath)
	}
}

func TestDeepLXReportsBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":429,"message":"Too many requests"}`)
	}))
	defer server.Close()
	service := deeplxTestService(t, server.URL)
	_, err := service.Translate(context.Background(), TranslationRequest{Text: "hello", TargetLanguage: "zh-CN"})
	if err == nil {
		t.Fatal("DeepLX 返回非 200 业务码时必须报错")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "Too many requests") {
		t.Fatalf("错误信息应包含业务码与原因，实际: %v", err)
	}
}

func TestDeepLXEndpointAndLanguage(t *testing.T) {
	endpoints := map[string]string{
		"http://127.0.0.1:1188":           "http://127.0.0.1:1188/translate",
		"http://127.0.0.1:1188/":          "http://127.0.0.1:1188/translate",
		"http://127.0.0.1:1188/translate": "http://127.0.0.1:1188/translate",
		" http://127.0.0.1:1188 ":         "http://127.0.0.1:1188/translate",
	}
	for raw, want := range endpoints {
		if got := deeplxEndpoint(raw); got != want {
			t.Fatalf("deeplxEndpoint(%q) = %q，期望 %q", raw, got, want)
		}
	}
	languages := map[string]string{"": "ZH", "zh-CN": "ZH", "en-US": "EN", "ja": "JA"}
	for target, want := range languages {
		if got := deeplxLanguage(target); got != want {
			t.Fatalf("deeplxLanguage(%q) = %q，期望 %q", target, got, want)
		}
	}
}
