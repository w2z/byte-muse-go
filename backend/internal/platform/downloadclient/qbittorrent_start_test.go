package downloadclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestQbittorrentStartUsesVersionEndpoint 验证启动命令按 qBittorrent 主版本选择端点：
// 5.x 只有 /torrents/start，4.x 仍是 /torrents/resume，用错端点会静默失败（404）。
func TestQbittorrentStartUsesVersionEndpoint(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct{ version, wantPath string }{
		{"v5.3.0", "/api/v2/torrents/start"},
		{"v4.6.7", "/api/v2/torrents/resume"},
	} {
		var called string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				_, _ = w.Write([]byte("Ok."))
			case "/api/v2/app/version":
				_, _ = w.Write([]byte(tc.version))
			case "/api/v2/torrents/start", "/api/v2/torrents/resume":
				_ = r.ParseForm()
				if r.PostForm.Get("hashes") != hash {
					t.Errorf("hashes = %q", r.PostForm.Get("hashes"))
				}
				called = r.URL.Path
				_, _ = w.Write([]byte(""))
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
			}
		}))
		client := NewQbittorrent(server.URL, "user", "pass", "/media", "ByteMuse", server.Client())
		if err := client.Start(context.Background(), hash); err != nil {
			t.Fatalf("%s 启动失败: %v", tc.version, err)
		}
		if called != tc.wantPath {
			t.Fatalf("%s 调用 %q, 期望 %q", tc.version, called, tc.wantPath)
		}
		server.Close()
	}
}

// TestQbittorrentStartRejectsInvalidHash 验证非法哈希在发请求前就被拒绝。
func TestQbittorrentStartRejectsInvalidHash(t *testing.T) {
	client := NewQbittorrent("http://127.0.0.1:1", "user", "pass", "/media", "ByteMuse", http.DefaultClient)
	if err := client.Start(context.Background(), "not-a-hash"); err == nil {
		t.Fatal("非法哈希应当报错")
	}
}
