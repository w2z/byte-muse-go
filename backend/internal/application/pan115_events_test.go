package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

type eventSettingsStub struct {
	items []ports.StoredSetting
	fail  bool
}

func (s *eventSettingsStub) List(context.Context) ([]ports.StoredSetting, error) { return s.items, nil }
func (s *eventSettingsStub) Upsert(_ context.Context, items []ports.StoredSetting) error {
	if s.fail {
		return errors.New("database unavailable")
	}
	s.items = items
	return nil
}

type lifeSourceStub struct {
	pages map[int]pan115.LifePage
	calls int
	err   error
}

func (s *lifeSourceStub) LifeEvents(_ context.Context, _ string, offset, limit int) (pan115.LifePage, error) {
	s.calls++
	return s.pages[offset], s.err
}

func TestPan115EventsGenerateRetryAndResume(t *testing.T) {
	ctx := context.Background()
	values := map[string]string{"PAN115_EVENT_ENABLE": "true", "PAN115_COOKIE": "UID=123_test; CID=test; SEID=test", strmPlayBaseSettingKey: "https://media.example.com"}
	values[strmPathsSettingKey] = strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "10", Path: "/movies", LocalPath: "/movies", Formats: []string{"mkv"}}})
	files := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{}}
	root := t.TempDir()
	strm := newStrmTestService(t, root, files, nil, values)
	repo := &eventSettingsStub{}
	source := &lifeSourceStub{pages: map[int]pan115.LifePage{}}
	account := func(context.Context) (string, error) { return "123", nil }
	worker := NewPan115EventService(source, strm, repo, strmTestSettings(values), account)
	if err := worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	source.pages[0] = pan115.LifePage{Total: 1, Events: []pan115.LifeEvent{{ID: 10, Type: 2, UpdatedAt: 4102444800}}}
	before := repo.items[0].Value
	if err := worker.Poll(ctx); err == nil {
		t.Fatal("scan error must remain retryable")
	}
	if repo.items[0].Value != before {
		t.Fatal("failed scan advanced cursor")
	}
	files.pages["10"] = domain.Pan115FilePage{Files: []domain.Pan115File{{ID: "42", PickCode: "pc-42", Name: "ABC-123.mkv"}, {ID: "43", PickCode: "pc-43", Name: "readme.txt"}}}
	if err := worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "movies", "ABC-123.mkv.strm")
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "https://media.example.com/files/play/115/pc-42\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "readme.txt.strm")); !os.IsNotExist(err) {
		t.Fatal("filter ignored")
	}
	delete(files.pages, "10")
	restarted := NewPan115EventService(source, strm, repo, strmTestSettings(values), account)
	if err := restarted.Poll(ctx); err != nil {
		t.Fatalf("replayed committed event: %v", err)
	}
	values["PAN115_EVENT_ENABLE"] = "false"
	calls := source.calls
	if err := worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if source.calls != calls {
		t.Fatal("disabled worker made request")
	}
}

func TestPan115EventsAccountMismatch(t *testing.T) {
	values := map[string]string{"PAN115_EVENT_ENABLE": "true", "PAN115_COOKIE": "UID=123_test; CID=test; SEID=test"}
	source := &lifeSourceStub{}
	worker := NewPan115EventService(source, nil, &eventSettingsStub{}, strmTestSettings(values), func(context.Context) (string, error) { return "456", nil })
	if err := worker.Poll(context.Background()); err == nil {
		t.Fatal("expected account mismatch")
	}
	if source.calls != 0 {
		t.Fatal("mismatched cookie used")
	}
}

func TestPan115EventsBaselineAndEmbyRetry(t *testing.T) {
	ctx := context.Background()
	failRefresh := true
	refreshCalls := 0
	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		if r.Method != http.MethodPost || r.URL.Path != "/emby/Library/Refresh" {
			t.Error("unexpected refresh request")
		}
		if failRefresh {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer emby.Close()
	values := map[string]string{"PAN115_EVENT_ENABLE": "true", "PAN115_COOKIE": "UID=123_test; CID=test; SEID=test", strmPlayBaseSettingKey: "https://media.example.com", strmEmbyRefreshSettingKey: "true", "EMBY_URL": emby.URL, "EMBY_API_KEY": "test-key"}
	values[strmPathsSettingKey] = strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "10", Path: "/movies", LocalPath: "/movies"}})
	files := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{}}
	root := t.TempDir()
	strm := newStrmTestService(t, root, files, nil, values)
	repo := &eventSettingsStub{}
	source := &lifeSourceStub{pages: map[int]pan115.LifePage{0: {Total: 1, Events: []pan115.LifeEvent{{ID: 1, Type: 2, UpdatedAt: 20}}}}}
	worker := NewPan115EventService(source, strm, repo, strmTestSettings(values), func(context.Context) (string, error) { return "123", nil })
	if err := worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 0 {
		t.Fatal("baseline replayed historical events")
	}
	baseline := repo.items[0].Value
	files.pages["10"] = domain.Pan115FilePage{Files: []domain.Pan115File{{ID: "42", PickCode: "pc-42", Name: "film.mkv"}}}
	source.pages[0] = pan115.LifePage{Total: 1, Events: []pan115.LifeEvent{{ID: 2, Type: 2, UpdatedAt: 21}}}
	if err := worker.Poll(ctx); err == nil {
		t.Fatal("failed refresh acknowledged")
	}
	if repo.items[0].Value != baseline {
		t.Fatal("failed refresh advanced cursor")
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "film.mkv.strm")); err != nil {
		t.Fatal(err)
	}
	failRefresh = false
	if err := worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 2 || repo.items[0].Value == baseline {
		t.Fatal("refresh retry not committed")
	}
}

