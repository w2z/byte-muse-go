package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"

	"github.com/robfig/cron/v3"
)

var ErrInvalidSetting = errors.New("invalid setting")

// 配置项取值域。一个 key 只有一个权威声明，前端控件类型与保存校验都从这里推导。
const (
	settingText      = "text"       // 任意文本
	settingBool      = "bool"       // true / false
	settingInt       = "int"        // 非负整数
	settingJSON      = "json"       // JSON 对象
	settingJSONArray = "json_array" // JSON 数组，元素为对象
	settingEnum      = "enum"       // 封闭取值
	settingSort      = "sort"       // 逗号分隔的排序标签
	settingCron      = "cron"       // 标准 5 段 cron（分 时 日 月 周）
)

// sortTags 是资源排序器接受的标签集合，与对标站排序器一致。
var translationEngines = []string{"none", "openai", "google", "baidu", "deeplx"}

var sortTags = []string{"uc", "!uc", "seeders", "chinese", "uhd", "!uhd", "site", "free"}

// mainSites 是主站选择器的取值，直接与采集到的 Torrent.site 比较。
var mainSites = []string{"ALL", "馒头", "BT", "PTT", "NicePT", "PTFans", "RousiPro"}

// imageModes 是图片显示模式的取值。
var imageModes = []string{"INVISIBLE", "VISIBLE", "BLUR"}

var bypassEngines = []string{"cloudflare_bypass_for_scraping", "flaresolverr", "scrapling"}

var ptDefaultDownloaderOptions = []string{"qbittorrent", "transmission"}

// btDefaultDownloaderOptions 是 BT 默认下载器的可选值；115 只接受磁力与直链，不接受私有种子文件。
var btDefaultDownloaderOptions = []string{"qbittorrent", "transmission", "aria2", "thunder", "pan115"}

// siteAuthTypes 是站点凭据模式；空值默认使用密钥。
var siteAuthTypes = []string{"key", "cookie"}

// SiteCookieCredential 只返回当前选中 Cookie 模式的凭据；密钥模式不得回退旧 Cookie。
// 仅显式选择 Cookie 时返回 Cookie，默认密钥及未知模式均不回退。
func SiteCookieCredential(values map[string]string, prefix string) string {
	mode := strings.TrimSpace(values[prefix+"_AUTH_TYPE"])
	if mode != "cookie" {
		return ""
	}
	return values[prefix+"_COOKIE"]
}

// rankTypes 是 JAVDB 榜单自动订阅类型，空值表示不订阅。
var rankTypes = []string{"daily", "weekly", "monthly"}

// strm 设置键：网盘目录到本地 strm 目录的映射、strm 内容使用的对外基址，
// 以及生成后是否自动刷新 Emby 媒体库；本地根目录固定为 /strm。
// 前端字段定义与此处共用同一组键名。
const (
	strmDownloadEnableSettingKey     = "STRM_DOWNLOAD_ENABLE"
	strmDownloadExtensionsSettingKey = "STRM_DOWNLOAD_EXTENSIONS"
	strmPathsSettingKey              = "STRM_PATHS"
	strmPlayBaseSettingKey           = "STRM_PLAY_BASE"
	strmEmbyRefreshSettingKey        = "STRM_EMBY_REFRESH"
	strmEmbyMediaEnableSettingKey    = "STRM_EMBY_MEDIA_ENABLE"
	strmEmbyMediaIntervalSettingKey  = "STRM_EMBY_MEDIA_INTERVAL_MINUTES"
	strmEmbyMediaAfterRefreshKey     = "STRM_EMBY_MEDIA_AFTER_REFRESH"
)

// pan115ScanPathsSettingKey 是 115 扫描入库目录设置键；元素形如 {"id":"目录 ID","path":"展示用路径"}。
const pan115ScanPathsSettingKey = "PAN115_SCAN_PATHS"

// settingSpec 描述一个可写配置项：是否敏感，以及取值范围。
type settingSpec struct {
	secret   bool
	kind     string
	allowed  []string
	maxBytes int // 0 使用普通配置的 8192 字节限制；提示词容量须兼容 MySQL TEXT。
}

