package downloadclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReadMetrics 确认只查询指定 hash，保留零值与缺失值差异并拒绝复合选择器。
func TestReadMetrics(t *testing.T) {
	hash := strings.Repeat("a", 40)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.WriteHeader(204)
			return
		}
		calls++
		if r.URL.Query().Get("hashes") != hash {
			t.Error(r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[{"hash":"` + hash + `","size":4096,"amount_left":0,"downloaded":4096,"dlspeed":0,"upspeed":1024,"save_path":"/downloads","ratio":1.5,"seeding_time":86400,"progress":1,"completion_on":1780000200,"added_on":1780000000}]`))
	}))
	defer server.Close()
	client := NewQbittorrent(server.URL, "user", "pass", "", "", server.Client())
	metrics, err := client.ReadMetrics(context.Background(), []string{hash, hash})
	if err != nil {
		t.Fatal(err)
	}
	m := metrics[hash]
	if m == nil || *m.SizeBytes != 4096 || *m.DownloadSpeed != 0 || *m.UploadSpeed != 1024 || *m.ShareRatio != 1.5 || *m.SeedingSeconds != 86400 || !m.Complete {
		t.Fatalf("metrics=%+v", m)
	}
	if _, err = client.ReadMetrics(context.Background(), []string{"all"}); err == nil {
		t.Fatal("accepted all")
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

// TestMetricsBatchFailure 不允许后续批次失败时将部分指标显示为完整快照。
func TestMetricsBatchFailure(t *testing.T) {
	hashes := make([]string, 51)
	for i := range hashes {
		hashes[i] = fmt.Sprintf("%040x", i+1)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.WriteHeader(204)
			return
		}
		calls++
		if calls == 2 {
			http.Error(w, "unavailable", 503)
			return
		}
		if got := len(strings.Split(r.URL.Query().Get("hashes"), "|")); got != 50 {
			t.Fatal(got)
		}
		fmt.Fprintf(w, `[{"hash":"%s"}]`, hashes[0])
	}))
	defer server.Close()
	c := NewQbittorrent(server.URL, "user", "pass", "", "", server.Client())
	got, err := c.ReadMetrics(context.Background(), hashes)
	if err == nil || got != nil || calls != 2 {
		t.Fatalf("got=%v err=%v calls=%d", got, err, calls)
	}
}
