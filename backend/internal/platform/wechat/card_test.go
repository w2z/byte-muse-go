package wechat

import (
	"context"
	"strings"
	"testing"

	"bytemuse/backend/internal/ports"
)

// templateCardOf 校验消息类型并取出 template_card 请求体。
func templateCardOf(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	if payload["msgtype"] != "template_card" {
		t.Fatalf("msgtype=%v，期望 template_card", payload["msgtype"])
	}
	card, ok := payload["template_card"].(map[string]any)
	if !ok {
		t.Fatalf("请求体缺少 template_card：%#v", payload)
	}
	return card
}

// cardButtonsOf 取出模板卡片的按钮列表。
func cardButtonsOf(t *testing.T, card map[string]any) []any {
	t.Helper()
	buttons, ok := card["button_list"].([]any)
	if !ok {
		t.Fatalf("模板卡片缺少 button_list：%#v", card)
	}
	return buttons
}

func TestSendCardBuildsTemplateCardPayload(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)
	card := ports.OutboundCard{
		PhotoURL: "https://example.test/SSIS-001.jpg",
		Title:    "SSIS-001",
		Text:     "中文标题\n状态：未订阅",
		Buttons: []ports.ActionButton{
			{Label: "订阅", Data: "bm:sub:SSIS-001"},
			{Label: "取消订阅", Data: "bm:unsub:SSIS-001"},
		},
	}
	if err := client.SendCard(context.Background(), "zhangsan", card); err != nil {
		t.Fatalf("发送模板卡片失败：%v", err)
	}

	payloads := fake.snapshot()
	if len(payloads) != 1 {
		t.Fatalf("发送次数=%d，期望 1", len(payloads))
	}
	payload := payloads[0]
	if payload["touser"] != "zhangsan" || payload["agentid"] != "1000002" {
		t.Fatalf("接收者或应用标识不符：%#v", payload)
	}
	body := templateCardOf(t, payload)
	if body["card_type"] != "button_interaction" {
		t.Fatalf("card_type=%v，期望 button_interaction", body["card_type"])
	}
	image, _ := body["card_image"].(map[string]any)
	if image["url"] != card.PhotoURL {
		t.Fatalf("card_image=%#v", body["card_image"])
	}
	if ratio, _ := image["aspect_ratio"].(float64); ratio != cardAspectRatio {
		t.Fatalf("aspect_ratio=%v，期望 %v", image["aspect_ratio"], cardAspectRatio)
	}
	main, _ := body["main_title"].(map[string]any)
	if main["title"] != "SSIS-001" || main["desc"] != card.Text {
		t.Fatalf("main_title=%#v", main)
	}
	buttons := cardButtonsOf(t, body)
	if len(buttons) != 2 {
		t.Fatalf("按钮数=%d，期望 2", len(buttons))
	}
	first, _ := buttons[0].(map[string]any)
	if first["text"] != "订阅" || first["key"] != "bm:sub:SSIS-001" {
		t.Fatalf("第一个按钮=%#v", first)
	}
}

// TestSendCardOmitsImageWithoutPhotoURL 验证没有封面时不发送 card_image，卡片仍可点击。
func TestSendCardOmitsImageWithoutPhotoURL(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)
	card := ports.OutboundCard{Title: "SSIS-001", Text: "状态：未订阅", Buttons: []ports.ActionButton{{Label: "订阅", Data: "bm:sub:SSIS-001"}}}
	if err := client.SendCard(context.Background(), "zhangsan", card); err != nil {
		t.Fatalf("发送模板卡片失败：%v", err)
	}
	body := templateCardOf(t, fake.snapshot()[0])
	if _, exists := body["card_image"]; exists {
		t.Fatalf("缺少封面时不应发送 card_image：%#v", body)
	}
	if buttons := cardButtonsOf(t, body); len(buttons) != 1 {
		t.Fatalf("按钮数=%d，期望 1", len(buttons))
	}
}

// TestSendCardRejectsButtonCountOutsidePlatformLimit 验证按钮数量超出平台上限时返回错误且不发起请求。
func TestSendCardRejectsButtonCountOutsidePlatformLimit(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)

	if err := client.SendCard(context.Background(), "zhangsan", ports.OutboundCard{Title: "SSIS-001"}); err == nil {
		t.Fatal("缺少按钮时必须返回错误")
	}
	tooMany := make([]ports.ActionButton, 0, maxCardButtons+1)
	for index := 0; index <= maxCardButtons; index++ {
		tooMany = append(tooMany, ports.ActionButton{Label: "操作", Data: "bm:sub:SSIS-001"})
	}
	if err := client.SendCard(context.Background(), "zhangsan", ports.OutboundCard{Title: "SSIS-001", Buttons: tooMany}); err == nil {
		t.Fatal("按钮超过上限时必须返回错误")
	}
	if _, sendCalls := fake.counts(); sendCalls != 0 {
		t.Fatalf("非法卡片不应发起请求，实际发送 %d 次", sendCalls)
	}
}

