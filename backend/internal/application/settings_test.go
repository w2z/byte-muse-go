package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bytemuse/backend/internal/ports"
)

type settingsRepositoryStub struct {
	items []ports.StoredSetting
}

// TestBypassProxySetting 验证开关持久化、部分更新禁用增强时关闭代理，以及非法布尔值拒绝。
func TestBypassProxySetting(t *testing.T) {
	repo := &settingsMemoryRepository{}
	svc, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		values map[string]string
		want   string
	}{
		{map[string]string{"BYPASS_ENGINE": "flaresolverr", "BYPASS_USE_PROXY": "true"}, "true"},
		{map[string]string{"BYPASS_ENGINE": ""}, "false"},
		{map[string]string{"BYPASS_USE_PROXY": "true"}, "false"},
		{map[string]string{"BYPASS_ENGINE": "flaresolverr"}, "false"},
		{map[string]string{"BYPASS_USE_PROXY": "true"}, "true"},
		{map[string]string{"BYPASS_USE_PROXY": ""}, "false"},
	} {
		got, err := svc.Update(ctx, tc.values)
		if err != nil || got.Values["BYPASS_USE_PROXY"] != tc.want {
			t.Fatalf("proxy=%q err=%v", got.Values["BYPASS_USE_PROXY"], err)
		}
	}
	if _, err := svc.Update(ctx, map[string]string{"BYPASS_USE_PROXY": "yes"}); !errors.Is(err, ErrInvalidSetting) {
		t.Fatal(err)
	}
}

// TestSettingsScheduleTimeValidation 校验定时任务键只接受标准 5 段 cron，避免非法表达式在下次启动时拖垮调度器。
func TestSettingsScheduleTimeValidation(t *testing.T) {
	repo := &settingsMemoryRepository{}
	svc, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	keys := []string{"RANK_SCHEDULE_TIME", "ACTOR_SCHEDULE_TIME", "TAG_SCHEDULE_TIME", "DOWNLOAD_SCHEDULE_TIME"}
	for _, key := range keys {
		for _, valid := range []string{"0 20 * * *", "30 21 * * *", "@daily", ""} {
			if _, err := svc.Update(ctx, map[string]string{key: valid}); err != nil {
				t.Fatalf("%s should accept %q: %v", key, valid, err)
			}
		}
		for _, invalid := range []string{"99 99 * * *", "0 20 * *", "every day", "0 20 * * * *"} {
			if _, err := svc.Update(ctx, map[string]string{key: invalid}); !errors.Is(err, ErrInvalidSetting) {
				t.Fatalf("%s should reject %q, got %v", key, invalid, err)
			}
		}
	}
}

