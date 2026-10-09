package bootstrap

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/ports"
)

// transitionNotifier 捕获终态装配入口交给渠道的业务消息。
type transitionNotifier struct {
	events   []application.NotificationEvent
	messages []application.NotificationMessage
}

func (notifier *transitionNotifier) Notify(_ context.Context, event application.NotificationEvent, message application.NotificationMessage) {
	notifier.events = append(notifier.events, event)
	notifier.messages = append(notifier.messages, message)
}

func (notifier *transitionNotifier) ChannelEventEnabled(context.Context, string, application.NotificationEvent) bool {
	return true
}

// TestTransferNotificationsUseResourceSnapshot 保证完成和失败事件复用标题置顶模板并带上任务资源。
func TestTransferNotificationsUseResourceSnapshot(t *testing.T) {
	notifier := &transitionNotifier{}
	for _, status := range []string{"downloading", "completed", "failed"} {
		notifyTransferTransitions(context.Background(), notifier, []ports.TransferTransition{{Code: "EXAMPLE-001", Title: "资源简介", Site: "示例站点", Kind: "bt", URI: "https://example.com/download", Status: status}})
	}
	if len(notifier.messages) != 2 || notifier.events[0] != application.NotificationDownloadComplete || notifier.events[1] != application.NotificationDownloadFailed {
		t.Fatalf("通知事件 = %v", notifier.events)
	}
	for index, expected := range []string{
		"标题: 资源简介\n番号: EXAMPLE-001\n状态: 已完成下载\n站点: 示例站点\n来源: BT\n下载链接: https://example.com/download\n描述: 资源简介",
		"标题: 资源简介\n番号: EXAMPLE-001\n状态: 下载失败\n站点: 示例站点\n来源: BT\n下载链接: https://example.com/download\n描述: 资源简介 原因：下载器报告任务失败",
	} {
		message := notifier.messages[index]
		if got := application.NotificationPlainText(message.Title, message.Text); got != expected {
			t.Fatalf("通知 = %q，期望 %q", got, expected)
		}
	}
}

// stubMessageHandler 用函数实现 ports.ChannelMessageHandler，供监督器测试注入。
type stubMessageHandler func(context.Context, ports.InboundMessage) error

func (h stubMessageHandler) Handle(ctx context.Context, msg ports.InboundMessage) error {
	return h(ctx, msg)
}

// memorySettingsRepository 是设置仓储的内存替身，供装配层测试写入并读回配置。
type memorySettingsRepository struct {
	mu    sync.Mutex
	items map[string]ports.StoredSetting
}

func newMemorySettingsRepository() *memorySettingsRepository {
	return &memorySettingsRepository{items: map[string]ports.StoredSetting{}}
}

func (r *memorySettingsRepository) List(context.Context) ([]ports.StoredSetting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ports.StoredSetting, 0, len(r.items))
	for _, item := range r.items {
		out = append(out, item)
	}
	return out, nil
}

func (r *memorySettingsRepository) Upsert(_ context.Context, items []ports.StoredSetting) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range items {
		r.items[item.Key] = item
	}
	return nil
}

// newTestSettings 返回绑定内存仓储的设置服务，写入的敏感值走与生产一致的加密路径。
func newTestSettings(t *testing.T) *application.SettingsService {
	t.Helper()
	service, err := application.NewSettingsService(newMemorySettingsRepository(), "sqlite", strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func saveSettings(t *testing.T, service *application.SettingsService, values map[string]string) {
	t.Helper()
	if _, err := service.Update(context.Background(), values); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
}

// TestChannelRegistryResolvesConfiguredSenders 验证渠道发送器按当前设置解析，并随凭据变化重建。
func TestChannelRegistryResolvesConfiguredSenders(t *testing.T) {
	settings := newTestSettings(t)
	registry := newChannelRegistry(settings)

	if _, ok := registry.Sender(ports.ChannelTelegram); ok {
		t.Fatal("未配置时不应解析出 Telegram 发送器")
	}
	if _, ok := registry.Sender(ports.ChannelWeChat); ok {
		t.Fatal("未配置时不应解析出企业微信发送器")
	}
	if _, ok := registry.Sender("unknown"); ok {
		t.Fatal("未知渠道不应解析出发送器")
	}

	saveSettings(t, settings, map[string]string{"TELEGRAM_BOT_TOKEN": "123:abc"})
	first, ok := registry.Sender(ports.ChannelTelegram)
	if !ok {
		t.Fatal("配置后应解析出 Telegram 发送器")
	}
	again, _ := registry.Sender(ports.ChannelTelegram)
	if first != again {
		t.Fatal("同一配置应复用同一发送器实例")
	}
	saveSettings(t, settings, map[string]string{"TELEGRAM_BOT_TOKEN": "456:def"})
	replaced, _ := registry.Sender(ports.ChannelTelegram)
	if replaced == first {
		t.Fatal("Bot Token 变化后应重建发送器")
	}

	saveSettings(t, settings, map[string]string{
		"WECHAT_CORP_ID":     "ww-test-corp",
		"WECHAT_CORP_SECRET": "secret-value",
		"WECHAT_AGENT_ID":    "1000002",
	})
	if _, ok := registry.Sender(ports.ChannelWeChat); !ok {
		t.Fatal("企业微信三项凭据齐全时应解析出发送器")
	}
	saveSettings(t, settings, map[string]string{"WECHAT_AGENT_ID": ""})
	if _, ok := registry.Sender(ports.ChannelWeChat); ok {
		t.Fatal("企业微信凭据不完整时不应解析出发送器")
	}
}

// TestChannelRegistryRebuildsTelegramSenderOnSpoilerChange 验证「图片防剧透」参与发送器签名，
// 保存设置后无需重启即可生效，且同一配置继续复用同一实例。
func TestChannelRegistryRebuildsTelegramSenderOnSpoilerChange(t *testing.T) {
	settings := newTestSettings(t)
	registry := newChannelRegistry(settings)
	saveSettings(t, settings, map[string]string{"TELEGRAM_BOT_TOKEN": "123:abc"})
	plain, ok := registry.Sender(ports.ChannelTelegram)
	if !ok {
		t.Fatal("配置 Bot Token 后应解析出 Telegram 发送器")
	}
	saveSettings(t, settings, map[string]string{"TELEGRAM_SPOILER": "true"})
	spoilered, _ := registry.Sender(ports.ChannelTelegram)
	if spoilered == plain {
		t.Fatal("防剧透开关变化后应重建发送器")
	}
	if again, _ := registry.Sender(ports.ChannelTelegram); again != spoilered {
		t.Fatal("同一配置应复用同一发送器实例")
	}
}

// wechatTestEncrypt 构造企业微信回调密文，用于验证设置驱动的回调链路。
func wechatTestEncrypt(t *testing.T, token, aesKey, corpID, plain string) (string, string, string, string) {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(aesKey + "=")
	if err != nil || len(key) != 32 {
		t.Fatalf("测试密钥无效: %v", err)
	}
	content := make([]byte, 16)
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(plain)))
	content = append(content, length...)
	content = append(content, []byte(plain)...)
	content = append(content, []byte(corpID)...)
	padding := 32 - len(content)%32
	content = append(content, []byte(strings.Repeat(string(rune(padding)), padding))...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(content))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, content)
	encrypt := base64.StdEncoding.EncodeToString(ciphertext)
	timestamp, nonce := "1700000000", "nonce-test"
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return encrypt, hex.EncodeToString(sum[:]), timestamp, nonce
}

