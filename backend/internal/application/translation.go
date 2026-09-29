package application

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type TranslationEngine string

const (
	TranslationEngineNone   TranslationEngine = "none"
	TranslationEngineOpenAI TranslationEngine = "openai"
	TranslationEngineGoogle TranslationEngine = "google"
	TranslationEngineBaidu  TranslationEngine = "baidu"
	TranslationEngineDeepLX TranslationEngine = "deeplx"
)

// Google 翻译有两个入口，由 GOOGLE_API_KEY 是否为空决定：
//   - 有 key：官方 Cloud Translation API v2，配额与计费按 key 所属项目结算。
//   - 无 key：浏览器翻译组件同款接口，无需任何凭据，但按来源 IP 限流且没有可用性承诺。
const (
	googleOfficialEndpoint = "https://translation.googleapis.com/language/translate/v2"
	googleFreeEndpoint     = "https://translate.googleapis.com/translate_a/single"
	// googleFreeClient 是免密钥接口的调用方标识；gtx 已被大规模滥用并长期返回 429，故使用 dict-chrome-ex。
	googleFreeClient = "dict-chrome-ex"
	// googleFreeMinInterval 是免密钥接口两次请求之间的最小间隔。该接口按来源 IP 限流，
	// 突发请求会返回 429，因此请求串行化并保持间隔，这是可用性要求而非性能优化。
	googleFreeMinInterval = 300 * time.Millisecond
	// translationRetryLimit 是 429 与 5xx 的最大重试次数，不含首次请求。
	translationRetryLimit = 3
	// translationRetryBaseDelay 是重试退避基数，按尝试次数翻倍。
	translationRetryBaseDelay = 500 * time.Millisecond
	// translationRequestTimeout 是单次翻译请求的超时；Google、百度与 DeepLX 共用。
	translationRequestTimeout = 20 * time.Second
	// translationMaxResponseBytes 是响应体上限，避免异常上游耗尽内存。
	translationMaxResponseBytes = 1 << 20
)

// 百度引擎调用开放平台的通用文本翻译 API：appid 对应配置项 BAIDU_APP_ID，
// 开发者密钥对应 BAIDU_API_KEY，签名方式为 MD5(appid+q+salt+密钥)。
const baiduEndpoint = "https://fanyi-api.baidu.com/api/trans/vip/translate"

// TranslationConfig 是翻译引擎的完整配置。GoogleAPIKey 允许为空：
// 为空时 Google 引擎改用免密钥接口，不再视为配置不完整。
type TranslationConfig struct {
	Engine                                                                                 TranslationEngine
	OpenAIURL, OpenAIModel, OpenAIAPIKey, GoogleAPIKey, BaiduAppID, BaiduAPIKey, DeepLXURL string
	TranslationPrompt                                                                      string
}

// TranslationRequest 描述一次翻译；Prompt 为空时使用配置中的默认提示词，
// 标签归一化等短词场景可传入专用提示词，避免标题提示词把短词扩写成句子。
type TranslationRequest struct{ Text, TargetLanguage, Prompt string }

// TranslationClient 是媒体标题翻译的最小依赖接口。
type TranslationClient interface {
	Translate(ctx context.Context, request TranslationRequest) (string, error)
}
type TranslationService struct {
	config TranslationConfig
	client *http.Client
	// googlePacer 只服务免密钥入口；官方 API 有自己的配额，不参与节流。
	googlePacer googlePacer
	// googleOfficialURL 与 googleFreeURL 是 Google 两个入口的地址；生产使用默认值，测试可替换。
	googleOfficialURL string
	googleFreeURL     string
	// baiduURL 是百度大模型文本翻译接口地址；生产使用默认值，测试可替换。
	baiduURL string
}