// TestPan115EventsRespectMappingRules 防止事件入口绕过映射范围、格式、体积或名称排除规则。
func TestPan115EventsRespectMappingRules(t *testing.T) {
	for _, kind := range []int{1, 2, 5, 6, 14, 17, 18, 20, 23, 24} {
		t.Run(strconv.Itoa(kind), func(t *testing.T) {
			ctx := context.Background()
			values := map[string]string{"PAN115_EVENT_ENABLE": "true", "PAN115_COOKIE": "UID=123_test; CID=test; SEID=test", strmPlayBaseSettingKey: "https://media.example.com"}
			values[strmPathsSettingKey] = strmTestMappings(t, []domain.StrmMapping{
				{Kind: "115", ID: "10", Path: "/movies", LocalPath: "/movies", Formats: []string{"mkv"}, MinSizeMB: 10, Exclude: []domain.StrmExcludeKeyword{{Mode: "contains", Value: "sample"}}},
				{Kind: "115", ID: "20", Path: "/series", LocalPath: "/series", Formats: []string{"mp4"}},
				{Kind: "cd2", ID: "/cloud", Path: "/cloud", LocalPath: "/cloud"},
			})
			files := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
				"10": {Files: []domain.Pan115File{
					{ID: "41", PickCode: "pc-41", Name: "accepted.mkv", Size: 10 * 1024 * 1024},
					{ID: "42", PickCode: "pc-42", Name: "small.mkv", Size: 10*1024*1024 - 1},
					{ID: "43", PickCode: "pc-43", Name: "wrong.mp4", Size: 20 * 1024 * 1024},
					{ID: "44", PickCode: "pc-44", Name: "SAMPLE.mkv", Size: 20 * 1024 * 1024},
					{ID: "excluded", Name: "Samples", IsDirectory: true},
					{ID: "11", Name: "nested", IsDirectory: true},
				}},
				"11":         {Files: []domain.Pan115File{{ID: "45", PickCode: "pc-45", Name: "nested.mkv", Size: 20 * 1024 * 1024}}},
				"20":         {Files: []domain.Pan115File{{ID: "46", PickCode: "pc-46", Name: "episode.mp4"}, {ID: "47", PickCode: "pc-47", Name: "wrong.mkv"}}},
				"unselected": {Files: []domain.Pan115File{{ID: "48", PickCode: "pc-48", Name: "outside.mkv", Size: 20 * 1024 * 1024}}},
			}}
			root := t.TempDir()
			strm := newStrmTestService(t, root, files, nil, values)
			repo := &eventSettingsStub{items: []ports.StoredSetting{{Key: pan115EventCursorKey, Value: `{"UserID":"123","ID":1}`}}}
			source := &lifeSourceStub{pages: map[int]pan115.LifePage{0: {Total: 1, Events: []pan115.LifeEvent{{ID: 2, Type: kind, UpdatedAt: 20}}}}}
			worker := NewPan115EventService(source, strm, repo, strmTestSettings(values), func(context.Context) (string, error) { return "123", nil })
			if err := worker.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			var actual []string
			err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() {
					rel, _ := filepath.Rel(root, p)
					actual = append(actual, filepath.ToSlash(rel))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"movies/accepted.mkv.strm", "movies/nested/nested.mkv.strm", "series/episode.mp4.strm"}
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("files=%v want=%v", actual, want)
			}
			target := filepath.Join(root, "movies", "accepted.mkv.strm")
			old := time.Unix(1700000000, 0)
			if err := os.Chtimes(target, old, old); err != nil {
				t.Fatal(err)
			}
			source.pages[0] = pan115.LifePage{Total: 1, Events: []pan115.LifeEvent{{ID: 3, Type: kind, UpdatedAt: 21}}}
			if err := worker.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if !info.ModTime().Equal(old) {
				t.Fatal("unchanged STRM rewritten")
			}
			values[strmPlayBaseSettingKey] = "https://new.example.com"
			source.pages[0] = pan115.LifePage{Total: 1, Events: []pan115.LifeEvent{{ID: 4, Type: kind, UpdatedAt: 22}}}
			if err := worker.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(target)
			if err != nil || string(content) != "https://new.example.com/files/play/115/pc-41\n" {
				t.Fatalf("update=%q err=%v", content, err)
			}
		})
	}
}

