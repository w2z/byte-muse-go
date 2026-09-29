package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type TranslationEngine string

const (
	TranslationEngineNone   TranslationEngine = "none"
	TranslationEngineOpenAI TranslationEngine = "openai"
	TranslationEngineGoogle TranslationEngine = "google"
	TranslationEngineBaidu  TranslationEngine = "baidu"
	TranslationEngineDeepLX TranslationEngine = "deeplx"
)

type TranslationConfig struct {
	Engine                                                                                 TranslationEngine
	OpenAIURL, OpenAIModel, OpenAIAPIKey, GoogleAPIKey, BaiduAppID, BaiduAPIKey, DeepLXURL string
	TranslationPrompt                                                                      string
}
type TranslationRequest struct{ Text, TargetLanguage string }

// TranslationClient 是媒体标题翻译的最小依赖接口。
type TranslationClient interface {
	Translate(ctx context.Context, request TranslationRequest) (string, error)
}
type TranslationService struct {
	config TranslationConfig
	client *http.Client
}

func NewTranslationService(config TranslationConfig, client *http.Client) (*TranslationService, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if config.Engine == "" {
		config.Engine = TranslationEngineNone
	}
	switch config.Engine {
	case TranslationEngineNone:
	case TranslationEngineOpenAI:
		if strings.TrimSpace(config.OpenAIURL) == "" || strings.TrimSpace(config.OpenAIModel) == "" || strings.TrimSpace(config.OpenAIAPIKey) == "" {
			return nil, errors.New("OpenAI translation configuration is incomplete")
		}
	case TranslationEngineGoogle:
		if strings.TrimSpace(config.GoogleAPIKey) == "" {
			return nil, errors.New("Google translation configuration is incomplete")
		}
	case TranslationEngineBaidu:
		if strings.TrimSpace(config.BaiduAppID) == "" || strings.TrimSpace(config.BaiduAPIKey) == "" {
			return nil, errors.New("Baidu translation configuration is incomplete")
		}
	case TranslationEngineDeepLX:
		if strings.TrimSpace(config.DeepLXURL) == "" {
			return nil, errors.New("DeepLX translation configuration is incomplete")
		}
	default:
		return nil, fmt.Errorf("unsupported translation engine %q", config.Engine)
	}
	return &TranslationService{config: config, client: client}, nil
}
func (s *TranslationService) Translate(ctx context.Context, request TranslationRequest) (string, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return "", errors.New("translation text is empty")
	}
	switch s.config.Engine {
	case TranslationEngineOpenAI:
		return s.openAI(ctx, text, request.TargetLanguage)
	case TranslationEngineGoogle:
		return s.google(ctx, text, request.TargetLanguage)
	case TranslationEngineBaidu:
		return s.baidu(ctx, text, request.TargetLanguage)
	case TranslationEngineDeepLX:
		return s.deeplx(ctx, text, request.TargetLanguage)
	default:
		return "", errors.New("translation engine is disabled")
	}
}
func (s *TranslationService) openAI(ctx context.Context, text, target string) (string, error) {
	if target == "" {
		target = "zh-CN"
	}
	systemPrompt := strings.TrimSpace(s.config.TranslationPrompt)
	if systemPrompt == "" {
		systemPrompt = "Translate to " + target + ". Return only translated text."
	}
	payload := map[string]any{"temperature": 0, "messages": []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": text}}}
	return openAICompletion(ctx, s.client, OpenAIConfig{URL: s.config.OpenAIURL, Model: s.config.OpenAIModel, APIKey: s.config.OpenAIAPIKey}, payload)
}
func (s *TranslationService) google(ctx context.Context, text, target string) (string, error) {
	payload := map[string]any{"q": text, "target": target}
	return s.genericJSON(ctx, "https://translation.googleapis.com/language/translate/v2", payload)
}
func (s *TranslationService) baidu(ctx context.Context, text, target string) (string, error) {
	payload := map[string]any{"q": text, "target": target, "appid": s.config.BaiduAppID, "key": s.config.BaiduAPIKey}
	return s.genericJSON(ctx, "https://fanyi-api.baidu.com/api/trans/vip/translate", payload)
}
func (s *TranslationService) genericJSON(ctx context.Context, endpoint string, payload map[string]any) (string, error) {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		TranslatedText string `json:"translatedText"`
		Translation    string `json:"translation"`
	}
	if err := s.doJSON(req, &out); err != nil {
		return "", err
	}
	value := out.TranslatedText
	if value == "" {
		value = out.Translation
	}
	if strings.TrimSpace(value) == "" {
		return "", errors.New("translation response is empty")
	}
	return strings.TrimSpace(value), nil
}
func (s *TranslationService) deeplx(ctx context.Context, text, target string) (string, error) {
	if target == "" {
		target = "ZH"
	}
	payload := map[string]any{
		"text":        text,
		"source_lang": "auto",
		"target_lang": target,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.DeepLXURL, "/")+"/translate", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Code int    `json:"code"`
		Data string `json:"data"`
	}
	if err := s.doJSON(req, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Data) == "" {
		return "", errors.New("translation response is empty")
	}
	return strings.TrimSpace(out.Data), nil
}
func (s *TranslationService) doJSON(req *http.Request, target any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("translation provider returned %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}
func ResolveAgentSystemPrompt(custom, fallback string) string {
	if value := strings.TrimSpace(custom); value != "" {
		return value
	}
	return fallback
}
