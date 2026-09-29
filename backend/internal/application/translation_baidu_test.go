package application

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// baiduTestService 构造百度引擎服务，并把接口地址指向测试服务器。
func baiduTestService(t *testing.T, endpoint string) *TranslationService {
	t.Helper()
	service, err := NewTranslationService(TranslationConfig{Engine: TranslationEngineBaidu, BaiduAppID: "app-1", BaiduAPIKey: "key-1"}, nil)
	if err != nil {
		t.Fatalf("构造百度翻译服务失败: %v", err)
	}
	service.baiduURL = endpoint
	return service
}

// TestBaiduSignsRequestAndParsesResult 校验请求按百度协议签名：salt 随机、sign 为原文的 MD5，
// 且原文在签名时不做 URL 编码（多字节文本先编码再签名会得到 54001）。
func TestBaiduSignsRequestAndParsesResult(t *testing.T) {
	cases := []struct{ name, text string }{
		{"ASCII", "apple"},
		{"多字节", "初撮り 新人NO.1STYLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotContentType string
			var gotForm url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotContentType = r.Header.Get("Content-Type")
				body, _ := io.ReadAll(r.Body)
				gotForm, _ = url.ParseQuery(string(body))
				io.WriteString(w, `{"from":"en","to":"zh","trans_result":[{"src":"apple","dst":"苹果"}]}`)
			}))
			defer server.Close()
			service := baiduTestService(t, server.URL)
			value, err := service.Translate(context.Background(), TranslationRequest{Text: tc.text, TargetLanguage: "zh-CN"})
			if err != nil {
				t.Fatalf("百度翻译失败: %v", err)
			}
			if value != "苹果" {
				t.Fatalf("译文 = %q，期望 苹果", value)
			}
			if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
				t.Fatalf("Content-Type = %q，期望表单编码", gotContentType)
			}
			if gotForm.Get("q") != tc.text || gotForm.Get("from") != "auto" || gotForm.Get("to") != "zh" || gotForm.Get("appid") != "app-1" {
				t.Fatalf("请求参数不符合百度协议: %v", gotForm)
			}
			salt := gotForm.Get("salt")
			if salt == "" {
				t.Fatal("使用 sign 鉴权必须携带 salt")
			}
			sum := md5.Sum([]byte("app-1" + tc.text + salt + "key-1"))
			if want := hex.EncodeToString(sum[:]); gotForm.Get("sign") != want {
				t.Fatalf("sign = %q，期望 MD5(appid+q+salt+密钥) = %q", gotForm.Get("sign"), want)
			}
		})
	}
}

func TestParseBaiduTranslation(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr string
	}{
		{"单段", `{"from":"en","to":"zh","trans_result":[{"src":"apple","dst":"苹果"}]}`, "苹果", ""},
		{"多段拼接", `{"trans_result":[{"dst":"上集"},{"dst":"下集"}]}`, "上集下集", ""},
		{"成功码", `{"error_code":"52000","trans_result":[{"dst":"苹果"}]}`, "苹果", ""},
		{"业务错误", `{"error_code":"54001","error_msg":"Invalid Sign"}`, "", "54001"},
		{"未收录错误码", `{"error_code":"99999","error_msg":"unknown"}`, "", "unknown"},
		{"译文为空", `{"trans_result":[]}`, "", "未返回译文"},
		{"非法 JSON", `not json`, "", "格式无效"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBaiduTranslation([]byte(tc.body))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("解析失败: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("期望报错，实际解析成功")
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("错误 = %v，期望包含 %q", err, tc.wantErr)
				}
			}
			if got != tc.want {
				t.Fatalf("译文 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestBaiduErrorCodeIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error_code":"54001","error_msg":"Invalid Sign"}`)
	}))
	defer server.Close()
	service := baiduTestService(t, server.URL)
	_, err := service.Translate(context.Background(), TranslationRequest{Text: "apple", TargetLanguage: "zh-CN"})
	if err == nil {
		t.Fatal("百度返回 error_code 时必须报错")
	}
	if !strings.Contains(err.Error(), "54001") || !strings.Contains(err.Error(), "开发者密钥") {
		t.Fatalf("错误信息应包含错误码与中文说明，实际: %v", err)
	}
}

func TestBaiduLanguage(t *testing.T) {
	cases := map[string]string{
		"":        "zh",
		"zh-CN":   "zh",
		"zh-cn":   "zh",
		"zh-TW":   "cht",
		"zh-Hant": "cht",
		"ja":      "jp",
		"ja-JP":   "jp",
		"ko":      "kor",
		"en":      "en",
		"fr-FR":   "fra",
	}
	for target, want := range cases {
		if got := baiduLanguage(target); got != want {
			t.Fatalf("baiduLanguage(%q) = %q，期望 %q", target, got, want)
		}
	}
}
