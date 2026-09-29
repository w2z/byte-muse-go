package agent

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
	"sync"
	"time"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

const (
	// maxIterations 限制单轮对话的工具调用轮数，避免模型陷入循环。
	maxIterations = 10
	// maxHistoryItems 是每个会话保留的最大消息条数。
	maxHistoryItems = 20
	// sessionTTL 是空闲会话的保留时长。
	sessionTTL = 24 * time.Hour
	// requestTimeout 是单次 OpenAI 请求的超时。
	requestTimeout = 60 * time.Second
	// maxResponseBytes 限制读取上游响应的字节数。
	maxResponseBytes = 4 << 20
)

// ErrAgentDisabled 表示 Agent 未启用或 OpenAI 参数不完整。
var ErrAgentDisabled = errors.New("Agent 未启用，请在设置页启用对话 Agent 并填写 OpenAI 接口地址、模型和 API Key")

// SettingsProvider 在执行时读取最新配置；Agent 不缓存凭据，改配置后无需重启即可生效。
type SettingsProvider func(ctx context.Context) (map[string]string, error)

// Orchestrator 是渠道无关的对话 Agent：管理会话历史、执行 OpenAI function calling 循环。
// 渠道差异由调用方（Router 与各平台适配器）承担，本类型不感知 Telegram 或企业微信。
type Orchestrator struct {
	deps     Deps
	settings SettingsProvider
	registry *Registry

	mu       sync.Mutex
	sessions map[string]*session
	clients  map[string]*http.Client
}

type session struct {
	history  []chatMessage
	lastSeen time.Time
}

// NewOrchestrator 绑定业务依赖、配置读取器和工具目录。
func NewOrchestrator(deps Deps, settings SettingsProvider, registry *Registry) *Orchestrator {
	return &Orchestrator{
		deps:     deps,
		settings: settings,
		registry: registry,
		sessions: make(map[string]*session),
		clients:  make(map[string]*http.Client),
	}
}

// Registry 暴露工具目录，供斜杠命令和外部消费者复用同一份定义。
func (o *Orchestrator) Registry() *Registry { return o.registry }

// Enabled 报告当前配置是否足以发起对话。
func (o *Orchestrator) Enabled(ctx context.Context) bool {
	_, err := o.config(ctx)
	return err == nil
}

// Reply 执行一轮对话并返回最终回复文本。
// 调用方负责把回复投递到渠道；本方法不直接发送消息。
func (o *Orchestrator) Reply(ctx context.Context, msg ports.InboundMessage) (string, error) {
	cfg, err := o.config(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(msg.Text) == "" {
		return "", nil
	}
	key := msg.SessionKey()
	o.cleanupExpired()
	history := o.history(key)
	messages := make([]chatMessage, 0, len(history)+2)
	messages = append(messages, chatMessage{Role: "system", Content: o.systemPrompt(cfg, msg.Channel)})
	messages = append(messages, history...)
	messages = append(messages, chatMessage{Role: "user", Content: msg.Text})

	ctx = WithMessage(ctx, msg)
	tools := o.registry.OpenAITools()
	used := make([]string, 0, 4)
	for iteration := 0; iteration < maxIterations; iteration++ {
		message, err := o.complete(ctx, cfg, messages, tools)
		if err != nil {
			logging.Error(logging.CategoryAgent, "Agent 对话失败", "channel", msg.Channel, "error", err.Error())
			return "", err
		}
		if len(message.ToolCalls) == 0 {
			reply := strings.TrimSpace(message.Content)
			if reply == "" {
				reply = "已处理完成"
			}
			o.remember(key, msg.Text, reply)
			logging.Info(logging.CategoryAgent, "Agent 对话完成", "channel", msg.Channel, "tools", strings.Join(used, ","))
			return reply, nil
		}
		messages = append(messages, chatMessage{Role: "assistant", Content: message.Content, ToolCalls: message.ToolCalls})
		for _, call := range message.ToolCalls {
			used = append(used, call.Function.Name)
			result := o.registry.Call(ctx, call.Function.Name, parseArguments(call.Function.Arguments))
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: result})
		}
	}
	return "操作步骤过多，请把需求拆成更小的请求后再试", nil
}

// ClearSession 清空一个会话的上下文；不存在的会话视为已清空。
func (o *Orchestrator) ClearSession(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.sessions, key)
}

// openAIConfig 是一次对话使用的完整配置。
type openAIConfig struct {
	endpoint string
	model    string
	apiKey   string
	proxy    string
	prompt   string
}

// config 读取并校验当前配置；任一必填项缺失都视为未启用。
func (o *Orchestrator) config(ctx context.Context) (openAIConfig, error) {
	if o.settings == nil {
		return openAIConfig{}, ErrAgentDisabled
	}
	values, err := o.settings(ctx)
	if err != nil {
		return openAIConfig{}, fmt.Errorf("读取 Agent 配置失败: %w", err)
	}
	if strings.TrimSpace(values["AGENT_ENABLE"]) != "true" {
		return openAIConfig{}, ErrAgentDisabled
	}
	endpoint, err := chatCompletionsEndpoint(values["OPENAI_URL"])
	if err != nil {
		return openAIConfig{}, ErrAgentDisabled
	}
	model := strings.TrimSpace(values["OPENAI_MODEL"])
	apiKey := strings.TrimSpace(values["OPENAI_API_KEY"])
	if model == "" || apiKey == "" {
		return openAIConfig{}, ErrAgentDisabled
	}
	return openAIConfig{
		endpoint: endpoint,
		model:    model,
		apiKey:   apiKey,
		proxy:    strings.TrimSpace(values["PROXY"]),
		prompt:   strings.TrimSpace(values["AGENT_SYSTEM_PROMPT"]),
	}, nil
}

