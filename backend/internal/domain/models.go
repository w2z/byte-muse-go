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

// Media is the API-facing catalog representation.
type Media struct {
	ID                 string             `json:"id"`
	Code               string             `json:"code"`
	Title              string             `json:"title"`
	TranslatedTitle    *string            `json:"translated_title"`
	PosterURL           *string            `json:"poster_url"`
	ReleaseDate         *string            `json:"release_date"`
	DurationMinutes    *int               `json:"duration_minutes"`
	SubscriptionStatus SubscriptionStatus `json:"subscription_status"`
	LibraryStatus      LibraryStatus      `json:"library_status"`
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
}

// SubscriptionPage is a repository result before request pagination metadata is attached.
type SubscriptionPage struct {
	Items []Subscription
	Total int
}

// DownloadTask is one independently tracked resource download attempt.
type DownloadTask struct {
	ID           string         `json:"id"`
	MediaID      string         `json:"media_id"`
	Status       DownloadStatus `json:"status"`
	ExternalID   *string        `json:"external_id"`
	ErrorMessage *string        `json:"error_message"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// DownloadPage is a repository result before request pagination metadata is attached.
type DownloadPage struct {
	Items []DownloadTask
	Total int
}

// SystemStatus exposes non-sensitive process state.
type SystemStatus struct {
	Version          string    `json:"version"`
	DatabaseDriver   string    `json:"database_driver"`
	SchedulerRunning bool      `json:"scheduler_running"`
	StartedAt        time.Time `json:"started_at"`
}

// Dashboard contains the first-stage operational counts shown to administrators.
type Dashboard struct {
	ActiveSubscriptions int `json:"active_subscriptions"`
	CompletedDownloads  int `json:"completed_downloads"`
	MediaCount           int `json:"media_count"`
	HealthyIntegrations  int `json:"healthy_integrations"`
}

// SystemSettings exposes the non-sensitive runtime settings allowed by the public contract.
type SystemSettings struct {
	DatabaseDriver string `json:"database_driver"`
	DemoSeedEnabled bool   `json:"demo_seed_enabled"`
}
