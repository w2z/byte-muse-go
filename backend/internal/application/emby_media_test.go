package application

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type embyRoundTripper func(*http.Request) (*http.Response, error)

func (f embyRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEmbyMediaListOnlyQueuesMissingStrm(t *testing.T) {
	var playback int
	transport := embyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/emby/Items" {
			if r.URL.Query().Get("Limit") != "200" || r.URL.Query().Get("StartIndex") != "0" {
				t.Fatalf("unexpected Limit=%q", r.URL.Query().Get("Limit"))
			}
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
	items, err := s.list(context.Background(), "", "http://emby.test", "secret")
	if err != nil || len(items) != 2 || items[1].ID != "missing" || items[1].NeedsRefresh != true {
		t.Fatalf("list=%+v err=%v", items, err)
	}
	if err := s.probe(context.Background(), "http://emby.test", "secret", items[1].ID); err != nil || playback != 1 {
		t.Fatalf("probe err=%v calls=%d", err, playback)
	}
}

// 默认十个探测请求阻塞时暂停不能提前确认，停止必须取消在途请求且不重放合并任务。
func TestEmbyMediaPauseWaitsForInflightAndCancelClearsPending(t *testing.T) {
	const concurrency = 10
	entered := make(chan struct{}, concurrency*2)
	release := make(chan struct{})
	var calls atomic.Int32
	s := NewEmbyMediaService(func(context.Context) (map[string]string, error) {
		return map[string]string{"EMBY_URL": "http://emby.test", "EMBY_API_KEY": "secret"}, nil
	})
	s.http.Transport = embyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			items := make([]string, concurrency*2)
			for i := range items {
				items[i] = fmt.Sprintf(`{"Id":"%d","Path":"/%d.strm"}`, i, i)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"Items":[%s],"TotalRecordCount":%d}`, strings.Join(items, ","), len(items))))}, nil
		}
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	task, _, _ := s.Enqueue(context.Background())
	t.Cleanup(func() { _, _ = s.Control(task.ID, "cancel") })
	for i := 0; i < concurrency; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	if _, err := s.Control(task.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if got := s.Snapshot(); got.State != "pausing" {
		t.Fatalf("confirmed pause with requests inflight: %+v", got)
	}
	for i := 0; i < concurrency; i++ {
		release <- struct{}{}
	}
	waitEmbyState(t, s, "paused")
	if calls.Load() != concurrency {
		t.Fatal("dispatched during pause")
	}
	if _, created, err := s.Enqueue(context.Background()); err != nil || created {
		t.Fatal("paused task replaced")
	}
	if _, err := s.Control(task.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < concurrency; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("workers did not resume")
		}
	}
	if _, err := s.Control(task.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	got := waitEmbyState(t, s, "canceled")
	if got.Processed != concurrency || got.Failed != 0 {
		t.Fatalf("cancellation counted as failure: %+v", got)
	}
	s.mu.Lock()
	pending := s.pending
	s.mu.Unlock()
	if pending {
		t.Fatal("cancel kept pending rerun")
	}
}

func waitEmbyState(t *testing.T, s *EmbyMediaService, state string) *EmbyMediaTask {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.Snapshot(); got != nil && got.State == state {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("wanted %s, got %+v", state, s.Snapshot())
	return nil
}

func TestEmbyMediaListPaginatesBeyondOnePage(t *testing.T) {
	requests := 0
	transport := embyRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/emby/Items" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		requests++
		start := r.URL.Query().Get("StartIndex")
		if start == "0" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Items":[{"Id":"first","Path":"/first.strm"}],"TotalRecordCount":201}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Items":[{"Id":"second","Path":"/second.strm"}],"TotalRecordCount":201}`))}, nil
	})
	s := NewEmbyMediaService(nil)
	s.http.Transport = transport
	items, err := s.list(context.Background(), "", "http://emby.test", "secret")
	if err != nil || len(items) != 2 || requests != 2 {
		t.Fatalf("items=%d requests=%d err=%v", len(items), requests, err)
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

func TestEmbyMediaControlTransitions(t *testing.T) {
	s := NewEmbyMediaService(nil)
	s.task = &EmbyMediaTask{ID: "task-1", State: "running"}
	s.wake = make(chan struct{})
	if task, err := s.Control("task-1", "pause"); err != nil || task.State != "pausing" {
		t.Fatalf("pause state=%+v err=%v", task, err)
	}
	s.task.State = "paused"
	if task, err := s.Control("task-1", "resume"); err != nil || task.State != "running" {
		t.Fatalf("resume state=%+v err=%v", task, err)
	}
	if task, err := s.Control("task-1", "cancel"); err != nil || task.State != "canceling" {
		t.Fatalf("cancel state=%+v err=%v", task, err)
	}
}
