package ports

import (
	"context"
	"time"

	"bytemuse/backend/internal/domain"
)

// SubscriptionDownloadAttempt is a leased task with a snapshot of active subscription rules.
type SubscriptionDownloadAttempt struct {
	ID, MediaID, Code string
	Mode              domain.SubscriptionMode
	Filter            map[string]any
	LeaseToken        string
}

// PendingSubmission is a resource whose download-client outcome still requires reconciliation.
type PendingSubmission struct{ ID, URI, InfoHash, Downloader, LeaseToken string }

// TransferState is a downloader-observed transport snapshot keyed by info hash.
type TransferState struct {
	Hash, Status         string
	AddedAt, CompletedAt *time.Time
}

// SubscriptionDownloadRepository is the durable boundary for scheduled subscription downloads.
type SubscriptionDownloadRepository interface {
	Enqueue(ctx context.Context, subscriptionID string) (domain.DownloadTask, error)
	EnqueueActive(ctx context.Context) (int, error)
	Claim(ctx context.Context, now time.Time) (*SubscriptionDownloadAttempt, error)
	SetCandidate(ctx context.Context, a SubscriptionDownloadAttempt, site, kind, uri, hash, downloader string, passed bool) error
	FinishSearch(ctx context.Context, a SubscriptionDownloadAttempt, message string) error
	ClaimPending(ctx context.Context, now time.Time) (*PendingSubmission, error)
	FinishSubmission(ctx context.Context, p PendingSubmission, success bool, errorMessage string) error
	ReleasePending(ctx context.Context, p PendingSubmission, message string) error
}
