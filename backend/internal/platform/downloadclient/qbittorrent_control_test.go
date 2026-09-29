package downloadclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// TestQbittorrentControlVersionAndDelete 验证版本能力、真实控制路径和文件删除参数。
func TestQbittorrentControlVersionAndDelete(t *testing.T) {
	for _, tc := range []struct {
		version, action, path string
		stop                  bool
	}{{"v4.6.7", "pause", "pause", false}, {"v5.2.3", "stop", "stop", true}, {"v4.6.7", "resume", "resume", false}, {"v5.2.3", "resume", "start", true}, {"v5.2.3", "delete", "delete", true}, {"v5.2.3", "delete_files", "delete", true}} {
		t.Run(tc.version+tc.action, func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					w.Write([]byte("Ok."))
				case "/api/v2/app/version":
					w.Write([]byte(tc.version))
				case "/api/v2/torrents/" + tc.path:
					called = true
					r.ParseForm()
					if r.Method != "POST" || r.Form.Get("hashes") != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
						t.Errorf("wrong targeted request")
					}
					if tc.path == "delete" && r.Form.Get("deleteFiles") != map[bool]string{true: "true", false: "false"}[tc.action == "delete_files"] {
						t.Errorf("wrong delete mode")
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c := NewQbittorrent(server.URL, "user", "pass", "", "", server.Client())
			caps, err := c.Capabilities(context.Background())
			if err != nil || slices.Contains(caps, "stop") != tc.stop {
				t.Fatalf("caps=%v err=%v", caps, err)
			}
			if err = c.Control(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tc.action); err != nil || !called {
				t.Fatalf("called=%v err=%v", called, err)
			}
		})
	}
}
