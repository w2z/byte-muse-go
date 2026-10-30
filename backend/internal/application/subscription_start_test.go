package application

import (
	"context"
	"errors"
	"testing"
)

// starterClient 记录提交后的启动调用，用于验证下载器可选启动能力。
type starterClient struct {
	started []string
	fail    error
}

func (c *starterClient) HasHash(context.Context, string) (bool, error) { return true, nil }

func (c *starterClient) Submit(context.Context, string) error { return nil }

func (c *starterClient) Start(_ context.Context, hash string) error {
	c.started = append(c.started, hash)
	return c.fail
}

// plainClient 只实现提交能力，模拟不支持启动的下载器。
type plainClient struct{}

func (plainClient) HasHash(context.Context, string) (bool, error) { return true, nil }

func (plainClient) Submit(context.Context, string) error { return nil }

// TestStartSubmittedStartsSupportedDownloader 验证支持启动的下载器在任务确认落库后被显式启动：
// qBittorrent 开启「添加种子时停止」时，不启动就等于没有下载。
func TestStartSubmittedStartsSupportedDownloader(t *testing.T) {
	client := &starterClient{}
	startSubmitted(context.Background(), client, "0123456789abcdef0123456789abcdef01234567")
	if len(client.started) != 1 || client.started[0] != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("启动调用 = %v", client.started)
	}
}

// TestStartSubmittedSkipsUnsupportedDownloader 验证不实现启动能力的下载器被跳过，不产生错误。
func TestStartSubmittedSkipsUnsupportedDownloader(t *testing.T) {
	startSubmitted(context.Background(), plainClient{}, "0123456789abcdef0123456789abcdef01234567")
}

// TestStartSubmittedIgnoresEmptyHashAndFailure 验证空哈希不发起调用，启动失败不返回错误：
// 任务已经提交成功，启动失败只记录日志，由后续状态同步暴露。
func TestStartSubmittedIgnoresEmptyHashAndFailure(t *testing.T) {
	empty := &starterClient{}
	startSubmitted(context.Background(), empty, "")
	if len(empty.started) != 0 {
		t.Fatalf("空哈希不应启动: %v", empty.started)
	}
	failing := &starterClient{fail: errors.New("start rejected")}
	startSubmitted(context.Background(), failing, "0123456789abcdef0123456789abcdef01234567")
	if len(failing.started) != 1 {
		t.Fatalf("启动调用 = %v", failing.started)
	}
}
