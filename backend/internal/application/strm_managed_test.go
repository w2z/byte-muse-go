package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

type managedRepoStub struct {
	mu    sync.Mutex
	items map[string]ports.StrmFileRecord
	fail  bool
}

func (r *managedRepoStub) Save(_ context.Context, item ports.StrmFileRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("store unavailable")
	}
	r.items[item.Key] = item
	return nil
}
func (r *managedRepoStub) List(_ context.Context, scope string) ([]ports.StrmFileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []ports.StrmFileRecord
	for _, item := range r.items {
		if item.Scope == scope {
			result = append(result, item)
		}
	}
	return result, nil
}
func (r *managedRepoStub) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("store unavailable")
	}
	delete(r.items, key)
	return nil
}

type managedPanStub struct {
	*strmPan115Stub
	infos     map[string]pan115.FileInfo
	infoErr   error
	infoCalls int
}

func (s *managedPanStub) EventFileInfo(_ context.Context, id string) (pan115.FileInfo, error) {
	s.infoCalls++
	if s.infoErr != nil {
		return pan115.FileInfo{}, s.infoErr
	}
	if info, ok := s.infos[id]; ok {
		return info, nil
	}
	return pan115.FileInfo{}, pan115.ErrFileNotFound
}

func managedFixture(t *testing.T) (*StrmService, *managedPanStub, *managedRepoStub, domain.StrmMapping, map[string]string) {
	t.Helper()
	mapping := domain.StrmMapping{Kind: "115", ID: "10", Path: "/movies", LocalPath: "/movies", Formats: []string{"mkv"}}
	values := map[string]string{strmPlayBaseSettingKey: "https://media.example.com", strmPathsSettingKey: strmTestMappings(t, []domain.StrmMapping{mapping})}
	api := &managedPanStub{strmPan115Stub: &strmPan115Stub{pages: map[string]domain.Pan115FilePage{"10": {}}}, infos: map[string]pan115.FileInfo{
		"42": {File: pan115.File{ID: "42", ParentID: "10", Name: "movie.mkv", PickCode: "pc42"}, Path: []pan115.Directory{{ID: "10", Name: "movies"}}},
	}}
	repo := &managedRepoStub{items: map[string]ports.StrmFileRecord{}}
	service := newStrmTestService(t, t.TempDir(), api, nil, values)
	service.SetManagedFiles(repo, func(context.Context) (string, error) { return "123", nil })
	return service, api, repo, mapping, values
}

func TestManagedEventsGenerateDeleteAndRetry(t *testing.T) {
	service, api, repo, mapping, values := managedFixture(t)
	ctx := context.Background()
	generate := []pan115.LifeEvent{{Type: 2, FileID: "42", ParentID: "10"}}
	result, err := service.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, generate, false)
	if err != nil || result.Created != 1 || len(repo.items) != 1 {
		t.Fatalf("generate=%+v records=%d err=%v", result, len(repo.items), err)
	}
	path := filepath.Join(service.root, "movies", "movie.strm")
	unknown := filepath.Join(service.root, "movies", "unknown.strm")
	if err := os.WriteFile(unknown, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	delete(api.infos, "42")
	remove := []pan115.LifeEvent{{Type: 22, FileID: "42", ParentID: "10"}}
	api.infoErr = errors.New("已达到当前访问上限")
	if _, err = service.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, remove, false); err == nil {
		t.Fatal("limit treated as deletion")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("removed on limit", err)
	}
	api.infoErr = nil
	repo.fail = true
	if _, err = service.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, remove, false); err == nil {
		t.Fatal("database failure hidden")
	}
	repo.fail = false
	result, err = service.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, remove, false)
	if err != nil || len(repo.items) != 0 {
		t.Fatalf("retry=%+v records=%d err=%v", result, len(repo.items), err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("managed file remains", err)
	}
	if _, err = os.Stat(unknown); err != nil {
		t.Fatal("unknown file removed", err)
	}
}

