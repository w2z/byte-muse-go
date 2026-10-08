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

// TaskCheckpointRepository 保存任务的独立断点；键不存在时返回 nil，不存储凭据。
type TaskCheckpointRepository interface {
	LoadCheckpoint(context.Context, string, string) ([]byte, error)
	SaveCheckpoint(context.Context, string, string, []byte) error
}
