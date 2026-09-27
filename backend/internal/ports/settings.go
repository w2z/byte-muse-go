package ports

import "context"

// StoredSetting is one persisted application setting. Secret values are encrypted by the application service before storage.
type StoredSetting struct {
	Key      string
	Value    string
	IsSecret bool
}

// SettingsRepository persists the bounded application setting set.
type SettingsRepository interface {
	List(context.Context) ([]StoredSetting, error)
	Upsert(context.Context, []StoredSetting) error
}