// writableSettings 是可写配置项的唯一权威清单，key 与旧版 template.env、对标站 /config 完全一致。
// 顺序按对标站设置页的分组排列，便于逐组核对。
var writableSettings = map[string]settingSpec{
	"CLOUD_UPLOAD_PATHS":    {kind: settingJSONArray},
	"CLOUD_UPLOAD_ENABLE":   {kind: settingBool},
	"CLOUD_UPLOAD_CONFLICT": {kind: settingEnum, allowed: []string{"skip", "overwrite", "keep_both"}},
	"PAN115_EVENT_ENABLE":   {kind: settingBool},
	// 站点
	"MTEAM_API_KEY":      {secret: true, kind: settingText},
	"PTT_COOKIE":         {secret: true, kind: settingText},
	"PTFANS_COOKIE":      {secret: true, kind: settingText},
	"ROUSIPRO_COOKIE":    {secret: true, kind: settingText},
	"NICEPT_COOKIE":      {secret: true, kind: settingText},
	"PTT_AUTH_TYPE":      {kind: settingEnum, allowed: siteAuthTypes},
	"PTT_PASSKEY":        {secret: true, kind: settingText},
	"PTT_UID":            {kind: settingInt},
	"PTFANS_AUTH_TYPE":   {kind: settingEnum, allowed: siteAuthTypes},
	"PTFANS_API_KEY":     {secret: true, kind: settingText},
	"ROUSIPRO_AUTH_TYPE": {kind: settingEnum, allowed: siteAuthTypes},
	"ROUSIPRO_API_KEY":   {secret: true, kind: settingText},
	"NICEPT_AUTH_TYPE":   {kind: settingEnum, allowed: siteAuthTypes},
	"NICEPT_API_KEY":     {secret: true, kind: settingText},

	// 媒体库
	"EMBY_URL":         {kind: settingText},
	"EMBY_API_KEY":     {secret: true, kind: settingText},
	"PLEX_URL":         {kind: settingText},
	"PLEX_TOKEN":       {secret: true, kind: settingText},
	"JELLYFIN_URL":     {kind: settingText},
	"JELLYFIN_API_KEY": {secret: true, kind: settingText},
	"JELLYFIN_USER":    {kind: settingText},

	// 微信
	"WECHAT_CORP_ID":          {kind: settingText},
	"WECHAT_CORP_SECRET":      {secret: true, kind: settingText},
	"WECHAT_AGENT_ID":         {kind: settingText},
	"WECHAT_PROXY":            {kind: settingText},
	"WECHAT_PHOTO":            {kind: settingText},
	"WECHAT_TOKEN":            {secret: true, kind: settingText},
	"WECHAT_ENCODING_AES_KEY": {secret: true, kind: settingText},
	"WECHAT_TO_USER":          {kind: settingText},
	"WECHAT_BANNER":           {kind: settingBool},
	// 微信渠道通知开关：与 application.NotificationEvent 一一对应，只影响微信推送与微信对话。
	"WECHAT_NOTIFY_SUBSCRIBE":         {kind: settingBool},
	"WECHAT_NOTIFY_SUBSCRIBE_FAILED":  {kind: settingBool},
	"WECHAT_NOTIFY_DOWNLOAD_START":    {kind: settingBool},
	"WECHAT_NOTIFY_DOWNLOAD_COMPLETE": {kind: settingBool},
	"WECHAT_NOTIFY_DOWNLOAD_FAILED":   {kind: settingBool},
	"WECHAT_NOTIFY_AGENT_CHAT":        {kind: settingBool},

	// Telegram
	"TELEGRAM_BOT_TOKEN": {secret: true, kind: settingText},
	"TELEGRAM_CHAT_ID":   {kind: settingText},
	"TELEGRAM_WHITELIST": {kind: settingText},
	"TELEGRAM_SPOILER":   {kind: settingBool},
	// Telegram 渠道通知开关：与微信各自独立，互不影响。
	"TELEGRAM_NOTIFY_SUBSCRIBE":         {kind: settingBool},
	"TELEGRAM_NOTIFY_SUBSCRIBE_FAILED":  {kind: settingBool},
	"TELEGRAM_NOTIFY_DOWNLOAD_START":    {kind: settingBool},
	"TELEGRAM_NOTIFY_DOWNLOAD_COMPLETE": {kind: settingBool},
	"TELEGRAM_NOTIFY_DOWNLOAD_FAILED":   {kind: settingBool},
	"TELEGRAM_NOTIFY_AGENT_CHAT":        {kind: settingBool},

	// Qbittorrent
	"QBITTORRENT_URL":           {kind: settingText},
	"QBITTORRENT_USERNAME":      {kind: settingText},
	"QBITTORRENT_PASSWORD":      {secret: true, kind: settingText},
	"QBITTORRENT_DOWNLOAD_PATH": {kind: settingText},
	"QBITTORRENT_CATEGORY":      {kind: settingText},

	// aria2（仅 BT 资源可使用）
	"ARIA2_URL":             {kind: settingText},
	"ARIA2_SECRET":          {secret: true, kind: settingText},
	"ARIA2_DOWNLOAD_PATH":   {kind: settingText},
	"PT_DEFAULT_DOWNLOADER": {kind: settingEnum, allowed: ptDefaultDownloaderOptions},
	"BT_DEFAULT_DOWNLOADER": {kind: settingEnum, allowed: btDefaultDownloaderOptions},

	// Transmission
	"TRANSMISSION_URL":           {kind: settingText},
	"TRANSMISSION_USERNAME":      {kind: settingText},
	"TRANSMISSION_PASSWORD":      {secret: true, kind: settingText},
	"TRANSMISSION_DOWNLOAD_PATH": {kind: settingText},
	"TRANSMISSION_LABEL":         {kind: settingText},

	// 迅雷
	"THUNDER_URL":           {kind: settingText},
	"THUNDER_FILE_ID":       {kind: settingText},
	"THUNDER_AUTHORIZATION": {secret: true, kind: settingText},

	// CloudDrive2
	"CLOUDNAS_URL":      {kind: settingText},
	"CLOUDNAS_USERNAME": {kind: settingText},
	"CLOUDNAS_PASSWORD": {secret: true, kind: settingText},
	"CLOUDNAS_SAVEPATH": {kind: settingText},

	// 115 网盘：OpenAPI 令牌由扫码绑定管理并单独落库；Cookie 是生活事件与扫码换取共用的唯一凭据，
	// 这里只保存 Cookie、离线下载的目标目录与扫描目录。
	"PAN115_COOKIE":           {secret: true, kind: settingText},
	"PAN115_SAVE_PATH":        {kind: settingText},
	pan115ScanPathsSettingKey: {kind: settingJSONArray},

	// strm：网盘目录与本地 strm 目录的映射，以及 strm 内容使用的对外基址。
	strmPathsSettingKey:              {kind: settingJSONArray},
	strmDownloadEnableSettingKey:     {kind: settingBool},
	strmDownloadExtensionsSettingKey: {kind: settingText},
	strmPlayBaseSettingKey:           {kind: settingText},
	strmEmbyRefreshSettingKey:        {kind: settingBool},
	strmEmbyMediaEnableSettingKey:    {kind: settingBool},
	strmEmbyMediaIntervalSettingKey:  {kind: settingInt},
	strmEmbyMediaAfterRefreshKey:     {kind: settingBool},

	// 过滤
	"DEFAULT_FILTER": {kind: settingJSON},

	// 排序
	"DEFAULT_SORT": {kind: settingSort},
	"MAIN_SITE":    {kind: settingEnum, allowed: mainSites},

	// 定时任务
	"RANK_PAGE":              {kind: settingInt},
	"RANK_TYPE":              {kind: settingEnum, allowed: rankTypes},
	"BRAND_TYPE":             {kind: settingText},
	"RANK_SCHEDULE_TIME":     {kind: settingCron},
	"ACTOR_SCHEDULE_TIME":    {kind: settingCron},
	"TAG_SCHEDULE_TIME":      {kind: settingCron},
	"DOWNLOAD_SCHEDULE_TIME": {kind: settingCron},
	"MAX_ACTOR":              {kind: settingInt},
	"TAG_MAX_SUB_PER_RUN":    {kind: settingInt},
	"PT_SEARCH_INTERVAL":     {kind: settingInt},

	// 翻译
	"BAIDU_APP_ID":       {kind: settingText},
	"BAIDU_API_KEY":      {secret: true, kind: settingText},
	"GOOGLE_API_KEY":     {secret: true, kind: settingText},
	"DEEPLX_URL":         {kind: settingText},
	"TRANSLATION_ENGINE": {kind: settingEnum, allowed: translationEngines},
	"TRANSLATION_PROMPT": {kind: settingText, maxBytes: 60 * 1024},

	// 翻译模型（OpenAI 兼容），与对话 Agent 的 OPENAI_* 相互独立
	"TRANSLATION_OPENAI_URL":     {kind: settingText},
	"TRANSLATION_OPENAI_MODEL":   {kind: settingText},
	"TRANSLATION_OPENAI_API_KEY": {secret: true, kind: settingText},

	// Agent
	"OPENAI_URL":          {kind: settingText},
	"OPENAI_MODEL":        {kind: settingText},
	"OPENAI_API_KEY":      {secret: true, kind: settingText},
	"AGENT_ENABLE":        {kind: settingBool},
	"AGENT_SYSTEM_PROMPT": {kind: settingText, maxBytes: 60 * 1024},

	// 其他
	"IMAGE_MODE":           {kind: settingEnum, allowed: imageModes},
	"PROXY":                {kind: settingText},
	"EXTERNAL_DOMAIN":      {kind: settingText},
	"BYPASS_URL":           {kind: settingText},
	"BYPASS_ENGINE":        {kind: settingEnum, allowed: bypassEngines},
	"BYPASS_USE_PROXY":     {kind: settingBool},
	"ENABLE_BT_ANTI_LEECH": {kind: settingBool},
	"ENABLE_AUTO_COMPLETE": {kind: settingBool},
	"LOG_RETENTION_DAYS":   {kind: settingInt},
}