func TestPan115EventsPaginationAndPersistenceFailure(t *testing.T) {
	ctx := context.Background()
	values := map[string]string{"PAN115_EVENT_ENABLE": "true", "PAN115_COOKIE": "UID=123_test; CID=test; SEID=test", strmPlayBaseSettingKey: "https://media.example.com"}
	values[strmPathsSettingKey] = strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "10", Path: "/movies", LocalPath: "/movies"}, {Kind: "cd2", ID: "/cloud", Path: "/cloud", LocalPath: "/cloud"}})
	baseline, _ := json.Marshal(pan115EventCursor{UserID: "123", ID: 5})
	repo := &eventSettingsStub{items: []ports.StoredSetting{{Key: pan115EventCursorKey, Value: string(baseline)}}}
	source := &lifeSourceStub{pages: map[int]pan115.LifePage{
		0: {Total: 3, Events: []pan115.LifeEvent{{ID: 8, Type: 8, UpdatedAt: 20}}},
		1: {Total: 3, Events: []pan115.LifeEvent{{ID: 7, Type: 2, UpdatedAt: 20}, {ID: 5, Type: 2, UpdatedAt: 20}}},
	}}
	files := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{"10": {Files: []domain.Pan115File{{ID: "42", PickCode: "pc-42", Name: "film.mkv"}}}}}
	root := t.TempDir()
	strm := newStrmTestService(t, root, files, nil, values)
	worker := NewPan115EventService(source, strm, repo, strmTestSettings(values), func(context.Context) (string, error) { return "123", nil })
	repo.fail = true
	if err := worker.Poll(ctx); err == nil {
		t.Fatal("failed checkpoint must be reported")
	}
	if repo.items[0].Value != string(baseline) {
		t.Fatal("checkpoint advanced")
	}
	repo.fail = false
	if err := worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if source.calls != 4 {
		t.Fatalf("pagination requests=%d", source.calls)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "film.mkv.strm")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cloud")); !os.IsNotExist(err) {
		t.Fatal("CD2 must not be scanned")
	}
	// A rename produces the new path without deleting an existing STRM.
	files.pages["10"] = domain.Pan115FilePage{Files: []domain.Pan115File{{ID: "42", PickCode: "pc-42", Name: "renamed.mkv"}}}
	source.pages[0] = pan115.LifePage{Total: 1, Events: []pan115.LifeEvent{{ID: 9, Type: 24, UpdatedAt: 21}}}
	if err := worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"film.mkv.strm", "renamed.mkv.strm"} {
		if _, err := os.Stat(filepath.Join(root, "movies", name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPan115EventSettingsEncryptAndHideCursor(t *testing.T) {
	ctx := context.Background()
	repo := &settingsMemoryRepository{}
	service, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_test; CID=test; SEID=test"
	if _, err := service.Update(ctx, map[string]string{"PAN115_EVENT_ENABLE": "true", "PAN115_COOKIE": cookie}); err != nil {
		t.Fatal(err)
	}
	items, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Key == "PAN115_COOKIE" && (!item.IsSecret || item.Value == cookie) {
			t.Fatal("cookie not encrypted")
		}
	}
	if err := repo.Upsert(ctx, []ports.StoredSetting{{Key: pan115EventCursorKey, Value: "internal"}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Values[pan115EventCursorKey]; ok {
		t.Fatal("internal cursor exposed")
	}
	for key, value := range map[string]string{pan115EventCursorKey: "override", "PAN115_COOKIE": "invalid", "PAN115_EVENT_ENABLE": "yes"} {
		if _, err := service.Update(ctx, map[string]string{key: value}); !errors.Is(err, ErrInvalidSetting) {
			t.Fatalf("%s accepted", key)
		}
	}
}

func TestPan115EventCookieControlsEnable(t *testing.T) {
	ctx := context.Background()
	repo := &settingsMemoryRepository{}
	service, err := NewSettingsService(repo, "sqlite", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		values  map[string]string
		enabled string
	}{
		{map[string]string{"PAN115_EVENT_ENABLE": "true"}, "false"},
		{map[string]string{"PAN115_COOKIE": "UID=123_test; CID=test; SEID=test"}, "false"},
		{map[string]string{"PAN115_EVENT_ENABLE": "true"}, "true"},
		{map[string]string{"PAN115_COOKIE": ""}, "false"},
		{map[string]string{"PAN115_EVENT_ENABLE": "true", "PAN115_COOKIE": "   "}, "false"},
	} {
		result, err := service.Update(ctx, tc.values)
		if err != nil {
			t.Fatal(err)
		}
		if result.Values["PAN115_EVENT_ENABLE"] != tc.enabled {
			t.Fatalf("enabled=%q want=%q", result.Values["PAN115_EVENT_ENABLE"], tc.enabled)
		}
		stored, _ := repo.List(ctx)
		for _, item := range stored {
			if item.Key == "PAN115_EVENT_ENABLE" && item.Value != tc.enabled {
				t.Fatal("persisted switch differs from response")
			}
		}
	}
}
