package ports

import (
	"context"
	"time"
)

// LibrarySource 保存可重新核验的文件身份，不保存令牌或播放链接。
// Kind 为 local/115/cd2/emby/plex/jellyfin，Location 为本地路径、网盘父目录或服务器地址。
type LibrarySource struct {
	Kind     string
	Scope    string // 网盘账号或服务器身份；空值表示历史记录缺少身份。
	Location string
	ItemID   string
}

// SubscriptionPresenceRepository 保存来源与订阅满足事实；订阅仍保持 active 供用户管理。
type SubscriptionPresenceRepository interface {
	LibrarySources(context.Context, string) ([]LibrarySource, error)
	CompleteScan(context.Context, SubscriptionScanAttempt, string, *LibrarySource) error
}

// ActiveTransfer 保存回查所需的任务快照；更新时间用于拒绝过期的缺失判断。
type ActiveTransfer struct {
	ID, InfoHash, Downloader string
	UpdatedAt                time.Time
}

// SubscriptionTransferPresenceRepository 回查当前订阅影片的下载，只有明确缺失才释放旧任务。
type SubscriptionTransferPresenceRepository interface {
	ActiveTransfers(context.Context, string) ([]ActiveTransfer, error)
	MarkTransferMissing(context.Context, ActiveTransfer) error
	MarkTransferCompleted(context.Context, ActiveTransfer) error
}