// SettingsService validates the bounded setting set and encrypts secret values before persistence.
type SettingsService struct {
	repository      ports.SettingsRepository
	databaseDriver  string
	secrets         *secretCipher
	scheduleApplier ScheduleApplier
}

// ScheduleApplier 把最新设置中的定时任务表达式同步到调度器。返回值表示本次同步是否成功，
// 由 bootstrap 注入，使保存设置后无需重启后端即可生效。
type ScheduleApplier func(values map[string]string) error

// SetScheduleApplier 注入调度同步回调；必须在开始处理请求前调用，服务运行期间不再变更。
func (s *SettingsService) SetScheduleApplier(applier ScheduleApplier) {
	s.scheduleApplier = applier
}

// NewSettingsService builds a settings service. The session secret also protects persisted application secrets.
func NewSettingsService(repository ports.SettingsRepository, databaseDriver string, secret string) (*SettingsService, error) {
	if repository == nil {
		return nil, fmt.Errorf("settings repository is required")
	}
	secrets, err := newSecretCipher(secret)
	if err != nil {
		return nil, err
	}
	return &SettingsService{repository: repository, databaseDriver: databaseDriver, secrets: secrets}, nil
}

// Get returns all persisted values, decrypting sensitive values for the authenticated settings consumer.
func (s *SettingsService) Get(ctx context.Context) (domain.SystemSettings, error) {
	items, err := s.repository.List(ctx)
	if err != nil {
		return domain.SystemSettings{}, fmt.Errorf("list settings: %w", err)
	}
	result := domain.SystemSettings{DatabaseDriver: s.databaseDriver, Values: make(map[string]string, len(items)), Configured: map[string]bool{}}
	for _, item := range items {
		spec, allowed := writableSettings[item.Key]
		if !allowed || spec.secret != item.IsSecret {
			continue
		}
		if spec.secret {
			plain, err := s.decrypt(item.Value)
			if err != nil {
				// A development database may contain secrets encrypted with an older
				// SESSION_SECRET. One stale credential must not prevent the whole
				// application (including login and health checks) from starting.
				// Treat it as unavailable; the settings page can replace it explicitly.
				log.Printf("warning: unable to decrypt persisted setting %s; treating it as unset", item.Key)
				continue
			}
			result.Configured[item.Key] = strings.TrimSpace(plain) != ""
			result.Values[item.Key] = plain
			continue
		}
		result.Values[item.Key] = item.Value
	}
	// 未启用增强或历史配置未提供开关时，一律对外呈现关闭状态。
	if result.Values["BYPASS_ENGINE"] == "" || result.Values["BYPASS_USE_PROXY"] != "true" {
		result.Values["BYPASS_USE_PROXY"] = "false"
	}
	// Cookie 缺失或无法解密时，历史开关值也不能启动事件监听。
	if strings.TrimSpace(result.Values["PAN115_COOKIE"]) == "" || result.Values["PAN115_EVENT_ENABLE"] != "true" {
		result.Values["PAN115_EVENT_ENABLE"] = "false"
	}
	return result, nil
}

