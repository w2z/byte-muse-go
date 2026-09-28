package application

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

var ErrInvalidSetting = errors.New("invalid setting")

// 配置项取值域。一个 key 只有一个权威声明，前端控件类型与保存校验都从这里推导。
const (
	settingText = "text" // 任意文本
	settingBool = "bool" // true / false
	settingInt  = "int"  // 非负整数
	settingJSON = "json" // JSON 对象
	settingEnum = "enum" // 封闭取值
	settingSort = "sort" // 逗号分隔的排序标签
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
var btDefaultDownloaderOptions = []string{"qbittorrent", "transmission", "aria2", "thunder"}

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

// settingSpec 描述一个可写配置项：是否敏感，以及取值范围。
type settingSpec struct {
	secret  bool
	kind    string
	allowed []string
}

// writableSettings 是可写配置项的唯一权威清单，key 与旧版 template.env、对标站 /config 完全一致。
// 顺序按对标站设置页的分组排列，便于逐组核对。
var writableSettings = map[string]settingSpec{
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

	// Telegram
	"TELEGRAM_BOT_TOKEN": {secret: true, kind: settingText},
	"TELEGRAM_CHAT_ID":   {kind: settingText},
	"TELEGRAM_WHITELIST": {kind: settingText},
	"TELEGRAM_SPOILER":   {kind: settingBool},

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

	// 过滤
	"DEFAULT_FILTER": {kind: settingJSON},

	// 排序
	"DEFAULT_SORT": {kind: settingSort},
	"MAIN_SITE":    {kind: settingEnum, allowed: mainSites},

	// 定时任务
	"RANK_PAGE":              {kind: settingInt},
	"RANK_TYPE":              {kind: settingEnum, allowed: rankTypes},
	"BRAND_TYPE":             {kind: settingText},
	"RANK_SCHEDULE_TIME":     {kind: settingText},
	"ACTOR_SCHEDULE_TIME":    {kind: settingText},
	"TAG_SCHEDULE_TIME":      {kind: settingText},
	"DOWNLOAD_SCHEDULE_TIME": {kind: settingText},
	"MAX_ACTOR":              {kind: settingInt},
	"TAG_MAX_SUB_PER_RUN":    {kind: settingInt},
	"PT_SEARCH_INTERVAL":     {kind: settingInt},

	// 翻译
	"BAIDU_APP_ID":       {kind: settingText},
	"BAIDU_API_KEY":      {secret: true, kind: settingText},
	"GOOGLE_API_KEY":     {secret: true, kind: settingText},
	"DEEPLX_URL":         {kind: settingText},
	"TRANSLATION_ENGINE": {kind: settingEnum, allowed: translationEngines},
	"TRANSLATION_PROMPT": {kind: settingText},

	// Agent
	"OPENAI_URL":          {kind: settingText},
	"OPENAI_MODEL":        {kind: settingText},
	"OPENAI_API_KEY":      {secret: true, kind: settingText},
	"AGENT_ENABLE":        {kind: settingBool},
	"AGENT_SYSTEM_PROMPT": {kind: settingText},

	// 其他
	"IMAGE_MODE":           {kind: settingEnum, allowed: imageModes},
	"PROXY":                {kind: settingText},
	"EXTERNAL_DOMAIN":      {kind: settingText},
	"BYPASS_URL":           {kind: settingText},
	"BYPASS_ENGINE":        {kind: settingEnum, allowed: bypassEngines},
	"JAVDB_HOST":           {kind: settingText},
	"ENABLE_BT_ANTI_LEECH": {kind: settingBool},
	"ENABLE_PHOTO_CACHE":   {kind: settingBool},
	"ENABLE_AUTO_COMPLETE": {kind: settingBool},
	"LOG_RETENTION_DAYS":   {kind: settingInt},
}

// SettingsService validates the bounded setting set and encrypts secret values before persistence.
type SettingsService struct {
	repository     ports.SettingsRepository
	databaseDriver string
	aead           cipher.AEAD
}

// NewSettingsService builds a settings service. The session secret also protects persisted application secrets.
func NewSettingsService(repository ports.SettingsRepository, databaseDriver string, secret string) (*SettingsService, error) {
	if repository == nil {
		return nil, fmt.Errorf("settings repository is required")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create settings cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create settings AEAD: %w", err)
	}
	return &SettingsService{repository: repository, databaseDriver: databaseDriver, aead: aead}, nil
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
	return result, nil
}

// Update validates and atomically persists supplied values. Blank values clear the corresponding setting,
// including encrypted secrets, so an administrator can intentionally remove a credential from the settings page.
func (s *SettingsService) Update(ctx context.Context, values map[string]string) (domain.SystemSettings, error) {
	items := make([]ports.StoredSetting, 0, len(values))
	for key, value := range values {
		spec, ok := writableSettings[key]
		if !ok || len(value) > 8192 {
			return domain.SystemSettings{}, fmt.Errorf("%w: %s", ErrInvalidSetting, key)
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
		items = append(items, ports.StoredSetting{Key: key, Value: value, IsSecret: spec.secret})
	}
	if len(items) > 0 {
		if err := s.repository.Upsert(ctx, items); err != nil {
			return domain.SystemSettings{}, fmt.Errorf("save settings: %w", err)
		}
	}
	return s.Get(ctx)
}

// validateSettingValue 按配置项取值域校验输入；空值表示清除该项。
func validateSettingValue(key string, spec settingSpec, value string) error {
	if value == "" {
		return nil
	}
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s %s", ErrInvalidSetting, key, reason)
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
	case settingJSON:
		var object map[string]any
		if err := json.Unmarshal([]byte(value), &object); err != nil {
			return invalid("需要是 JSON 对象")
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
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create settings nonce: %w", err)
	}
	sealed := s.aead.Seal(nil, nonce, []byte(value), nil)
	return base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func (s *SettingsService) decrypt(value string) (string, error) {
	encoded := strings.TrimSpace(value)
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode encrypted setting: %w", err)
	}
	nonceSize := s.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("encrypted setting is too short")
	}
	plain, err := s.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("open encrypted setting: %w", err)
	}
	return string(plain), nil
}