// TestSettingsUpdateNotifiesScheduleApplier 验证保存设置后把最新值交给调度同步回调，使定时任务
// 表达式无需重启后端即可生效；同步失败不能把已经落库的保存报成失败。
func TestSettingsUpdateNotifiesScheduleApplier(t *testing.T) {
	repo := &settingsMemoryRepository{}
	svc, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	var applied map[string]string
	calls := 0
	svc.SetScheduleApplier(func(values map[string]string) error {
		calls++
		applied = values
		return nil
	})
	if _, err := svc.Update(context.Background(), map[string]string{"RANK_SCHEDULE_TIME": "0 3 * * *"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || applied["RANK_SCHEDULE_TIME"] != "0 3 * * *" {
		t.Fatalf("schedule applier not invoked with saved values: calls=%d values=%v", calls, applied)
	}

	svc.SetScheduleApplier(func(map[string]string) error { return errors.New("scheduler unavailable") })
	saved, err := svc.Update(context.Background(), map[string]string{"RANK_SCHEDULE_TIME": "0 4 * * *"})
	if err != nil {
		t.Fatalf("schedule sync failure must not fail the save: %v", err)
	}
	if saved.Values["RANK_SCHEDULE_TIME"] != "0 4 * * *" {
		t.Fatalf("saved value = %q", saved.Values["RANK_SCHEDULE_TIME"])
	}
}

// TestSettingsPromptCapacity 防止长中文提示词被普通配置长度限制拒绝，并核对完整回读和清空。
func TestSettingsPromptCapacity(t *testing.T) {
	for _, key := range []string{"AGENT_SYSTEM_PROMPT", "TRANSLATION_PROMPT"} {
		t.Run(key, func(t *testing.T) {
			repo := &settingsMemoryRepository{}
			service, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
			if err != nil {
				t.Fatal(err)
			}
			for _, prompt := range []string{strings.Repeat("中文提示词\n", 1000), strings.Repeat("中", 20480), strings.Repeat("😀", 15360), ""} {
				want := strings.TrimSpace(prompt)
				got, err := service.Update(context.Background(), map[string]string{key: prompt})
				if err != nil {
					t.Fatalf("save %d bytes: %v", len(prompt), err)
				}
				if got.Values[key] != want {
					t.Fatal("save response truncated prompt")
				}
				got, err = service.Get(context.Background())
				if err != nil || got.Values[key] != want {
					t.Fatal("prompt did not round trip")
				}
			}
		})
	}
}

// TestSettingsLengthRejectionIsAtomic 验证字节边界、清晰错误及失败时不覆盖现有配置。
func TestSettingsLengthRejectionIsAtomic(t *testing.T) {
	for _, tc := range []struct{ key, value, limit string }{
		{"AGENT_SYSTEM_PROMPT", strings.Repeat("中", 20480) + "a", "61440"},
		{"TRANSLATION_PROMPT", strings.Repeat("😀", 15360) + "a", "61440"},
		{"OPENAI_MODEL", strings.Repeat("a", 8193), "8192"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			repo := &settingsMemoryRepository{items: []ports.StoredSetting{{Key: tc.key, Value: "original"}, {Key: "AGENT_ENABLE", Value: "false"}}}
			service, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Update(context.Background(), map[string]string{tc.key: tc.value, "AGENT_ENABLE": "true"})
			if !errors.Is(err, ErrInvalidSetting) || !strings.Contains(err.Error(), tc.limit) || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("expected explicit byte limit, got %v", err)
			}
			got, err := service.Get(context.Background())
			if err != nil || got.Values[tc.key] != "original" || got.Values["AGENT_ENABLE"] != "false" {
				t.Fatal("rejected batch changed settings")
			}
		})
	}
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

// TestSettingsIgnoresRetiredLogRetention 验证旧值不再回显或允许保存，避免产生无效配置。
func TestSettingsIgnoresRetiredLogRetention(t *testing.T) {
	service, err := NewSettingsService(settingsRepositoryStub{items: []ports.StoredSetting{{Key: "LOG_RETENTION_DAYS", Value: "30"}}}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := settings.Values["LOG_RETENTION_DAYS"]; exists {
		t.Fatal("retired retention setting must not be exposed")
	}
	if _, err := service.Update(context.Background(), map[string]string{"LOG_RETENTION_DAYS": "30"}); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("retired retention setting must be rejected: %v", err)
	}
}

func TestSettingsAcceptsDownloaderAndBypassOptions(t *testing.T) {
	service, err := NewSettingsService(settingsRepositoryStub{}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), map[string]string{
		"ARIA2_URL":     "http://aria2:6800/jsonrpc",
		"BYPASS_ENGINE": "scrapling",
	}); err != nil {
		t.Fatalf("new settings should be accepted: %v", err)
	}
}

func TestSettingsRejectsUnknownBypassEngine(t *testing.T) {
	service, err := NewSettingsService(settingsRepositoryStub{}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), map[string]string{"BYPASS_ENGINE": "unknown"}); err == nil {
		t.Fatal("unknown bypass engine must be rejected")
	}
}

