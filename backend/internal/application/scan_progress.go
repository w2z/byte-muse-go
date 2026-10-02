package application

import (
	"bytemuse/backend/internal/domain"
	"context"
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
