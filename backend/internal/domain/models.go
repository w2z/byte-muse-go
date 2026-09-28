// Package domain defines the business values shared by application entry points.
package domain

import "time"

// SubscriptionStatus describes subscription intent independently from download and library state.
type SubscriptionStatus string

const (
	SubscriptionStatusNone     SubscriptionStatus = "none"
	SubscriptionStatusActive   SubscriptionStatus = "active"
	SubscriptionStatusCanceled SubscriptionStatus = "canceled"
)

// SubscriptionMode controls whether resource matching is strict or may preload candidates.
type SubscriptionMode string

const (
	SubscriptionModeStrict  SubscriptionMode = "strict"
	SubscriptionModePreload SubscriptionMode = "preload"
)

// DownloadStatus describes the lifecycle of one download task.
type DownloadStatus string

const (
	DownloadStatusQueued      DownloadStatus = "queued"
	DownloadStatusSearching   DownloadStatus = "searching"
	DownloadStatusSubmitted   DownloadStatus = "submitted"
	DownloadStatusDownloading DownloadStatus = "downloading"
	DownloadStatusCompleted   DownloadStatus = "completed"
	DownloadStatusFailed      DownloadStatus = "failed"
	DownloadStatusUnknown     DownloadStatus = "unknown"
)

// LibraryStatus describes whether media is present in the configured media library.
type LibraryStatus string

const (
	LibraryStatusUnknown LibraryStatus = "unknown"
	LibraryStatusAbsent  LibraryStatus = "absent"
	LibraryStatusPresent LibraryStatus = "present"
)

// MediaDisplayStatus is the single user-facing state shown on media cards.
// It combines library, download, and subscription facts without hiding failed or unknown tasks.
type MediaDisplayStatus string

const (
	MediaDisplayStatusUnsubscribed MediaDisplayStatus = "unsubscribed"
	MediaDisplayStatusSubscribed   MediaDisplayStatus = "subscribed"
	MediaDisplayStatusDownloading  MediaDisplayStatus = "downloading"
	MediaDisplayStatusCompleted    MediaDisplayStatus = "completed"
	MediaDisplayStatusFailed       MediaDisplayStatus = "failed"
	MediaDisplayStatusUnknown      MediaDisplayStatus = "unknown"
)

// ResolveMediaDisplayStatus applies the product-wide display priority used by every media view.
func ResolveMediaDisplayStatus(library LibraryStatus, subscription SubscriptionStatus, download *DownloadStatus) MediaDisplayStatus {
	if library == LibraryStatusPresent || download != nil && *download == DownloadStatusCompleted {
		return MediaDisplayStatusCompleted
	}
	if download != nil {
		switch *download {
		case DownloadStatusQueued, DownloadStatusSearching, DownloadStatusSubmitted, DownloadStatusDownloading:
			return MediaDisplayStatusDownloading
		}
	}
	if subscription == SubscriptionStatusActive {
		return MediaDisplayStatusSubscribed
	}
	if download != nil {
		switch *download {
		case DownloadStatusFailed:
			return MediaDisplayStatusFailed
		case DownloadStatusUnknown:
			return MediaDisplayStatusUnknown
		}
	}
	return MediaDisplayStatusUnsubscribed
}

// ValidVideoType 校验单值影片分类；未知分类用 NULL 表示，不使用猜测值。
func ValidVideoType(value string) bool {
	switch value {
	case "censored", "uncensored", "uncensored_cracked", "leaked":
		return true
	}
	return false
}

// Media is the API-facing catalog representation.
type Media struct {
	ID                 string             `json:"id"`
	Code               string             `json:"code"`
	Title              string             `json:"title"`
	VideoType          *string            `json:"video_type"` // NULL 表示尚未分类。
	TranslatedTitle    *string            `json:"translated_title"`
	PosterURL          *string            `json:"poster_url"`
	BannerURL          *string            `json:"banner_url"`
	PreviewURL         *string            `json:"preview_url"`
	StillPhotos        []string           `json:"still_photos"`
	ReleaseDate        *string            `json:"release_date"`
	DurationMinutes    *int               `json:"duration_minutes"`
	SubscriptionStatus SubscriptionStatus `json:"subscription_status"`
	LibraryStatus      LibraryStatus      `json:"library_status"`
	DisplayStatus      MediaDisplayStatus `json:"display_status"`
	ActiveSubscription *Subscription      `json:"active_subscription"`
	DownloadStatus     *DownloadStatus    `json:"download_status"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// MediaPage is a repository result before request pagination metadata is attached.
type MediaPage struct {
	Items []Media
	Total int
}

// Subscription records subscription intent and its optimistic concurrency version.
type Subscription struct {
	ID        string             `json:"id"`
	MediaID   string             `json:"media_id"`
	Status    SubscriptionStatus `json:"status"`
	Mode      SubscriptionMode   `json:"mode"`
	Filter    map[string]any     `json:"filter"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Version   int                `json:"version"`
	Media     *Media             `json:"media,omitempty"`
}

// SubscriptionPage is a repository result before request pagination metadata is attached.
type SubscriptionPage struct {
	Items []Subscription
	Total int
}

// DownloadTask is one independently tracked resource download attempt.
type DownloadTask struct {
	ID             string         `json:"id"`
	MediaID        string         `json:"media_id"`
	SourceSite     *string        `json:"source_site"`
	SourceKind     *string        `json:"source_kind"`
	Downloader     *string        `json:"downloader"`
	InfoHash       *string        `json:"info_hash"`
	TransferStatus *string        `json:"transfer_status"`
	AddedAt        *time.Time     `json:"added_at"`
	CompletedAt    *time.Time     `json:"completed_at"`
	Status         DownloadStatus `json:"status"`
	ExternalID     *string        `json:"external_id"`
	ErrorMessage   *string        `json:"error_message"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// DownloadPage is a repository result before request pagination metadata is attached.
type DownloadPage struct {
	Items []DownloadTask
	Total int
}

// ScheduledTask describes one configured background job shown on the task page.
type ScheduledTask struct {
	Name    string     `json:"name"`
	Cron    string     `json:"cron"`
	LastRun *time.Time `json:"last_run"`
	Running bool       `json:"running"`
}

// Dashboard contains the first-stage operational counts shown to administrators.
type Dashboard struct {
	ActiveSubscriptions int `json:"active_subscriptions"`
	CompletedDownloads  int `json:"completed_downloads"`
	MediaCount          int `json:"media_count"`
}

// SystemSettings exposes the runtime settings allowed by the authenticated settings contract.
type SystemSettings struct {
	DatabaseDriver string            `json:"database_driver"`
	Values         map[string]string `json:"values"`
	Configured     map[string]bool   `json:"configured"`
}
