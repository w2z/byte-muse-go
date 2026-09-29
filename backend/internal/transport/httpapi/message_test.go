package httpapi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"bytemuse/backend/internal/platform/wechat"
	"bytemuse/backend/internal/ports"
)

// recordingChannelMessages 记录回调层解析出的入站消息。
type recordingChannelMessages struct {
	mu       sync.Mutex
	messages []ports.InboundMessage
	done     chan struct{}
}

func newRecordingChannelMessages() *recordingChannelMessages {
	return &recordingChannelMessages{done: make(chan struct{}, 4)}
}

func (r *recordingChannelMessages) Handle(_ context.Context, msg ports.InboundMessage) error {
	r.mu.Lock()
	r.messages = append(r.messages, msg)
	r.mu.Unlock()
	r.done <- struct{}{}
	return nil
}

// wechatEncrypt 按企业微信规则构造密文，仅用于测试，与生产解密实现互为逆运算。
func wechatEncrypt(t *testing.T, token, aesKey, corpID, plain string) (encrypt, signature, timestamp, nonce string) {
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
	if padding == 0 {
		padding = 32
	}
	content = append(content, bytes.Repeat([]byte{byte(padding)}, padding)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(content))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, content)
	encrypt = base64.StdEncoding.EncodeToString(ciphertext)
	timestamp, nonce = "1700000000", "nonce-test"
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

// newWeChatCallback 构造绑定真实加解密器的回调处理器。
func newWeChatCallback(t *testing.T) ports.CallbackVerifier {
	t.Helper()
	crypto, err := wechat.NewCrypto(testWeChatToken, testWeChatAESKey, testWeChatCorpID)
	if err != nil {
		t.Fatal(err)
	}
	return wechat.NewCallback(crypto)
}

// TestMessageEndpointRequiresConfiguration 验证未配置企业微信时回调端点返回 503 而不是 401/404。
func TestMessageEndpointRequiresConfiguration(t *testing.T) {
	h := New(Dependencies{})
	for _, method := range []string{"GET", "POST"} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(method, "/api/v1/message?signature=x", strings.NewReader("")))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s 未配置期望 503，实际 %d", method, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "wechat_not_configured") {
			t.Fatalf("%s 响应体未说明未配置: %s", method, recorder.Body.String())
		}
	}
}

// TestMessageEndpointVerifyURL 验证 GET 回调地址校验：签名正确时原样回写明文，签名错误时 400。
func TestMessageEndpointVerifyURL(t *testing.T) {
	h := New(Dependencies{WeChatCallback: newWeChatCallback(t)})
	echo := "echo-plain-text"
	encrypt, signature, timestamp, nonce := wechatEncrypt(t, testWeChatToken, testWeChatAESKey, testWeChatCorpID, echo)
	query := "?" + url.Values{
		"msg_signature": {signature},
		"timestamp":     {timestamp},
		"nonce":         {nonce},
		"echostr":       {encrypt},
	}.Encode()

	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/message"+query, nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != echo {
		t.Fatalf("校验回调期望 200 与 %q，实际 %d %q", echo, recorder.Code, recorder.Body.String())
	}

	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest("GET", "/api/v1/message?"+url.Values{"msg_signature": {"deadbeef"}, "timestamp": {"1"}, "nonce": {"2"}, "echostr": {encrypt}}.Encode(), nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("错误签名期望 400，实际 %d", bad.Code)
	}
}

// TestMessageEndpointReceiveDecryptsAndDispatches 验证 POST 回调解密后交给渠道处理器，且无需会话即可访问。
func TestMessageEndpointReceiveDecryptsAndDispatches(t *testing.T) {
	handler := newRecordingChannelMessages()
	h := New(Dependencies{WeChatCallback: newWeChatCallback(t), ChannelMessages: handler})

	inner := `<xml><ToUserName>ww-test-corp</ToUserName><FromUserName>zhangsan</FromUserName><MsgType>text</MsgType><Content>查看下载进度</Content></xml>`
	encrypt, signature, timestamp, nonce := wechatEncrypt(t, testWeChatToken, testWeChatAESKey, testWeChatCorpID, inner)
	body := "<xml><ToUserName>ww-test-corp</ToUserName><Encrypt><![CDATA[" + encrypt + "]]></Encrypt></xml>"
	query := "?" + url.Values{"msg_signature": {signature}, "timestamp": {timestamp}, "nonce": {nonce}}.Encode()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/v1/message"+query, strings.NewReader(body))
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("回调解密期望 200，实际 %d %s", recorder.Code, recorder.Body.String())
	}

	select {
	case <-handler.done:
	case <-time.After(2 * time.Second):
		t.Fatal("回调未在超时内交给渠道处理器")
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.messages) != 1 {
		t.Fatalf("期望 1 条入站消息，实际 %d", len(handler.messages))
	}
	got := handler.messages[0]
	if got.Channel != ports.ChannelWeChat || got.ChatID != "zhangsan" || got.UserID != "zhangsan" || got.Text != "查看下载进度" {
		t.Fatalf("入站消息不符: %#v", got)
	}
}

// TestMessageEndpointRejectsTamperedBody 验证签名不符的 POST 回调返回 400 且不投递。
func TestMessageEndpointRejectsTamperedBody(t *testing.T) {
	handler := newRecordingChannelMessages()
	h := New(Dependencies{WeChatCallback: newWeChatCallback(t), ChannelMessages: handler})

	encrypt, _, timestamp, nonce := wechatEncrypt(t, testWeChatToken, testWeChatAESKey, testWeChatCorpID, "<xml><Content>hi</Content></xml>")
	body := "<xml><Encrypt><![CDATA[" + encrypt + "]]></Encrypt></xml>"
	query := "?" + url.Values{"msg_signature": {strings.Repeat("0", 40)}, "timestamp": {timestamp}, "nonce": {nonce}}.Encode()

	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/v1/message"+query, strings.NewReader(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("篡改请求期望 400，实际 %d", recorder.Code)
	}
	select {
	case <-handler.done:
		t.Fatal("非法回调不应投递消息")
	case <-time.After(100 * time.Millisecond):
	}
}
