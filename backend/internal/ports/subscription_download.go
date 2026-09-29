package ports

import (
	"context"
	"time"

	"bytemuse/backend/internal/domain"
)

// DownloadOrigin 标识一次下载尝试的发起方，是失败通知是否推送的唯一依据。
// schedule 由定时任务与后台批处理产生，失败只落库不推送；user 由用户显式发起，失败必须推送。
type DownloadOrigin string

const (
	// DownloadOriginSchedule 是定时任务批量扫描发起的下载尝试，也是历史数据的默认值。
	DownloadOriginSchedule DownloadOrigin = "schedule"
	// DownloadOriginUser 是用户在聊天渠道或后台显式发起的下载尝试。
	DownloadOriginUser DownloadOrigin = "user"
)

// SubscriptionDownloadAttempt is a leased task with a snapshot of active subscription rules.
type SubscriptionDownloadAttempt struct {
	ID, MediaID, Code string
	// Title 是关联影片标题（优先译文），仅用于通知文案。
	Title string
	// Cover 是通知配图地址，仅用于推送封面；为空时通知降级为纯文本。
	Cover      string
	Mode       domain.SubscriptionMode
	Filter     map[string]any
	LeaseToken string
	// Origin 是本次尝试的发起方，决定搜索失败时是否推送通知。
	Origin DownloadOrigin
}

// PendingSubmission is a resource whose download-client outcome still requires reconciliation.
// Code/Title/Site/Cover 来自已落库的任务快照，仅用于通知文案与配图，不参与提交逻辑。
type PendingSubmission struct {
	ID, URI, InfoHash, Downloader, LeaseToken string
	Code, Title, Site, Cover                  string
}

// TransferState is a downloader-observed transport snapshot keyed by info hash.
type TransferState struct {
	Hash, Status         string
	AddedAt, CompletedAt *time.Time
	ObservedAt           time.Time // 拉取快照前的时间，用于拒绝控制操作之前的过期响应。
}

// TransferTransition 是一次传输状态跃迁：只报告首次进入终态（completed / failed）的任务。
// 下载器每次轮询都会重复上报同一状态，通知必须依赖这里做去重。
type TransferTransition struct {
	TaskID string
	Code   string
	Title  string
	Cover  string
	Status string
}

// SubscriptionDownloadRepository is the durable boundary for scheduled subscription downloads.
type SubscriptionDownloadRepository interface {
	// Enqueue 登记一条订阅的下载尝试；origin 决定搜索失败时是否推送通知。
	Enqueue(ctx context.Context, subscriptionID string, origin DownloadOrigin) (domain.DownloadTask, error)
	// EnqueueActive 批量登记全部有效订阅，来源固定为 schedule：批量扫描的失败不推送。
	EnqueueActive(ctx context.Context) (int, error)
	Claim(ctx context.Context, now time.Time) (*SubscriptionDownloadAttempt, error)
	SetCandidate(ctx context.Context, a SubscriptionDownloadAttempt, site, kind, uri, hash, downloader string, passed bool) error
	FinishSearch(ctx context.Context, a SubscriptionDownloadAttempt, message string) error
	ClaimPending(ctx context.Context, now time.Time) (*PendingSubmission, error)
	FinishSubmission(ctx context.Context, p PendingSubmission, success bool, errorMessage string) error
	ReleasePending(ctx context.Context, p PendingSubmission, message string) error
}
