package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"fmt"
	"strings"
	"time"
)

// ScanProgress 是当前任务的动态文件进度；Total 是已发现总数，扫描期间可增长。
// Processed 包含成功、跳过及已尝试但失败的文件，不等同于新增文件数。
type ScanProgress = domain.ScanProgress

type scanProgressKey struct{}
type scanDiscoveryKey struct{}

// NotifyScanProgress 向当前同步观察者转发持久化任务快照，不改变后台任务生命周期。
func NotifyScanProgress(ctx context.Context, p ScanProgress) {
	if report, ok := ctx.Value(scanProgressKey{}).(func(ScanProgress)); ok {
		report(p)
	}
}

// withScanDiscovery 在共享目录遍历器发现符合过滤条件的文件时更新当前任务总数。
func withScanDiscovery(ctx context.Context, found func()) context.Context {
	return context.WithValue(ctx, scanDiscoveryKey{}, found)
}

func reportScanDiscovery(ctx context.Context) {
	if found, ok := ctx.Value(scanDiscoveryKey{}).(func()); ok {
		found()
	}
}

// WithScanProgress 给本次同步扫描附加进度观察器，不保存全局状态；回调在扫描线程内顺序执行。
func WithScanProgress(ctx context.Context, report func(ScanProgress)) context.Context {
	return context.WithValue(ctx, scanProgressKey{}, report)
}

// reportScanProgress 统一计算文件处理百分比；空任务只有完成后才显示 100%。
func reportScanProgress(ctx context.Context, phase string, processed, total int, current string) {
	report, _ := ctx.Value(scanProgressKey{}).(func(ScanProgress))
	if report == nil {
		return
	}
	p := ScanProgress{Phase: phase, Processed: processed, Total: total, Current: current}
	if total > 0 {
		p.Percent = processed * 100 / total
	}
	if phase == "completed" {
		p.Percent = 100
	}
	report(p)
}

// pan115CooldownNotice 描述一次 115 限流冷却，供任务进度展示。
// 冷却期间不再产生新的网盘请求，任务仍在运行；恢复后由下一次进度回调自然覆盖。
// path 是当前正在扫描的映射或目录，便于用户定位是哪条配置触发了限流。
func pan115CooldownNotice(path string, wait time.Duration) string {
	label := strings.TrimSpace(path)
	if label == "" {
		label = "115 网盘"
	}
	return fmt.Sprintf("%s：115 访问受限，等待 %s 后重试", label, formatCooldown(wait))
}

// formatCooldown 把冷却时长格式化成便于阅读的中文时长。
func formatCooldown(wait time.Duration) string {
	if wait <= 0 {
		return "片刻"
	}
	if wait < time.Minute {
		seconds := int(wait.Seconds() + 0.5)
		if seconds < 1 {
			seconds = 1
		}
		return fmt.Sprintf("%d 秒", seconds)
	}
	minutes := int(wait.Minutes() + 0.5)
	if minutes < 60 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	hours, rest := minutes/60, minutes%60
	if rest == 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", hours, rest)
}