func TestManagedEventsProtectModifiedAndMovedDescendant(t *testing.T) {
	for _, modified := range []bool{false, true} {
		t.Run(map[bool]string{false: "moved child", true: "modified local"}[modified], func(t *testing.T) {
			service, api, repo, mapping, values := managedFixture(t)
			ctx := context.Background()
			info := api.infos["42"]
			info.ParentID = "20"
			info.Path = append(info.Path, pan115.Directory{ID: "20", Name: "folder"})
			api.infos["42"] = info
			if _, err := service.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, []pan115.LifeEvent{{Type: 2, FileID: "42"}}, false); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(service.root, "movies", "folder", "movie.strm")
			if modified {
				if err := os.WriteFile(path, []byte("user edited"), 0600); err != nil {
					t.Fatal(err)
				}
				delete(api.infos, "42")
			}
			if _, err := service.syncPan115Events(ctx, []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, []pan115.LifeEvent{{Type: 22, FileID: "20", ParentID: "10"}}, false); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("protected file removed", err)
			}
			if len(repo.items) != 1 {
				t.Fatal("protected ownership lost")
			}
		})
	}
}

func TestManagedGenerationStoreFailureDoesNotLoseOwnership(t *testing.T) {
	service, _, repo, mapping, values := managedFixture(t)
	events := []pan115.LifeEvent{{Type: 2, FileID: "42"}}
	repo.fail = true
	if _, err := service.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, events, false); err == nil {
		t.Fatal("missing failure")
	}
	repo.fail = false
	if _, err := service.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, events, false); err != nil {
		t.Fatal(err)
	}
	if len(repo.items) != 1 {
		t.Fatal("retry lost ownership of newly generated file")
	}
}

func TestManagedFolderDeletesOnlyTrackedFilesAndDownloads(t *testing.T) {
	service, api, repo, mapping, values := managedFixture(t)
	values[strmDownloadEnableSettingKey] = "true"
	values[strmDownloadExtensionsSettingKey] = `["jpg"]`
	api.playURL = "https://download.example.com/poster"
	api.infos["20"] = pan115.FileInfo{File: pan115.File{ID: "20", ParentID: "10", Name: "folder", IsDirectory: true}, Path: []pan115.Directory{{ID: "10", Name: "movies"}}}
	api.pages["20"] = domain.Pan115FilePage{Files: []domain.Pan115File{{ID: "42", Name: "movie.mkv", PickCode: "pc42"}, {ID: "43", Name: "poster.jpg", PickCode: "pc43"}}}
	service.http.Transport = embyRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("poster-content"))}, nil
	})
	result, err := service.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, []pan115.LifeEvent{{Type: 17, FileID: "20"}}, false)
	if err != nil || result.Files != 1 || result.Downloaded != 1 || len(repo.items) != 2 {
		t.Fatalf("generated=%+v records=%d err=%v", result, len(repo.items), err)
	}
	delete(api.infos, "20")
	delete(api.infos, "42")
	result, err = service.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, []pan115.LifeEvent{{Type: 22, FileID: "20", ParentID: "10"}}, false)
	if err != nil || result.Deleted != 2 || len(repo.items) != 0 {
		t.Fatalf("deleted=%+v records=%d err=%v", result, len(repo.items), err)
	}
	if _, err := os.Stat(filepath.Join(service.root, "movies", "folder")); !os.IsNotExist(err) {
		t.Fatal("empty directory remains", err)
	}
	if _, err := os.Stat(filepath.Join(service.root, "movies")); err != nil {
		t.Fatal("mapping root deleted", err)
	}
}