// NewTranslationService 按引擎构造翻译服务，只校验该引擎真正必需的参数。
// Google 引擎不强制要求 API Key：有 key 走官方接口，没有 key 走免密钥接口。
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
	return &TranslationService{
		config:            config,
		client:            client,
		googleOfficialURL: googleOfficialEndpoint,
		googleFreeURL:     googleFreeEndpoint,
		baiduURL:          baiduEndpoint,
	}, nil
}
func (s *TranslationService) Translate(ctx context.Context, request TranslationRequest) (string, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return "", errors.New("translation text is empty")
	}
	switch s.config.Engine {
	case TranslationEngineOpenAI:
		return s.openAI(ctx, text, request.TargetLanguage, request.Prompt)
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
func (s *TranslationService) openAI(ctx context.Context, text, target, prompt string) (string, error) {
	if target == "" {
		target = "zh-CN"
	}
	systemPrompt := strings.TrimSpace(prompt)
	if systemPrompt == "" {
		systemPrompt = strings.TrimSpace(s.config.TranslationPrompt)
	}
	if systemPrompt == "" {
		systemPrompt = "Translate to " + target + ". Return only translated text."
	}
	payload := map[string]any{"temperature": 0, "messages": []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": text}}}
	return openAICompletion(ctx, s.client, OpenAIConfig{URL: s.config.OpenAIURL, Model: s.config.OpenAIModel, APIKey: s.config.OpenAIAPIKey}, payload)
}

// googlePacer 串行化免密钥入口的请求并保证最小间隔。
// 免密钥接口按来源 IP 限流，突发请求会返回 429，节流是可用性的一部分而不是性能优化。
type googlePacer struct {
	mu   sync.Mutex
	last time.Time
}

// wait 在距离上次放行不足最小间隔时阻塞；上下文取消时立即返回，不占用后续请求的节流窗口。
func (p *googlePacer) wait(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.last.IsZero() {
		if delay := googleFreeMinInterval - time.Since(p.last); delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	p.last = time.Now()
	return nil
}

// google 按 GOOGLE_API_KEY 是否配置选择入口：有 key 走官方 API，没有 key 走免密钥接口。
// 两个入口对调用方语义一致（目标语言、空值回落），调用方不需要知道实际使用哪一个。
func (s *TranslationService) google(ctx context.Context, text, target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		target = "zh-CN"
	}
	if key := strings.TrimSpace(s.config.GoogleAPIKey); key != "" {
		return s.googleOfficial(ctx, text, target, key)
	}
	return s.googleFree(ctx, text, target)
}

// googleOfficial 调用官方 Cloud Translation API v2；配额与计费由 key 所属项目承担。
func (s *TranslationService) googleOfficial(ctx context.Context, text, target, key string) (string, error) {
	payload := map[string]any{"q": text, "target": target, "format": "text", "key": key}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("Google 翻译请求构建失败")
	}
	body, err := s.doProviderRequest(ctx, "Google 翻译", func() (*http.Request, error) {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, s.googleOfficialURL, bytes.NewReader(raw))
		if e != nil {
			return nil, e
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Data struct {
			Translations []struct {
				TranslatedText string `json:"translatedText"`
			} `json:"translations"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &out) != nil || len(out.Data.Translations) == 0 {
		return "", errors.New("Google 翻译返回的响应格式无效")
	}
	// 官方接口按 HTML 转义返回译文，标题入库前必须还原实体，否则会写入 &#39; 之类的字面量。
	value := strings.TrimSpace(html.UnescapeString(out.Data.Translations[0].TranslatedText))
	if value == "" {
		return "", errors.New("Google 翻译未返回译文")
	}
	return value, nil
}

// googleFree 调用免密钥接口。该接口没有配额承诺、按来源 IP 限流，属于非公开接口，
// 只用于没有 Google Cloud 凭据的场景；可用性依赖这里的串行节流与退避重试。
func (s *TranslationService) googleFree(ctx context.Context, text, target string) (string, error) {
	body, err := s.doProviderRequest(ctx, "Google 翻译", func() (*http.Request, error) {
		if e := s.googlePacer.wait(ctx); e != nil {
			return nil, e
		}
		query := url.Values{}
		query.Set("client", googleFreeClient)
		query.Set("sl", "auto")
		query.Set("tl", target)
		query.Set("dt", "t")
		query.Set("q", text)
		return http.NewRequestWithContext(ctx, http.MethodGet, s.googleFreeURL+"?"+query.Encode(), nil)
	})
	if err != nil {
		return "", err
	}
	return parseGoogleFreeTranslation(body)
}

// parseGoogleFreeTranslation 解析免密钥接口的嵌套数组响应：[[["译文","原文",...],...],...]。
// 长文本会被切成多段，需要按顺序拼接；结构异常时返回错误，绝不返回空译文冒充成功。
func parseGoogleFreeTranslation(body []byte) (string, error) {
	var outer []json.RawMessage
	if err := json.Unmarshal(body, &outer); err != nil || len(outer) == 0 {
		return "", errors.New("Google 翻译返回的响应格式无效")
	}
	var segments []json.RawMessage
	if err := json.Unmarshal(outer[0], &segments); err != nil {
		return "", errors.New("Google 翻译返回的响应格式无效")
	}
	var builder strings.Builder
	for _, segment := range segments {
		var fields []json.RawMessage
		if json.Unmarshal(segment, &fields) != nil || len(fields) == 0 {
			continue
		}
		var piece string
		if json.Unmarshal(fields[0], &piece) != nil {
			continue
		}
		builder.WriteString(piece)
	}
	value := strings.TrimSpace(builder.String())
	if value == "" {
		return "", errors.New("Google 翻译未返回译文")
	}
	return value, nil
}

// doProviderRequest 发送翻译请求，并在 429 与 5xx 时按次数退避重试；build 每次重试都重新构造请求。
// provider 只用于错误文案；Google、百度与 DeepLX 共用同一套超时、重试与响应体上限。
func (s *TranslationService) doProviderRequest(ctx context.Context, provider string, build func() (*http.Request, error)) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= translationRetryLimit; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(translationRetryBaseDelay << (attempt - 1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		req, err := build()
		if err != nil {
			return nil, err
		}
		body, retryable, err := s.sendProviderRequest(ctx, req, provider)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, lastErr
}

// sendProviderRequest 发送单次请求；第二个返回值表示是否为可重试的临时失败（429 与 5xx）。
// 失败时只返回状态码，不读取或回显上游正文，避免把配额提示和凭据细节写入日志。
func (s *TranslationService) sendProviderRequest(ctx context.Context, req *http.Request, provider string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, translationRequestTimeout)
	defer cancel()
	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req.WithContext(ctx))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, false, fmt.Errorf("%s请求超时，请检查网络或代理", provider)
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, false, ctx.Err()
		}
		return nil, false, fmt.Errorf("无法连接%s，请检查网络或代理", provider)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, retryable, fmt.Errorf("%s返回 HTTP %d", provider, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, translationMaxResponseBytes+1))
	if err != nil || len(body) > translationMaxResponseBytes {
		return nil, false, fmt.Errorf("%s返回的响应格式无效", provider)
	}
	return body, false, nil
}

// baidu 调用百度通用文本翻译 API：appid + salt + sign 签名鉴权，成功时返回 trans_result[].dst。
// 该接口在业务失败时同样返回 HTTP 200，必须由响应体的 error_code 判定失败。
func (s *TranslationService) baidu(ctx context.Context, text, target string) (string, error) {
	appid := strings.TrimSpace(s.config.BaiduAppID)
	key := strings.TrimSpace(s.config.BaiduAPIKey)
	// salt 只要求每次请求不同；签名必须用未编码的原文计算，先做 URL 编码会得到 54001。
	salt := rand.Text()
	form := url.Values{}
	form.Set("q", text)
	form.Set("from", "auto")
	form.Set("to", baiduLanguage(target))
	form.Set("appid", appid)
	form.Set("salt", salt)
	form.Set("sign", baiduSign(appid, text, salt, key))
	body, err := s.doProviderRequest(ctx, "百度翻译", func() (*http.Request, error) {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, s.baiduURL, strings.NewReader(form.Encode()))
		if e != nil {
			return nil, e
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req, nil
	})
	if err != nil {
		return "", err
	}
	return parseBaiduTranslation(body)
}

// baiduSign 按百度文档计算签名：MD5(appid+q+salt+密钥) 的 32 位小写十六进制。
func baiduSign(appid, text, salt, key string) string {
	sum := md5.Sum([]byte(appid + text + salt + key))
	return hex.EncodeToString(sum[:])
}

// parseBaiduTranslation 解析百度返回：译文可能分多段，需要按顺序拼接；
// error_code 非空（52000 表示成功）即业务失败，此时不能把空译文当成成功。
func parseBaiduTranslation(body []byte) (string, error) {
	var out struct {
		ErrorCode   string `json:"error_code"`
		ErrorMsg    string `json:"error_msg"`
		TransResult []struct {
			Dst string `json:"dst"`
		} `json:"trans_result"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", errors.New("百度翻译返回的响应格式无效")
	}
	if code := strings.TrimSpace(out.ErrorCode); code != "" && code != "52000" {
		reason := baiduErrorMessages[code]
		if reason == "" {
			reason = strings.TrimSpace(out.ErrorMsg)
		}
		return "", fmt.Errorf("百度翻译返回错误 %s：%s", code, reason)
	}
	var builder strings.Builder
	for _, item := range out.TransResult {
		builder.WriteString(item.Dst)
	}
	value := strings.TrimSpace(builder.String())
	if value == "" {
		return "", errors.New("百度翻译未返回译文")
	}
	return value, nil
}

// baiduErrorMessages 是百度错误码的中文说明，用于把英文错误信息变成可操作的提示；
// 未收录的错误码回退为百度原文，避免丢失上游信息。
var baiduErrorMessages = map[string]string{
	"52001": "请求超时，请检查待翻译文本与语种参数",
	"52002": "百度服务暂时不可用，请稍后重试",
	"52003": "未授权用户，请确认 APPID 正确且已开通文本翻译服务",
	"54000": "必填参数为空",
	"54001": "签名错误，请确认 BAIDU_API_KEY 是开放平台的开发者密钥（密钥）",
	"54003": "访问频率受限，请降低调用频率",
	"54004": "账户余额不足，请前往百度翻译开放平台充值",
	"54005": "长文本请求过于频繁，请稍后重试",
	"58000": "客户端 IP 非法，请检查开放平台中填写的服务器 IP",
	"58001": "译文语言方向不支持",
	"58002": "服务已关闭，请在开放平台开启对应服务",
	"58003": "当前 IP 已被封禁",
	"59003": "待翻译文本超过 6000 字符上限",
	"59004": "请求 QPS 超限",
	"20003": "请求内容存在安全风险",
}

// baiduLanguage 把项目统一的目标语言（如 zh-CN）转换为百度语种代码。
// 百度在部分语种上使用与 ISO 639-1 不同的代码，必须显式映射，其余按主语言子标签透传。
func baiduLanguage(target string) string {
	code := strings.ToLower(strings.TrimSpace(target))
	if code == "" {
		return "zh"
	}
	if mapped, ok := baiduLanguageAliases[code]; ok {
		return mapped
	}
	base := code
	if index := strings.IndexAny(base, "-_"); index >= 0 {
		base = base[:index]
	}
	if mapped, ok := baiduLanguageAliases[base]; ok {
		return mapped
	}
	return base
}

// baiduLanguageAliases 只收录百度与 ISO 639-1 写法不一致的语种，其余语种沿用 ISO 代码。
var baiduLanguageAliases = map[string]string{
	"zh-tw":   "cht",
	"zh-hk":   "cht",
	"zh-hant": "cht",
	"ja":      "jp",
	"ko":      "kor",
	"fr":      "fra",
	"es":      "spa",
	"ar":      "ara",
	"vi":      "vie",
	"he":      "heb",
}

// deeplx 调用 DeepLX。DeepLX 是自建服务、地址由用户填写，因此同时兼容
// 「只填服务地址」与「直接填到 /translate」两种写法，并按 DeepL 语种代码传参。
func (s *TranslationService) deeplx(ctx context.Context, text, target string) (string, error) {
	payload := map[string]any{
		"text":        text,
		"source_lang": "auto",
		"target_lang": deeplxLanguage(target),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("DeepLX 翻译请求构建失败")
	}
	body, err := s.doProviderRequest(ctx, "DeepLX", func() (*http.Request, error) {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, deeplxEndpoint(s.config.DeepLXURL), bytes.NewReader(raw))
		if e != nil {
			return nil, e
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data"`
	}
	if json.Unmarshal(body, &out) != nil {
		return "", errors.New("DeepLX 返回的响应格式无效")
	}
	// DeepLX 用 HTTP 200 + 业务 code 表达失败，只看 data 会把限流和地址错误当成空译文。
	if out.Code != 200 {
		message := strings.TrimSpace(out.Message)
		if message == "" {
			message = "DeepLX 未返回失败原因"
		}
		return "", fmt.Errorf("DeepLX 返回错误 %d：%s", out.Code, message)
	}
	value := strings.TrimSpace(out.Data)
	if value == "" {
		return "", errors.New("DeepLX 未返回译文")
	}
	return value, nil
}

// deeplxEndpoint 按 DeepLX 文档补齐 /translate 路径，避免用户已填完整地址时出现 /translate/translate。
func deeplxEndpoint(rawURL string) string {
	value := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if strings.HasSuffix(value, "/translate") {
		return value
	}
	return value + "/translate"
}

// deeplxLanguage 把项目统一的目标语言（如 zh-CN）转换为 DeepLX 语种代码。
// DeepLX 只接受大写代码，小写或带地区的写法会被判为不支持的语种；本项目只请求 zh-CN。
func deeplxLanguage(target string) string {
	code := strings.TrimSpace(target)
	if code == "" {
		return "ZH"
	}
	if index := strings.IndexAny(code, "-_"); index >= 0 {
		code = code[:index]
	}
	return strings.ToUpper(code)
}

func ResolveAgentSystemPrompt(custom, fallback string) string {
	if value := strings.TrimSpace(custom); value != "" {
		return value
	}
	return fallback
}

// translationNoiseMarkers 是模型没有按「只返回译文」作答时才会出现的短语：提示词回显、拒答与免责声明。
// 命中即判定本次输出无效，调用方不得写入数据库。
var translationNoiseMarkers = []string{
	"翻译如下", "以下是翻译", "最终翻译", "按照专业翻译", "我已按照", "已按照", "按照中文语境",
	"清理了所有", "移除了无关信息", "去除html", "html标签", "无关内容与标签",
	"无法提供", "不能提供", "无法为您", "不能为您", "欢迎随时提出", "请提出其他",
	"作为ai", "作为人工智能", "ai助手", "违反国家法律", "法律法规", "社会主义核心价值观", "不适宜",
	"讲述了", "本片讲述", "影片讲述", "剧情简介", "故事简介", "观众将看到", "埋下伏笔", "剧情梗概",
	"i cannot", "i'm sorry", "as an ai", "translate to", "return only",
}

// SanitizeTranslatedTitle 校验模型输出是否为可直接展示的标题译文。
// 返回 ok=false 表示输出无效（提示词回显、拒答、免责声明、夹带说明或含换行），调用方必须保留原值并允许重试。
// 这是译文入库的唯一权威校验，懒翻译与采集翻译任务共用。
func SanitizeTranslatedTitle(title, translated string) (string, bool) {
	value := strings.TrimSpace(translated)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	lowered := strings.ToLower(value)
	for _, marker := range translationNoiseMarkers {
		if strings.Contains(lowered, strings.ToLower(marker)) {
			return "", false
		}
	}
	// 标题译文与原文同量级；明显超出说明模型在译文之外夹带了说明或剧情概述。
	if limit := utf8.RuneCountInString(title)*3 + 40; utf8.RuneCountInString(value) > limit {
		return "", false
	}
	return value, true
}