func TestSettingsAcceptsPTAndBTDefaultDownloaders(t *testing.T) {
	service, err := NewSettingsService(settingsRepositoryStub{}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), map[string]string{
		"PT_DEFAULT_DOWNLOADER": "transmission",
		"BT_DEFAULT_DOWNLOADER": "aria2",
	}); err != nil {
		t.Fatalf("default downloader settings should be accepted: %v", err)
	}
}

func TestSettingsRejectsDisallowedDefaultDownloaders(t *testing.T) {
	service, err := NewSettingsService(settingsRepositoryStub{}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"PT_DEFAULT_DOWNLOADER": "aria2",
		"BT_DEFAULT_DOWNLOADER": "clouddrive2",
	} {
		if _, err := service.Update(context.Background(), map[string]string{key: value}); err == nil {
			t.Fatalf("%s=%s must be rejected", key, value)
		}
	}
}

func TestSettingsPTSiteCredentialsAndRetiredRousiKey(t *testing.T) {
	service, err := NewSettingsService(settingsRepositoryStub{}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"PTFANS_COOKIE", "ROUSIPRO_COOKIE"} {
		if _, err := service.Update(context.Background(), map[string]string{key: "session=demo"}); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	for _, values := range []map[string]string{{"ROUSI_COOKIE": "legacy"}, {"MAIN_SITE": "Rousi"}} {
		if _, err := service.Update(context.Background(), values); err == nil {
			t.Fatalf("retired value accepted: %#v", values)
		}
	}
}

// TestSettingsPan115ScanPaths 校验 115 扫描目录只接受 {id,path} 数组：拒绝对象、空元素与重复目录，
// 并确认合法值原样回读。
func TestSettingsPan115ScanPaths(t *testing.T) {
	service, err := NewSettingsService(&settingsMemoryRepository{}, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const saved = `[{"id":"341789","path":"/影片"},{"id":"341790","path":"/影片/4K"}]`
	settings, err := service.Update(ctx, map[string]string{"PAN115_SCAN_PATHS": saved})
	if err != nil {
		t.Fatalf("save scan paths: %v", err)
	}
	if settings.Values["PAN115_SCAN_PATHS"] != saved {
		t.Fatalf("scan paths did not round trip: %q", settings.Values["PAN115_SCAN_PATHS"])
	}
	for _, valid := range []string{"", "[]", `[{"id":"341789","path":"/影片"}]`} {
		if _, err := service.Update(ctx, map[string]string{"PAN115_SCAN_PATHS": valid}); err != nil {
			t.Fatalf("should accept %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"{}",
		`{"id":"341789","path":"/影片"}`,
		"null",
		"[1,2]",
		`[{"id":"","path":"/影片"}]`,
		`[{"id":"341789","path":""}]`,
		`[{"id":"341789","path":"/影片"},{"id":"341789","path":"/影片/2"}]`,
	} {
		if _, err := service.Update(ctx, map[string]string{"PAN115_SCAN_PATHS": invalid}); !errors.Is(err, ErrInvalidSetting) {
			t.Fatalf("should reject %q, got %v", invalid, err)
		}
	}
}

// TestStrmRootSettingRemoved 验证旧设置不再回显或接受修改，同时保留历史记录。
func TestStrmRootSettingRemoved(t *testing.T) {
	repo := &settingsMemoryRepository{items: []ports.StoredSetting{{Key: "STRM_ROOT", Value: "/legacy/strm"}}}
	service, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	saved, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := saved.Values["STRM_ROOT"]; present {
		t.Fatal("旧根目录设置不应回显")
	}
	for _, value := range []string{"", "/strm", "/other"} {
		if _, err := service.Update(ctx, map[string]string{"STRM_ROOT": value}); !errors.Is(err, ErrInvalidSetting) {
			t.Fatalf("应拒绝旧设置更新: %v", err)
		}
	}
	if len(repo.items) != 1 || repo.items[0].Value != "/legacy/strm" {
		t.Fatal("不得修改历史根目录记录")
	}
}

// settingsMemoryRepository 只在测试中保留加密记录，用于核验保存和读取的真实业务边界。
type settingsMemoryRepository struct{ items []ports.StoredSetting }

func (r *settingsMemoryRepository) List(context.Context) ([]ports.StoredSetting, error) {
	return r.items, nil
}
func (r *settingsMemoryRepository) Upsert(_ context.Context, items []ports.StoredSetting) error {
	for _, item := range items {
		found := false
		for i := range r.items {
			if r.items[i].Key == item.Key {
				r.items[i] = item
				found = true
				break
			}
		}
		if !found {
			r.items = append(r.items, item)
		}
	}
	return nil
}

func TestSettingsSiteAuthModesAndEncryptedCredentials(t *testing.T) {
	repo := &settingsMemoryRepository{}
	service, err := NewSettingsService(repo, "sqlite", "a-development-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	values := map[string]string{
		"PTT_AUTH_TYPE": "key", "PTT_PASSKEY": "example-passkey", "PTT_UID": "123", "PTT_COOKIE": "session=example",
		"PTFANS_AUTH_TYPE": "key", "PTFANS_API_KEY": "example-ptfans",
		"ROUSIPRO_AUTH_TYPE": "cookie", "ROUSIPRO_API_KEY": "example-rousi",
		"NICEPT_AUTH_TYPE": "key", "NICEPT_API_KEY": "example-nicept",
	}
	settings, err := service.Update(ctx, values)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range values {
		if settings.Values[key] != want {
			t.Fatalf("%s did not round trip", key)
		}
	}
	for _, item := range repo.items {
		if item.Key == "PTT_PASSKEY" || item.Key == "PTT_COOKIE" || item.Key == "PTFANS_API_KEY" || item.Key == "ROUSIPRO_API_KEY" || item.Key == "NICEPT_API_KEY" {
			if !item.IsSecret || item.Value == values[item.Key] || !settings.Configured[item.Key] {
				t.Fatalf("%s must be encrypted and configured", item.Key)
			}
		}
	}
	for _, prefix := range []string{"PTT", "PTFANS", "ROUSIPRO", "NICEPT"} {
		for _, mode := range []string{"cookie", "key", ""} {
			if _, err := service.Update(ctx, map[string]string{prefix + "_AUTH_TYPE": mode}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := service.Update(ctx, map[string]string{prefix + "_AUTH_TYPE": "unknown"}); err == nil {
			t.Fatalf("%s invalid auth type accepted", prefix)
		}
	}
	for _, uid := range []string{"-1", "0", "1.5", "+1"} {
		if _, err := service.Update(ctx, map[string]string{"PTT_UID": uid}); err == nil {
			t.Fatal("non-positive or fractional UID accepted")
		}
	}
	settings, err = service.Update(ctx, map[string]string{"PTFANS_API_KEY": ""})
	if err != nil || settings.Configured["PTFANS_API_KEY"] {
		t.Fatal("blank key must clear credential")
	}
	if settings.Values["PTT_COOKIE"] != values["PTT_COOKIE"] {
		t.Fatal("unrelated credential was lost")
	}
}

func TestSiteCookieCredentialHonorsModeWithoutFallback(t *testing.T) {
	for _, prefix := range []string{"PTT", "PTFANS", "ROUSIPRO", "NICEPT"} {
		for _, mode := range []string{"", "cookie", "key", "invalid"} {
			values := map[string]string{prefix + "_AUTH_TYPE": mode, prefix + "_COOKIE": "session=example"}
			got := SiteCookieCredential(values, prefix)
			want := ""
			if mode == "cookie" {
				want = "session=example"
			}
			if got != want {
				t.Fatalf("%s mode %q used incorrect credential", prefix, mode)
			}
		}
	}
}
