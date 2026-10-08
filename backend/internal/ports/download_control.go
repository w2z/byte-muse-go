package ports

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
)

var (
	// ErrDownloadNotFound 表示任务已不存在。
	ErrDownloadNotFound = errors.New("下载任务不存在")
	// ErrDownloadConflict 表示任务状态或操作租约已变化。
	ErrDownloadConflict = errors.New("任务状态已变化或正在操作，请刷新后重试")
	// ErrDownloadAction 表示当前状态或下载器不支持该操作。
	ErrDownloadAction = errors.New("当前任务不支持此操作")
)

// DownloadController 根据下载器实际版本提供能力，并回查单个任务。
type DownloadController interface {
	Capabilities(context.Context) ([]string, error)
	Observe(context.Context, string) (*TransferState, error)
	Control(context.Context, string, string) error
}

// DownloadMetricsReader 只读取当前页精确 hash 的指标；不支持的下载器无需实现。
type DownloadMetricsReader interface {
	ReadMetrics(context.Context, []string) (map[string]*domain.DownloadMetrics, error)
}

// DownloadControlRepository 使用已有任务租约排他控制，防止并发点击和过期状态覆盖。
type DownloadControlRepository interface {
	LockControl(context.Context, string) (domain.DownloadTask, string, error)
	FinishControl(context.Context, string, string, string, *TransferState) error
	ReleaseControl(context.Context, string, string) error
}
