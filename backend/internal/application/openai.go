package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrInvalidOpenAIConfig 表示不能发起测试的表单输入。
var ErrInvalidOpenAIConfig = errors.New("请填写有效的 OpenAI 接口地址、模型名称和 API Key；地址不能包含用户名、密码、查询参数或片段")

// OpenAIConfig 是本次请求使用的配置；连接测试只使用草稿，不读取或保存设置。
type OpenAIConfig struct {
	URL    string `json:"url"`
	Model  string `json:"model"`
	APIKey string `json:"api_key"`
}

// TestOpenAI 使用简短对话验证模型与凭据，只有收到非空回复才视为可用。
func TestOpenAI(ctx context.Context, config OpenAIConfig) error {
	_, err := openAICompletion(ctx, nil, config, map[string]any{"messages": []map[string]string{{"role": "user", "content": "Reply only with OK."}}})
	return err
}

// openAICompletion 统一翻译与连接测试的请求、超时和响应判定；不重试、不回显上游正文或凭据。
func openAICompletion(ctx context.Context, client *http.Client, config OpenAIConfig, payload map[string]any) (string, error) {
	config.URL, config.Model, config.APIKey = strings.TrimSpace(config.URL), strings.TrimSpace(config.Model), strings.TrimSpace(config.APIKey)
	endpoint, err := url.Parse(config.URL)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || config.Model == "" || config.APIKey == "" {
		return "", ErrInvalidOpenAIConfig
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/chat/completions"
	payload["model"] = config.Model
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("OpenAI 请求构建失败")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw))
	if err != nil {
		return "", ErrInvalidOpenAIConfig
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.APIKey)
	if client == nil {
		client = http.DefaultClient
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("OpenAI 请求超时，请检查接口地址或网络")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", errors.New("OpenAI 请求已取消")
		}
		return "", errors.New("无法连接 OpenAI，请检查接口地址或网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case 401, 403:
			return "", fmt.Errorf("OpenAI 鉴权失败（HTTP %d），请检查 API Key 和模型权限", resp.StatusCode)
		case 404:
			return "", errors.New("OpenAI 接口或模型不存在（HTTP 404），请检查接口地址和模型名称")
		case 429:
			return "", errors.New("OpenAI 请求受限（HTTP 429），请检查额度或稍后重试")
		default:
			return "", fmt.Errorf("OpenAI 请求失败（HTTP %d）", resp.StatusCode)
		}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 || json.Unmarshal(body, &out) != nil {
		return "", errors.New("OpenAI 返回的响应格式无效")
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("OpenAI 未返回有效回复，请检查模型是否支持对话")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
