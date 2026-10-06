package application

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type embyRoundTripper func(*http.Request) (*http.Response, error)

func (f embyRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEmbyMediaListOnlyQueuesMissingStrm(t *testing.T) {
	var playback int
	transport := embyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/emby/Items" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Items":[{"Id":"ok","Path":"/x/ok.strm","MediaSources":[{"RunTimeTicks":100,"MediaStreams":[{}]}]},{"Id":"missing","Path":"/x/missing.strm","MediaSources":[]},{"Id":"movie","Path":"/x/movie.mkv","MediaSources":[]}] ,"TotalRecordCount":3}`))}, nil
		}
		if r.URL.Path == "/emby/Items/missing/PlaybackInfo" {
			playback++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	s := NewEmbyMediaService(func(context.Context) (map[string]string, error) {
		return map[string]string{"EMBY_URL": "http://emby.test", "EMBY_API_KEY": "secret"}, nil
	})
	s.http.Transport = transport
	items, err := s.list(context.Background(), "http://emby.test", "secret")
	if err != nil || len(items) != 1 || items[0].ID != "missing" {
		t.Fatalf("list=%+v err=%v", items, err)
	}
	if err := s.probe(context.Background(), "http://emby.test", "secret", items[0].ID); err != nil || playback != 1 {
		t.Fatalf("probe err=%v calls=%d", err, playback)
	}
}

// TestEmbyMediaSettingsDependency prevents partial updates enabling the dependent option.
func TestEmbyMediaSettingsDependency(t *testing.T) {
	s, err := NewSettingsService(&settingsMemoryRepository{}, "sqlite", "12345678901234567890123456789012")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Update(context.Background(), map[string]string{"STRM_EMBY_MEDIA_AFTER_REFRESH": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Values["STRM_EMBY_MEDIA_AFTER_REFRESH"] != "false" {
		t.Fatal("dependency not enforced")
	}
	for _, interval := range []string{"0", "-1", "1.5", "10081"} {
		if _, err := s.Update(context.Background(), map[string]string{"STRM_EMBY_MEDIA_INTERVAL_MINUTES": interval}); err == nil {
			t.Fatalf("accepted %s", interval)
		}
	}
	if _, err := s.Update(context.Background(), map[string]string{"STRM_EMBY_MEDIA_INTERVAL_MINUTES": "60"}); err != nil {
		t.Fatal(err)
	}
}
