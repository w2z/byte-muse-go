package wechat

import (
	"net/url"
	"testing"

	"bytemuse/backend/internal/ports"
)

// callbackFixture 生成一份可直接喂给 Callback 的加密回调数据。
func callbackFixture(t *testing.T, crypt *Crypto, key []byte, plaintext string) (url.Values, []byte) {
	t.Helper()
	encrypt := encryptMessage(t, key, plaintext, testCorpID)
	query := url.Values{}
	query.Set("msg_signature", signFixture(testToken, "1717000000", "nonce-value", encrypt))
	query.Set("timestamp", "1717000000")
	query.Set("nonce", "nonce-value")
	return query, []byte("<xml><Encrypt><![CDATA[" + encrypt + "]]></Encrypt></xml>")
}

func TestCallbackVerifyReturnsEcho(t *testing.T) {
	crypt, key := newTestCrypto(t)
	callback := NewCallback(crypt)
	echo := "1616140317555161061"
	encrypt := encryptMessage(t, key, echo, testCorpID)
	query := url.Values{}
	query.Set("msg_signature", signFixture(testToken, "1717000000", "nonce-value", encrypt))
	query.Set("timestamp", "1717000000")
	query.Set("nonce", "nonce-value")
	query.Set("echostr", encrypt)

	plain, err := callback.Verify(query)
	if err != nil {
		t.Fatalf("URL 校验失败：%v", err)
	}
	if plain != echo {
		t.Fatalf("回写明文不匹配：%q", plain)
	}
}

func TestCallbackVerifyRequiresAllParameters(t *testing.T) {
	crypt, _ := newTestCrypto(t)
	callback := NewCallback(crypt)
	complete := url.Values{}
	complete.Set("msg_signature", "signature")
	complete.Set("timestamp", "1717000000")
	complete.Set("nonce", "nonce-value")
	complete.Set("echostr", "encrypt-value")

	for _, missing := range []string{"msg_signature", "timestamp", "nonce", "echostr"} {
		query := url.Values{}
		for key, values := range complete {
			if key != missing {
				query[key] = values
			}
		}
		if _, err := callback.Verify(query); err == nil {
			t.Fatalf("缺少 %s 时必须返回错误", missing)
		}
	}
}

func TestCallbackReceiveTextMessage(t *testing.T) {
	crypt, key := newTestCrypto(t)
	callback := NewCallback(crypt)
	plaintext := "<xml><ToUserName>ww0123456789abcdef</ToUserName>" +
		"<FromUserName>zhangsan</FromUserName><MsgType>text</MsgType>" +
		"<Content>ABC-123</Content><MsgId>1234567890</MsgId></xml>"
	query, body := callbackFixture(t, crypt, key, plaintext)

	message, err := callback.Receive(query, body)
	if err != nil {
		t.Fatalf("解析回调失败：%v", err)
	}
	if message.Channel != ports.ChannelWeChat {
		t.Fatalf("渠道标识=%q，期望 %q", message.Channel, ports.ChannelWeChat)
	}
	if message.ChatID != "zhangsan" || message.UserID != "zhangsan" {
		t.Fatalf("发送者标识不正确：%#v", message)
	}
	if message.Text != "ABC-123" {
		t.Fatalf("消息内容=%q，期望 ABC-123", message.Text)
	}
	if message.SessionKey() != ports.ChannelWeChat+":zhangsan" {
		t.Fatalf("会话标识不正确：%q", message.SessionKey())
	}
}

func TestCallbackReceiveNonTextMessage(t *testing.T) {
	crypt, key := newTestCrypto(t)
	callback := NewCallback(crypt)
	plaintext := "<xml><FromUserName>zhangsan</FromUserName><MsgType>event</MsgType><Event>click</Event></xml>"
	query, body := callbackFixture(t, crypt, key, plaintext)

	message, err := callback.Receive(query, body)
	if err != nil {
		t.Fatalf("非文本消息不应报错：%v", err)
	}
	if message.Text != "" {
		t.Fatalf("非文本消息应返回空文本，实际 %q", message.Text)
	}
	if message.Channel != ports.ChannelWeChat || message.ChatID != "zhangsan" {
		t.Fatalf("非文本消息仍应保留来源信息：%#v", message)
	}
}

func TestCallbackReceiveRejectsBadSignature(t *testing.T) {
	crypt, key := newTestCrypto(t)
	callback := NewCallback(crypt)
	query, body := callbackFixture(t, crypt, key, "<xml><MsgType>text</MsgType></xml>")
	query.Set("msg_signature", "0000000000000000000000000000000000000000")

	if _, err := callback.Receive(query, body); err == nil {
		t.Fatal("签名不匹配时必须返回错误")
	}
}

func TestCallbackWithoutCryptoReturnsError(t *testing.T) {
	callback := NewCallback(nil)
	if _, err := callback.Verify(url.Values{}); err == nil {
		t.Fatal("未配置加解密器时校验必须返回错误")
	}
	if _, err := callback.Receive(url.Values{}, nil); err == nil {
		t.Fatal("未配置加解密器时接收必须返回错误")
	}
}
