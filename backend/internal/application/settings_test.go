package application

import (
	"context"
	"testing"

	"bytemuse/backend/internal/ports"
)

type settingsRepositoryStub struct {
	items []ports.StoredSetting
}

func (r settingsRepositoryStub) List(context.Context) ([]ports.StoredSetting, error) {
	return r.items, nil
}

func (r settingsRepositoryStub) Upsert(context.Context, []ports.StoredSetting) error { return nil }

func TestSettingsGetSkipsSecretThatCannotBeDecrypted(t *testing.T) {
	service, err := NewSettingsService(settingsRepositoryStub{items: []ports.StoredSetting{
		{Key: "EMBY_API_KEY", Value: "not-a-valid-ciphertext", IsSecret: true},
		{Key: "EMBY_URL", Value: "http://emby.local"},
	}}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create settings service: %v", err)
	}

	settings, err := service.Get(context.Background())
	if err != nil {
		t.Fatalf("Get should keep the service available when one legacy secret cannot be decrypted: %v", err)
	}
	if settings.Values["EMBY_API_KEY"] != "" || settings.Configured["EMBY_API_KEY"] {
		t.Fatalf("undecryptable secret must be treated as unavailable: %#v", settings)
	}
	if settings.Values["EMBY_URL"] != "http://emby.local" {
		t.Fatalf("non-secret settings must still be returned: %#v", settings.Values)
	}
}

func TestSettingsRejectsNegativeRetentionDays(t *testing.T) {
	service, err := NewSettingsService(settingsRepositoryStub{}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), map[string]string{"LOG_RETENTION_DAYS": "-1"}); err == nil {
		t.Fatal("negative retention days must be rejected")
	}
}
