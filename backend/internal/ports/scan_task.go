package ports

import (
	"bytemuse/backend/internal/domain"
	"context"
)

// ScanTaskRepository 持久化任务快照；不存在的最新任务返回 nil。
type ScanTaskRepository interface {
	Save(context.Context, domain.ScanTask) error
	Latest(context.Context, string) (*domain.ScanTask, error)
	Get(context.Context, string) (*domain.ScanTask, error)
	Interrupt(context.Context) error
}
