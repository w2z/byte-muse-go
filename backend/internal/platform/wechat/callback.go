package wechat

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"bytemuse/backend/internal/ports"
)

// Callback 处理企业微信回调：GET 用于 URL 校验，POST 用于接收消息。
// 只负责协议层校验与解析，业务分流由调用方交给 application 层的 Router。
type Callback struct {
	crypto *Crypto
}

// NewCallback 绑定回调加解密器；未配置企业微信时传入 nil，回调入口会返回明确错误而不是 panic。
func NewCallback(crypto *Crypto) *Callback {
	return &Callback{crypto: crypto}
}

// Verify 校验企业微信服务器 URL 校验请求，返回需要原样回写的明文 echostr。
func (cb *Callback) Verify(query url.Values) (string, error) {
	if cb == nil || cb.crypto == nil {
		return "", errors.New("企业微信回调加解密器未初始化")
	}
	signature, timestamp, nonce, echostr, err := callbackParams(query, true)
	if err != nil {
		return "", err
	}
	return cb.crypto.VerifyURL(signature, timestamp, nonce, echostr)
}

// templateCardEvent 是模板卡片按钮点击的事件名，EventKey 即按钮的 key。
const templateCardEvent = "template_card_event"

// Receive 解析 POST 回调并返回统一入站消息。
// 文本消息保留原始内容不做裁剪；模板卡片按钮点击解析为 Action；其余消息返回空内容且不报错，
// 由调用方决定是否忽略。
func (cb *Callback) Receive(query url.Values, body []byte) (ports.InboundMessage, error) {
	if cb == nil || cb.crypto == nil {
		return ports.InboundMessage{}, errors.New("企业微信回调加解密器未初始化")
	}
	signature, timestamp, nonce, _, err := callbackParams(query, false)
	if err != nil {
		return ports.InboundMessage{}, err
	}
	plain, err := cb.crypto.Decrypt(signature, timestamp, nonce, body)
	if err != nil {
		return ports.InboundMessage{}, err
	}
	var inbound inboundEnvelope
	if err := xml.Unmarshal(plain, &inbound); err != nil {
		return ports.InboundMessage{}, fmt.Errorf("解析企业微信消息失败：%w", err)
	}
	sender := strings.TrimSpace(inbound.FromUserName)
	message := ports.InboundMessage{
		Channel: ports.ChannelWeChat,
		ChatID:  sender,
		UserID:  sender,
	}
	msgType := strings.TrimSpace(inbound.MsgType)
	if msgType == "event" && strings.TrimSpace(inbound.Event) == templateCardEvent {
		if key := strings.TrimSpace(inbound.EventKey); key != "" {
			message.Action = &ports.InboundAction{Data: key}
		}
		return message, nil
	}
	if msgType != "text" {
		return message, nil
	}
	message.Text = inbound.Content
	return message, nil
}

// callbackParams 读取回调公共参数；needEcho 为真时要求 echostr 存在（URL 校验场景）。
// 缺少任一必需参数都返回明确错误，避免把无效请求当成签名失败而掩盖配置问题。
func callbackParams(query url.Values, needEcho bool) (signature, timestamp, nonce, echostr string, err error) {
	signature = strings.TrimSpace(query.Get("msg_signature"))
	timestamp = strings.TrimSpace(query.Get("timestamp"))
	nonce = strings.TrimSpace(query.Get("nonce"))
	echostr = strings.TrimSpace(query.Get("echostr"))
	switch {
	case signature == "":
		return "", "", "", "", errors.New("企业微信回调缺少 msg_signature 参数")
	case timestamp == "":
		return "", "", "", "", errors.New("企业微信回调缺少 timestamp 参数")
	case nonce == "":
		return "", "", "", "", errors.New("企业微信回调缺少 nonce 参数")
	case needEcho && echostr == "":
		return "", "", "", "", errors.New("企业微信回调缺少 echostr 参数")
	}
	return signature, timestamp, nonce, echostr, nil
}

// inboundEnvelope 是企业微信回调的明文字段子集，其余字段不参与业务分流。
type inboundEnvelope struct {
	XMLName      xml.Name `xml:"xml"`
	MsgType      string   `xml:"MsgType"`
	Event        string   `xml:"Event"`
	EventKey     string   `xml:"EventKey"`
	Content      string   `xml:"Content"`
	FromUserName string   `xml:"FromUserName"`
}
