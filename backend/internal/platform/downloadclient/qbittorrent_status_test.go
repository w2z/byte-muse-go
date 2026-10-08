package downloadclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestTransferStatesLargeLibrary 验证超过单次响应上限的任务库可完整分页读取，后续页失败时不返回部分快照。
func TestTransferStatesLargeLibrary(t *testing.T) {
	for _, failLater := range []bool{false, true} {
		t.Run(fmt.Sprintf("later_failure_%t", failLater), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/auth/login" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				if limit <= 0 {
					limit = 1580
				}
				if failLater && offset > 0 {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				rows := make([]map[string]any, 0)
				for i := offset; i < min(offset+limit, 1580); i++ {
					rows = append(rows, map[string]any{"hash": fmt.Sprintf("%040x", i+1), "state": "downloading", "progress": 0.5, "name": strings.Repeat("x", 2048)})
				}
				_ = json.NewEncoder(w).Encode(rows)
			}))
			defer server.Close()
			client := NewQbittorrent(server.URL, "user", "pass", "", "", server.Client())
			items, err := client.ListTransferStates(context.Background())
			if failLater {
				if err == nil || !strings.Contains(err.Error(), "503") || items != nil {
					t.Fatalf("partial snapshot: count=%d err=%v", len(items), err)
				}
				return
			}
			if err != nil || len(items) != 1580 {
				t.Fatalf("count=%d err=%v", len(items), err)
			}
			seen := make(map[string]bool)
			for _, item := range items {
				seen[item.Hash] = true
			}
			if len(seen) != 1580 || items[1579].Status != "downloading" {
				t.Fatalf("missing or duplicate torrents: %d", len(seen))
			}
		})
	}
}

func TestQbittorrentListTransferStates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			if _, err := r.Cookie("SID"); err != nil {
				t.Error("missing login cookie")
			}
			_, _ = w.Write([]byte(`[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","state":"stoppedDL","progress":0.25,"added_on":1780000000,"completion_on":-1},{"hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","state":"queuedUP","progress":1,"added_on":1780000100,"completion_on":1780000200},{"hash":"cccccccccccccccccccccccccccccccccccccccc","state":"errorDL","progress":0.1,"added_on":1780000300,"completion_on":-1}]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewQbittorrent(server.URL, "user", "pass", "", "", server.Client())
	items, err := client.ListTransferStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Status != "stopped" || items[1].Status != "completed" || items[1].CompletedAt == nil || items[2].Status != "failed" {
		t.Fatalf("states=%+v", items)
	}
}

func TestTransferStatusReportsErrorBeforeHistoricCompletion(t *testing.T) {
	if got := transferStatus("errorUP", 1, 1780000200); got != "failed" {
		t.Fatalf("error after completion status=%s", got)
	}
}