// chatCompletionsEndpoint 把设置里的接口地址规范化成 chat/completions 端点。
// 与设置页「测试 OpenAI」使用同一条规则，拒绝带凭据、查询串或片段的地址。
func chatCompletionsEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrAgentDisabled
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/chat/completions"
	return parsed.String(), nil
}

// complete 发起一次 chat/completions 请求并解析首条候选消息。
func (o *Orchestrator) complete(ctx context.Context, cfg openAIConfig, messages []chatMessage, tools []map[string]any) (chatMessage, error) {
	payload := map[string]any{
		"model":       cfg.model,
		"messages":    messages,
		"tool_choice": "auto",
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return chatMessage{}, errors.New("Agent 请求构建失败")
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, cfg.endpoint, bytes.NewReader(raw))
	if err != nil {
		return chatMessage{}, errors.New("Agent 请求构建失败")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+cfg.apiKey)
	response, err := o.client(cfg.proxy).Do(request)
	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return chatMessage{}, errors.New("模型响应超时，请稍后重试")
		}
		if errors.Is(requestCtx.Err(), context.Canceled) {
			return chatMessage{}, errors.New("对话已取消")
		}
		return chatMessage{}, errors.New("无法连接模型服务，请检查 OpenAI 接口地址或网络")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return chatMessage{}, errors.New("模型返回的响应无法读取")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return chatMessage{}, fmt.Errorf("模型服务返回错误（HTTP %d），请检查 API Key、模型权限或额度", response.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return chatMessage{}, errors.New("模型返回的响应格式无效")
	}
	if len(parsed.Choices) == 0 {
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return chatMessage{}, errors.New("模型返回错误，请检查模型名称与账号额度")
		}
		return chatMessage{}, errors.New("模型未返回有效回复")
	}
	message := parsed.Choices[0].Message
	message.ToolCalls = normalizeToolCalls(message.ToolCalls)
	return message, nil
}

// normalizeToolCalls 补齐模型省略的调用标识，保证 tool 消息能正确回填。
func normalizeToolCalls(calls []toolCall) []toolCall {
	out := make([]toolCall, 0, len(calls))
	for index, call := range calls {
		if strings.TrimSpace(call.Function.Name) == "" {
			continue
		}
		if strings.TrimSpace(call.ID) == "" {
			call.ID = fmt.Sprintf("call_%d", index)
		}
		if strings.TrimSpace(call.Type) == "" {
			call.Type = "function"
		}
		out = append(out, call)
	}
	return out
}

// client 按代理配置缓存 HTTP 客户端；不跟随重定向，避免凭据被转发到其他主机。
func (o *Orchestrator) client(proxy string) *http.Client {
	o.mu.Lock()
	defer o.mu.Unlock()
	if cached, ok := o.clients[proxy]; ok {
		return cached
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		if parsed, err := url.Parse(proxy); err == nil && parsed.Host != "" {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	created := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	o.clients[proxy] = created
	return created
}

func (o *Orchestrator) history(key string) []chatMessage {
	o.mu.Lock()
	defer o.mu.Unlock()
	item, ok := o.sessions[key]
	if !ok {
		return nil
	}
	return append([]chatMessage(nil), item.history...)
}

func (o *Orchestrator) remember(key, userText, reply string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	item, ok := o.sessions[key]
	if !ok {
		item = &session{}
		o.sessions[key] = item
	}
	item.history = append(item.history,
		chatMessage{Role: "user", Content: userText},
		chatMessage{Role: "assistant", Content: reply},
	)
	if len(item.history) > maxHistoryItems {
		item.history = item.history[len(item.history)-maxHistoryItems:]
	}
	item.lastSeen = time.Now()
}

// cleanupExpired 清理空闲会话，避免长期运行后内存无界增长。
func (o *Orchestrator) cleanupExpired() {
	cutoff := time.Now().Add(-sessionTTL)
	o.mu.Lock()
	defer o.mu.Unlock()
	for key, item := range o.sessions {
		if item.lastSeen.Before(cutoff) {
			delete(o.sessions, key)
		}
	}
}

// parseArguments 解析模型给出的工具参数；非法 JSON 视为空参数，由工具自行报错。
func parseArguments(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]any{}
	}
	return parsed
}

// messageContextKey 在 context 中携带当前入站消息，供主动回复类工具定位渠道与目标。
type messageContextKey struct{}

// WithMessage 把当前入站消息写入 context。
func WithMessage(ctx context.Context, msg ports.InboundMessage) context.Context {
	return context.WithValue(ctx, messageContextKey{}, msg)
}

// MessageFromContext 读取当前入站消息。
func MessageFromContext(ctx context.Context) (ports.InboundMessage, bool) {
	msg, ok := ctx.Value(messageContextKey{}).(ports.InboundMessage)
	return msg, ok
}
