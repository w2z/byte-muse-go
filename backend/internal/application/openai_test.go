package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOpenAITimeoutAndTranslation 验证共享请求保留翻译协议，并遵守调用方取消与截止时间。
func TestOpenAITimeoutAndTranslation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]string `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) == 2 {
			if body.Messages[0]["content"] != "自定义翻译提示" {
				t.Error("翻译提示丢失")
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"译文"}}]}`))
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	service, err := NewTranslationService(TranslationConfig{Engine: TranslationEngineOpenAI, OpenAIURL: server.URL, OpenAIModel: "example", OpenAIAPIKey: "example-key", TranslationPrompt: "自定义翻译提示"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Translate(context.Background(), TranslationRequest{Text: "source"})
	if err != nil || value != "译文" {
		t.Fatalf("translation=%q err=%v", value, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = TestOpenAI(ctx, OpenAIConfig{URL: server.URL, Model: "example", APIKey: "example-key"})
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("timeout=%v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	err = TestOpenAI(canceled, OpenAIConfig{URL: server.URL, Model: "example", APIKey: "example-key"})
	if err == nil || !strings.Contains(err.Error(), "取消") {
		t.Fatalf("cancellation=%v", err)
	}
}