// Update validates and atomically persists supplied values. Blank values clear the corresponding setting,
// including encrypted secrets, so an administrator can intentionally remove a credential from the settings page.
func (s *SettingsService) Update(ctx context.Context, values map[string]string) (domain.SystemSettings, error) {
	items := make([]ports.StoredSetting, 0, len(values))
	for key, value := range values {
		spec, ok := writableSettings[key]
		if !ok {
			return domain.SystemSettings{}, fmt.Errorf("%w: %s", ErrInvalidSetting, key)
		}
		maxBytes := spec.maxBytes
		if maxBytes == 0 {
			maxBytes = 8192
		}
		if len(value) > maxBytes {
			return domain.SystemSettings{}, fmt.Errorf("%w: %s 最多允许 %d 字节（UTF-8），当前 %d 字节", ErrInvalidSetting, key, maxBytes, len(value))
		}
		value = strings.TrimSpace(value)
		if err := validateSettingValue(key, spec, value); err != nil {
			return domain.SystemSettings{}, err
		}
		if spec.secret {
			encrypted, err := s.encrypt(value)
			if err != nil {
				return domain.SystemSettings{}, err
			}
			value = encrypted
		}
		if key == strmDownloadExtensionsSettingKey {
			extensions, err := parseStrmDownloadExtensions(value)
			if err != nil {
				return domain.SystemSettings{}, fmt.Errorf("%w: %s %s", ErrInvalidSetting, key, err)
			}
			encoded, _ := json.Marshal(extensions)
			value = string(encoded)
		}
		items = append(items, ports.StoredSetting{Key: key, Value: value, IsSecret: spec.secret})
	}
	// 部分更新也遵循增强关闭则代理关闭；与本次设置使用同一批次持久化。
	_, changesEngine := values["BYPASS_ENGINE"]
	_, changesProxy := values["BYPASS_USE_PROXY"]
	if changesEngine || changesProxy {
		engine, supplied := values["BYPASS_ENGINE"]
		if !supplied {
			current, err := s.Get(ctx)
			if err != nil {
				return domain.SystemSettings{}, err
			}
			engine = current.Values["BYPASS_ENGINE"]
		}
		if strings.TrimSpace(engine) == "" || changesProxy && strings.TrimSpace(values["BYPASS_USE_PROXY"]) == "" {
			found := false
			for i := range items {
				if items[i].Key == "BYPASS_USE_PROXY" {
					items[i].Value = "false"
					found = true
				}
			}
			if !found {
				items = append(items, ports.StoredSetting{Key: "BYPASS_USE_PROXY", Value: "false"})
			}
		}
	}
	// 部分更新按最终 Cookie 判断；清空凭据与关闭监听在同一批次原子保存。
	_, changesCookie := values["PAN115_COOKIE"]
	_, changesListener := values["PAN115_EVENT_ENABLE"]
	if changesCookie || changesListener {
		cookie, supplied := values["PAN115_COOKIE"]
		if !supplied {
			current, err := s.Get(ctx)
			if err != nil {
				return domain.SystemSettings{}, err
			}
			cookie = current.Values["PAN115_COOKIE"]
		}
		if strings.TrimSpace(cookie) == "" {
			found := false
			for i := range items {
				if items[i].Key == "PAN115_EVENT_ENABLE" {
					items[i].Value = "false"
					found = true
				}
			}
			if !found {
				items = append(items, ports.StoredSetting{Key: "PAN115_EVENT_ENABLE", Value: "false"})
			}
		}
	}
	// Emby 媒体信息刷新依赖“生成后刷新媒体库”；部分保存也必须保持该不变式。
	_, changesEmbyRefresh := values[strmEmbyRefreshSettingKey]
	_, changesAfterRefresh := values[strmEmbyMediaAfterRefreshKey]
	if changesEmbyRefresh || changesAfterRefresh {
		refresh, supplied := values[strmEmbyRefreshSettingKey]
		if !supplied {
			current, err := s.Get(ctx)
			if err != nil {
				return domain.SystemSettings{}, err
			}
			refresh = current.Values[strmEmbyRefreshSettingKey]
		}
		if strings.TrimSpace(refresh) != "true" {
			found := false
			for i := range items {
				if items[i].Key == strmEmbyMediaAfterRefreshKey {
					items[i].Value = "false"
					found = true
				}
			}
			if !found {
				items = append(items, ports.StoredSetting{Key: strmEmbyMediaAfterRefreshKey, Value: "false"})
			}
		}
	}
	if len(items) > 0 {
		if err := s.repository.Upsert(ctx, items); err != nil {
			return domain.SystemSettings{}, fmt.Errorf("save settings: %w", err)
		}
	}
	saved, err := s.Get(ctx)
	if err != nil {
		return domain.SystemSettings{}, err
	}
	// 设置已落库，定时任务表达式需要立即生效：Apply 对未变化的表达式是幂等的，
	// 因此每次保存都重新对齐一次。同步失败只记录，不把已成功的保存报成失败，
	// 重启会按落库设置重新注册。
	if s.scheduleApplier != nil {
		if err := s.scheduleApplier(saved.Values); err != nil {
			log.Printf("warning: apply schedule after settings save failed: %v", err)
		}
	}
	return saved, nil
}

