package ports

import (
	"context"
	"errors"
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

// ErrSubscriptionTaskActive 表示该订阅已有进行中的下载任务，本次搜索不再需要建立任务。
var ErrSubscriptionTaskActive = errors.New("该订阅已有进行中的下载任务")

// SubscriptionScanAttempt 是一次已领取的订阅资源搜索，带订阅规则与影片快照。
// 搜索没有选中资源时不产生下载任务，本次搜索结束，等待下一次排期重新搜索。
type SubscriptionScanAttempt struct {
	ID, SubscriptionID, MediaID string
	Code                        string
	// Title 是关联影片标题（优先译文），仅用于通知文案。
	Title string
	// Cover 是通知配图地址，仅用于推送封面；为空时通知降级为纯文本。
	Cover      string
	Mode       domain.SubscriptionMode
	Filter     map[string]any
	LeaseToken string
	// Origin 是本次搜索的发起方，决定搜索失败时是否推送通知。
	Origin DownloadOrigin
}

// ScanCandidate 是搜索选中并已解析完成的资源快照，是建立下载任务的唯一输入。
type ScanCandidate struct {
	Site, Kind, URI, InfoHash, Downloader string
	FilterPassed                          bool
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

// SubscriptionDownloadRepository 是订阅搜索队列与下载任务的持久化边界。
// 搜索与下载是两个独立事实：subscription_scans 只保存待执行的资源搜索，
// download_tasks 只保存已经选中资源、真正提交给下载器的任务。
type SubscriptionDownloadRepository interface {
	// EnqueueScan 为一条有效订阅登记一次资源搜索；origin 决定搜索失败时是否推送通知。
	// 该订阅已有进行中的下载任务时不再登记搜索，直接返回该任务标识：搜索的目的就是建立下载任务。
	// 已有待执行搜索时只把发起方升级为 user，不重复入队。
	EnqueueScan(ctx context.Context, subscriptionID string, origin DownloadOrigin) (string, error)
	// EnqueueActiveScans 批量登记全部有效订阅的资源搜索，来源固定为 schedule：批量扫描的失败不推送。
	EnqueueActiveScans(ctx context.Context) (int, error)
	// ClaimScan 领取一次待执行搜索，返回订阅规则与影片快照。
	ClaimScan(ctx context.Context, now time.Time) (*SubscriptionScanAttempt, error)
	// FinishScan 结束一次没有产生下载任务的搜索：删除队列项，等待下一次排期重新搜索。
	FinishScan(ctx context.Context, a SubscriptionScanAttempt) error
	// StartTask 用选中资源建立下载任务，是 download_tasks 的唯一写入入口。
	// 该订阅已有有效任务时返回 ErrSubscriptionTaskActive；订阅已失效时返回 ErrSubscriptionNotFound。
	StartTask(ctx context.Context, a SubscriptionScanAttempt, c ScanCandidate) (PendingSubmission, error)
	ClaimPending(ctx context.Context, now time.Time) (*PendingSubmission, error)
	FinishSubmission(ctx context.Context, p PendingSubmission, success bool, errorMessage string) error
	ReleasePending(ctx context.Context, p PendingSubmission, message string) error
}
