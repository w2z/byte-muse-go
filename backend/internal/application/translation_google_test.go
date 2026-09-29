package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// googleTestService 构造 Google 引擎服务，并把两个入口指向测试服务器。
func googleTestService(t *testing.T, key, officialURL, freeURL string) *TranslationService {
	t.Helper()
	service, err := NewTranslationService(TranslationConfig{Engine: TranslationEngineGoogle, GoogleAPIKey: key}, nil)
	if err != nil {
		t.Fatalf("构造 Google 翻译服务失败: %v", err)
	}
	service.googleOfficialURL = officialURL
	service.googleFreeURL = freeURL
	return service
}

func TestNewTranslationServiceGoogleAllowsEmptyKey(t *testing.T) {
	if _, err := NewTranslationService(TranslationConfig{Engine: TranslationEngineGoogle}, nil); err != nil {
		t.Fatalf("Google 引擎不应强制要求 API Key: %v", err)
	}
}

func TestGoogleWithoutKeyUsesFreeEndpoint(t *testing.T) {
	var gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		io.WriteString(w, `[[["初次拍摄","初撮り",null,null,3]],null,"ja"]`)
	}))
	defer server.Close()
	service := googleTestService(t, "", server.URL+"/v2", server.URL+"/single")
	value, err := service.Translate(context.Background(), TranslationRequest{Text: "初撮り"})
	if err != nil {
		t.Fatalf("免密钥翻译失败: %v", err)
	}
	if value != "初次拍摄" {
		t.Fatalf("译文 = %q，期望 初次拍摄", value)
	}
	if gotPath != "/single" {
		t.Fatalf("请求路径 = %q，未配置 key 必须走免密钥接口", gotPath)
	}
	query, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("解析请求参数失败: %v", err)
	}
	if query.Get("client") != googleFreeClient || query.Get("sl") != "auto" || query.Get("tl") != "zh-CN" || query.Get("q") != "初撮り" {
		t.Fatalf("免密钥请求参数不符合预期: %s", gotQuery)
	}
}

func TestGoogleWithKeyUsesOfficialEndpoint(t *testing.T) {
	var gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"data":{"translations":[{"translatedText":"初次拍摄 &#39;特别篇&#39;"}]}}`)
	}))
	defer server.Close()
	service := googleTestService(t, "test-key", server.URL+"/v2", server.URL+"/single")
	value, err := service.Translate(context.Background(), TranslationRequest{Text: "初撮り", TargetLanguage: "zh-CN"})
	if err != nil {
		t.Fatalf("官方接口翻译失败: %v", err)
	}
	if value != "初次拍摄 '特别篇'" {
		t.Fatalf("译文 = %q，期望还原 HTML 实体", value)
	}
	if gotPath != "/v2" {
		t.Fatalf("请求路径 = %q，配置了 key 必须走官方接口", gotPath)
	}
	var payload map[string]any
	if json.Unmarshal(gotBody, &payload) != nil || payload["key"] != "test-key" || payload["target"] != "zh-CN" {
		t.Fatalf("官方请求必须携带 key 与目标语言，实际请求体: %s", gotBody)
	}
}

func TestParseGoogleFreeTranslation(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"单段", `[[["初次拍摄","初撮り",null,null,3]],null,"ja"]`, "初次拍摄", false},
		{"多段拼接", `[[["上集","前編",null],["下集","後編",null]],null,"ja"]`, "上集下集", false},
		{"译文为空", `[[["","初撮り",null]],null,"ja"]`, "", true},
		{"非法 JSON", `not json`, "", true},
		{"空数组", `[]`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGoogleFreeTranslation([]byte(tc.body))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v，期望出错 = %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("译文 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestGoogleFreeRetriesOnRateLimit(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `[[["初次拍摄","初撮り",null]],null,"ja"]`)
	}))
	defer server.Close()
	service := googleTestService(t, "", server.URL+"/v2", server.URL+"/single")
	value, err := service.Translate(context.Background(), TranslationRequest{Text: "初撮り", TargetLanguage: "zh-CN"})
	if err != nil {
		t.Fatalf("限流重试后应成功: %v", err)
	}
	if value != "初次拍摄" {
		t.Fatalf("译文 = %q，期望 初次拍摄", value)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("请求次数 = %d，期望 3", got)
	}
}

func TestGoogleFreeDoesNotRetryClientError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	service := googleTestService(t, "", server.URL+"/v2", server.URL+"/single")
	if _, err := service.Translate(context.Background(), TranslationRequest{Text: "初撮り", TargetLanguage: "zh-CN"}); err == nil {
		t.Fatal("HTTP 400 必须返回错误")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("HTTP 400 不应重试，请求次数 = %d", got)
	}
}