func TestManagedDeleteCursorRestartAndIsolation(t *testing.T) {
	service, api, repo, mapping, values := managedFixture(t)
	values["PAN115_EVENT_ENABLE"] = "true"
	values["PAN115_COOKIE"] = "UID=123_test; CID=test; SEID=test"
	mappings, _ := parseStrmMappings(values[strmPathsSettingKey])
	mapping = mappings[0]
	if _, err := service.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, []pan115.LifeEvent{{Type: 2, FileID: "42"}}, false); err != nil {
		t.Fatal(err)
	}
	settings := &eventSettingsStub{}
	source := &lifeSourceStub{pages: map[int]pan115.LifePage{}}
	worker := NewPan115EventService(source, service, settings, strmTestSettings(values), service.managedAccount)
	if err := worker.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := settings.items[0].Value
	source.pages[0] = pan115.LifePage{Total: 1, Events: []pan115.LifeEvent{{ID: 100, Type: 22, FileID: "42", ParentID: "10", UpdatedAt: 4102444800}}}
	delete(api.infos, "42")
	api.infoErr = errors.New("network error")
	if err := worker.Poll(context.Background()); err == nil || settings.items[0].Value != before {
		t.Fatal("failed deletion acknowledged")
	}
	api.infoErr = nil
	worker = NewPan115EventService(source, service, settings, strmTestSettings(values), service.managedAccount)
	if err := worker.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.items) != 0 || settings.items[0].Value == before {
		t.Fatal("restart did not finish deletion")
	}
	if err := worker.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagedDeleteScopeAndPathProtection(t *testing.T) {
	service, api, repo, mapping, values := managedFixture(t)
	if _, err := service.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, []pan115.LifeEvent{{Type: 2, FileID: "42"}}, false); err != nil {
		t.Fatal(err)
	}
	delete(api.infos, "42")
	service.managedAccount = func(context.Context) (string, error) { return "456", nil }
	if deleted, err := service.deletePan115Event(context.Background(), pan115.LifeEvent{Type: 22, FileID: "42", ParentID: "10"}, []domain.StrmMapping{mapping}, nil); err != nil || deleted != 0 {
		t.Fatal("account scope violated", err)
	}
	for _, item := range repo.items {
		item.RelativePath = filepath.Join("..", "outside.strm")
		if _, err := service.removeManagedFile(context.Background(), item, mapping); err == nil {
			t.Fatal("unsafe path accepted")
		}
	}
}

func TestManagedDigestRejectsSymlink(t *testing.T) {
	rootPath := t.TempDir()
	outside := filepath.Join(t.TempDir(), "external.strm")
	if err := os.WriteFile(outside, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "link.strm")); err != nil {
		t.Skip("system does not permit test symlinks: ", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := managedDigest(root, "link.strm"); err == nil {
		t.Fatal("symlink accepted")
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "external" {
		t.Fatal("outside file changed")
	}
}

func TestManagedEventGenerationUsesOnlyChangedFile(t *testing.T) {
	service, api, _, mapping, values := managedFixture(t)
	api.pages = nil
	if result, err := service.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, []pan115.LifeEvent{{Type: 2, FileID: "42"}, {Type: 2, FileID: "42"}}, false); err != nil || result.Created != 1 || api.infoCalls != 1 {
		t.Fatalf("result=%+v infoCalls=%d err=%v", result, api.infoCalls, err)
	}
}

// TestManagedEventsRejectNameCollision 保护同批和跨批事件中已生成文件的播放地址与归属。
func TestManagedEventsRejectNameCollision(t *testing.T) {
	for _, sameBatch := range []bool{true, false} {
		t.Run(fmt.Sprint(sameBatch), func(t *testing.T) {
			svc, api, repo, mapping, values := managedFixture(t)
			mapping.Formats = []string{"mkv", "mp4"}
			api.infos["43"] = pan115.FileInfo{File: pan115.File{ID: "43", ParentID: "10", Name: "movie.mp4", PickCode: "pc43"}, Path: []pan115.Directory{{ID: "10", Name: "movies"}}}
			events := []pan115.LifeEvent{{Type: 2, FileID: "42"}, {Type: 2, FileID: "43"}}
			if !sameBatch {
				if _, err := svc.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, events[:1], false); err != nil {
					t.Fatal(err)
				}
				events = events[1:]
			}
			_, err := svc.syncPan115Events(context.Background(), []domain.StrmMapping{mapping}, values[strmPlayBaseSettingKey], values, events, false)
			if err == nil || !strings.Contains(err.Error(), "文件名冲突") {
				t.Fatalf("expected collision, got %v", err)
			}
			data, err := os.ReadFile(filepath.Join(svc.root, "movies", "movie.strm"))
			if err != nil || !strings.HasSuffix(strings.TrimSpace(string(data)), "/pc42") {
				t.Fatalf("content=%q err=%v", data, err)
			}
			for _, record := range repo.items {
				if record.FileID != "42" {
					t.Fatalf("owner changed: %+v", record)
				}
			}
		})
	}
}