// pan115ScanPath 是 115 扫描目录设置 PAN115_SCAN_PATHS 的一个元素。
// ID 是 115 目录标识，是扫描时的权威依据；Path 是选择目录时的完整路径，只用于展示。
type pan115ScanPath struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// parsePan115ScanPaths 解析 PAN115_SCAN_PATHS 设置；空值表示没有配置任何扫描目录。
// 设置保存校验与扫描入库共用这一份实现，避免两套规则漂移。
// 返回的错误只说明原因，由调用方补充设置键等上下文。
func parsePan115ScanPaths(raw string) ([]pan115ScanPath, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		return nil, errors.New("需要是 JSON 数组")
	}
	var entries []pan115ScanPath
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return nil, errors.New("需要是 JSON 数组，元素为目录 ID 与目录路径")
	}
	seen := make(map[string]bool, len(entries))
	for index := range entries {
		entries[index].ID = strings.TrimSpace(entries[index].ID)
		entries[index].Path = strings.TrimSpace(entries[index].Path)
		entry := entries[index]
		if entry.ID == "" || entry.Path == "" {
			return nil, errors.New("每一项都需要包含目录 ID 与目录路径")
		}
		if seen[entry.ID] {
			return nil, errors.New("目录重复: " + entry.ID)
		}
		seen[entry.ID] = true
	}
	return entries, nil
}

