package downloadclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