const (
	testWeChatToken  = "callback-token"
	testWeChatAESKey = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	testWeChatCorpID = "ww-test-corp"
)

// TestWeChatCallbackProviderFollowsSettings 验证回调处理器按当前设置解析，未配置时返回 ErrChannelNotConfigured。
func TestWeChatCallbackProviderFollowsSettings(t *testing.T) {
	settings := newTestSettings(t)
	provider := newWeChatCallback(settings)

	if _, err := provider.Verify(url.Values{}); !errors.Is(err, ports.ErrChannelNotConfigured) {
		t.Fatalf("未配置期望 ErrChannelNotConfigured，实际 %v", err)
	}

	saveSettings(t, settings, map[string]string{
		"WECHAT_TOKEN":            testWeChatToken,
		"WECHAT_ENCODING_AES_KEY": testWeChatAESKey,
		"WECHAT_CORP_ID":          testWeChatCorpID,
	})

	echo := "echo-plain"
	encrypt, signature, timestamp, nonce := wechatTestEncrypt(t, testWeChatToken, testWeChatAESKey, testWeChatCorpID, echo)
	verified, err := provider.Verify(url.Values{
		"msg_signature": {signature},
		"timestamp":     {timestamp},
		"nonce":         {nonce},
		"echostr":       {encrypt},
	})
	if err != nil || verified != echo {
		t.Fatalf("回调校验失败: %q %v", verified, err)
	}

	inner := `<xml><FromUserName>zhangsan</FromUserName><MsgType>text</MsgType><Content>查看下载进度</Content></xml>`
	encrypt, signature, timestamp, nonce = wechatTestEncrypt(t, testWeChatToken, testWeChatAESKey, testWeChatCorpID, inner)
	body, err := xml.Marshal(struct {
		XMLName xml.Name `xml:"xml"`
		Encrypt string   `xml:"Encrypt"`
	}{Encrypt: encrypt})
	if err != nil {
		t.Fatal(err)
	}
	message, err := provider.Receive(url.Values{
		"msg_signature": {signature},
		"timestamp":     {timestamp},
		"nonce":         {nonce},
	}, body)
	if err != nil {
		t.Fatalf("回调解密失败: %v", err)
	}
	if message.Channel != ports.ChannelWeChat || message.ChatID != "zhangsan" || message.Text != "查看下载进度" {
		t.Fatalf("入站消息不符: %#v", message)
	}

	saveSettings(t, settings, map[string]string{"WECHAT_TOKEN": ""})
	if _, err := provider.Verify(url.Values{}); !errors.Is(err, ports.ErrChannelNotConfigured) {
		t.Fatalf("清空 token 后期望 ErrChannelNotConfigured，实际 %v", err)
	}
}

// TestParseTelegramWhitelist 验证白名单解析忽略空白项。
func TestParseTelegramWhitelist(t *testing.T) {
	got := parseTelegramWhitelist(" 123 , 456 ,,789 ")
	want := []string{"123", "456", "789"}
	if len(got) != len(want) {
		t.Fatalf("白名单项数不符: %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("白名单第 %d 项不符: %#v", index, got)
		}
	}
	if len(parseTelegramWhitelist("")) != 0 {
		t.Fatal("空白名单应解析为空")
	}
}

// TestRunChannelSupervisorIdleWithoutToken 验证未配置 Bot Token 时监督器保持空闲并能按 ctx 结束。
func TestRunChannelSupervisorIdleWithoutToken(t *testing.T) {
	settings := newTestSettings(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunChannelSupervisor(ctx, settings, stubMessageHandler(func(context.Context, ports.InboundMessage) error {
			t.Error("未配置时不应处理消息")
			return nil
		}))
	}()
	time.Sleep(120 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("监督器未按 ctx 结束")
	}
}