// validateSettingValue 按配置项取值域校验输入；空值表示清除该项。
func validateSettingValue(key string, spec settingSpec, value string) error {
	if value == "" {
		return nil
	}
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s %s", ErrInvalidSetting, key, reason)
	}
	if key == "PAN115_COOKIE" && pan115.LifeCookieUserID(value) == "" {
		return invalid("需要有效的 UID、CID、SEID，且不能含换行")
	}
	switch spec.kind {
	case settingBool:
		if value != "true" && value != "false" {
			return invalid("只能是 true 或 false")
		}
	case settingInt:
		number, err := strconv.Atoi(value)
		if err != nil || number < 0 {
			return invalid("需要是非负整数")
		}
		if key == "PTT_UID" && (number == 0 || strings.Trim(value, "0123456789") != "") {
			return invalid("需要是正整数用户 ID")
		}
		if key == strmEmbyMediaIntervalSettingKey && (number < 1 || number > 10080) {
			return invalid("需要是 1 到 10080 分钟")
		}
	case settingJSON:
		var object map[string]any
		if err := json.Unmarshal([]byte(value), &object); err != nil {
			return invalid("需要是 JSON 对象")
		}
	case settingJSONArray:
		if key == "CLOUD_UPLOAD_PATHS" {
			if _, err := parseUploadMappings(value); err != nil {
				return invalid(err.Error())
			}
			return nil
		}
		if !strings.HasPrefix(value, "[") {
			return invalid("需要是 JSON 数组")
		}
		// 数组元素结构由各设置键自己的解析器定义，避免用一个通用结构解释所有数组设置。
		if key == strmPathsSettingKey {
			if _, err := parseStrmMappings(value); err != nil {
				return invalid(err.Error())
			}
			return nil
		}
		if _, err := parsePan115ScanPaths(value); err != nil {
			return invalid(err.Error())
		}
	case settingEnum:
		if !containsValue(spec.allowed, value) {
			return invalid("取值不在允许范围内: " + strings.Join(spec.allowed, ", "))
		}
	case settingSort:
		for _, tag := range strings.Split(value, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if !containsValue(sortTags, tag) {
				return invalid("排序标签无效: " + tag)
			}
		}
	case settingCron:
		if _, err := cron.ParseStandard(value); err != nil {
			return invalid("需要是合法的 5 段 cron 表达式（分 时 日 月 周）")
		}
	}
	return nil
}

func containsValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *SettingsService) encrypt(value string) (string, error) {
	return s.secrets.encrypt(value)
}

func (s *SettingsService) decrypt(value string) (string, error) {
	return s.secrets.decrypt(value)
}
