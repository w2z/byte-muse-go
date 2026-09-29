package application

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
)

// captureLogs 把包级日志重定向到内存缓冲，返回缓冲与恢复原日志器的函数。
func captureLogs() (*bytes.Buffer, func()) {
	previous := logging.Default
	buffer := &bytes.Buffer{}
	logging.Default = logging.New(buffer)
	return buffer, func() { logging.Default = previous }
}

// TestSubscriptionLogsUseCodeInsteadOfInternalIDs 约束订阅日志用番号标识对象。
// 订阅 ID 与媒体 ID 都是不对外暴露的内部标识，写进日志无法与任何界面元素对应，
// 因此订阅日志只允许出现番号。
func TestSubscriptionLogsUseCodeInsteadOfInternalIDs(t *testing.T) {
	media := &domain.Media{ID: "m1", Code: "SSIS-001", Title: "原标题"}
	repository := &stubSubscriptionRepository{item: domain.Subscription{ID: "s1", MediaID: "m1", Media: media}, created: true}
	service := NewSubscriptionService(repository)
	ctx := context.Background()

	buffer, restore := captureLogs()
	defer restore()

	if _, _, err := service.Create(ctx, CreateSubscriptionCommand{IdempotencyKey: "channel:SSIS-001", MediaID: "m1", Mode: domain.SubscriptionModeStrict}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, UpdateSubscriptionCommand{ID: "s1", Mode: domain.SubscriptionModePreload, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cancel(ctx, "s1"); err != nil {
		t.Fatal(err)
	}

	output := buffer.String()
	for _, message := range []string{"订阅已保存", "订阅已编辑", "订阅已取消"} {
		if !strings.Contains(output, `"msg":"`+message+`"`) {
			t.Errorf("日志缺少 %s：%s", message, output)
		}
	}
	if !strings.Contains(output, `"code":"SSIS-001"`) {
		t.Errorf("订阅日志未使用番号：%s", output)
	}
	for _, forbidden := range []string{"subscription_id", `"media_id"`} {
		if strings.Contains(output, forbidden) {
			t.Errorf("订阅日志不应出现内部标识 %s：%s", forbidden, output)
		}
	}
}

// TestSubscriptionLogsRecordFailureReason 约束订阅失败日志带出原因，失败时没有番号也不编造标识。
func TestSubscriptionLogsRecordFailureReason(t *testing.T) {
	repository := &stubSubscriptionRepository{err: errSubscriptionStub}
	service := NewSubscriptionService(repository)
	ctx := context.Background()

	buffer, restore := captureLogs()
	defer restore()

	if _, err := service.Cancel(ctx, "s1"); err == nil {
		t.Fatal("期望取消失败")
	}

	output := buffer.String()
	if !strings.Contains(output, `"msg":"取消订阅失败"`) || !strings.Contains(output, `"error":"`+errSubscriptionStub.Error()+`"`) {
		t.Fatalf("取消失败日志 = %s", output)
	}
	if strings.Contains(output, "subscription_id") {
		t.Fatalf("失败日志不应出现订阅 ID：%s", output)
	}
}

// errSubscriptionStub 是订阅失败用例使用的固定原因，断言日志原样带出该原因。
var errSubscriptionStub = errors.New("subscription not found")