// TestSendCardTruncatesLongCopy 验证超长标题与正文按平台上限截断，避免整张卡片被拒绝。
func TestSendCardTruncatesLongCopy(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)
	card := ports.OutboundCard{
		Title:   strings.Repeat("号", maxCardTitleRunes+20),
		Text:    strings.Repeat("文", maxCardDescRunes+20),
		Buttons: []ports.ActionButton{{Label: "订阅", Data: "bm:sub:SSIS-001"}},
	}
	if err := client.SendCard(context.Background(), "zhangsan", card); err != nil {
		t.Fatalf("发送模板卡片失败：%v", err)
	}
	main, _ := templateCardOf(t, fake.snapshot()[0])["main_title"].(map[string]any)
	if title, _ := main["title"].(string); len([]rune(title)) > maxCardTitleRunes {
		t.Fatalf("标题长度=%d，超过上限 %d", len([]rune(title)), maxCardTitleRunes)
	}
	if desc, _ := main["desc"].(string); len([]rune(desc)) > maxCardDescRunes {
		t.Fatalf("正文长度=%d，超过上限 %d", len([]rune(desc)), maxCardDescRunes)
	}
}

// TestReplaceButtonsIsNoopForWeChat 验证企业微信不维护卡片更新映射，替换按钮按约定返回 nil 且不发请求。
func TestReplaceButtonsIsNoopForWeChat(t *testing.T) {
	fake := &fakeWeChat{}
	client := newTestClient(t, fake, "", false)
	if err := client.ReplaceButtons(context.Background(), "zhangsan", "1", []ports.ActionButton{{Label: "订阅", Data: "bm:sub:SSIS-001"}}); err != nil {
		t.Fatalf("ReplaceButtons 应返回 nil，实际 %v", err)
	}
	if _, sendCalls := fake.counts(); sendCalls != 0 {
		t.Fatalf("企业微信不支持更新卡片，不应发起请求，实际 %d 次", sendCalls)
	}
}

// TestCallbackReceiveTemplateCardEvent 验证模板卡片按钮点击被解析为业务动作。
func TestCallbackReceiveTemplateCardEvent(t *testing.T) {
	crypt, key := newTestCrypto(t)
	callback := NewCallback(crypt)
	plaintext := "<xml><FromUserName>zhangsan</FromUserName><MsgType>event</MsgType>" +
		"<Event>template_card_event</Event><EventKey>bm:pause:SSIS-001</EventKey></xml>"
	query, body := callbackFixture(t, crypt, key, plaintext)

	message, err := callback.Receive(query, body)
	if err != nil {
		t.Fatalf("解析按钮事件失败：%v", err)
	}
	if message.Text != "" {
		t.Fatalf("按钮事件不应携带文本：%q", message.Text)
	}
	if message.Action == nil || message.Action.Data != "bm:pause:SSIS-001" {
		t.Fatalf("按钮动作=%+v", message.Action)
	}
	if message.Channel != ports.ChannelWeChat || message.ChatID != "zhangsan" || message.SessionKey() != ports.ChannelWeChat+":zhangsan" {
		t.Fatalf("来源信息不正确：%#v", message)
	}
}

// TestCallbackIgnoresTemplateCardEventWithoutKey 验证缺少 EventKey 的事件不产生动作，避免误触发业务。
func TestCallbackIgnoresTemplateCardEventWithoutKey(t *testing.T) {
	crypt, key := newTestCrypto(t)
	callback := NewCallback(crypt)
	plaintext := "<xml><FromUserName>zhangsan</FromUserName><MsgType>event</MsgType><Event>template_card_event</Event></xml>"
	query, body := callbackFixture(t, crypt, key, plaintext)

	message, err := callback.Receive(query, body)
	if err != nil {
		t.Fatalf("解析事件失败：%v", err)
	}
	if message.Action != nil {
		t.Fatalf("缺少 EventKey 不应产生动作：%+v", message.Action)
	}
}
