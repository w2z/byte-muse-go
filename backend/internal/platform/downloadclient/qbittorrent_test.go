package downloadclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestQbittorrentAcceptsNoContentLogin 覆盖 qBittorrent 5.x 登录返回 204 空响应的情况：
// 旧实现只接受 200 + "Ok."，会让状态同步和提交全部失败。
func TestQbittorrentAcceptsNoContentLogin(t *testing.T) {
	added := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_8081", Value: "session", Path: "/"})
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/torrents/info":
			if cookie, e := r.Cookie("QBT_SID_8081"); e != nil || cookie.Value != "session" {
				t.Error("missing session cookie")
			}
			_, _ = w.Write([]byte("[{\"hash\":\"0123456789abcdef0123456789abcdef01234567\"}]"))
		case "/api/v2/torrents/add":
			added++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewQbittorrent(server.URL, "user", "pass", "/media", "ByteMuse", server.Client())
	found, e := client.HasHash(context.Background(), "0123456789abcdef0123456789abcdef01234567")
	if e != nil || !found {
		t.Fatalf("found=%v error=%v", found, e)
	}
	if e = client.Submit(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"); e != nil {
		t.Fatal(e)
	}
	if added != 1 {
		t.Fatalf("add calls=%d", added)
	}
}

// TestQbittorrentRejectsFailedLogin 保证放宽状态码后仍然拒绝显式的失败响应。
func TestQbittorrentRejectsFailedLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("Fails."))
	}))
	defer server.Close()
	client := NewQbittorrent(server.URL, "user", "wrong", "", "", server.Client())
	if _, e := client.HasHash(context.Background(), "0123456789abcdef0123456789abcdef01234567"); e == nil {
		t.Fatal("failed login must be rejected")
	}
}

func TestQbittorrentRechecksHashBeforeSubmission(t *testing.T) {
	added := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte("[{\"hash\":\"0123456789abcdef0123456789abcdef01234567\"}]"))
		case "/api/v2/torrents/add":
			added++
			_, _ = w.Write([]byte("Ok."))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewQbittorrent(server.URL, "user", "pass", "/media", "ByteMuse", server.Client())
	found, e := client.HasHash(context.Background(), "0123456789abcdef0123456789abcdef01234567")
	if e != nil || !found {
		t.Fatalf("found=%v error=%v", found, e)
	}
	if added != 0 {
		t.Fatalf("duplicate submission: %d", added)
	}
}

func TestQbittorrentSubmitsMagnetWithConfiguredPath(t *testing.T) {
	added := url.Values{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			if cookie, e := r.Cookie("SID"); e != nil || cookie.Value != "session" {
				t.Error("missing session cookie")
			}
			_ = r.ParseForm()
			added = r.PostForm
			_, _ = w.Write([]byte("Ok."))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewQbittorrent(server.URL, "user", "pass", "/media", "ByteMuse", server.Client())
	e := client.Submit(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(added.Get("urls"), "magnet:") || added.Get("savepath") != "/media" || added.Get("category") != "ByteMuse" {
		t.Fatalf("add form=%v", added)
	}
}

func TestQbittorrentUploadsPrivateTorrentFile(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
			}
			file, _, e := r.FormFile("torrents")
			if e != nil {
				t.Error(e)
			} else {
				data := make([]byte, 64)
				n, _ := file.Read(data)
				received = string(data[:n])
				_ = file.Close()
			}
			if r.FormValue("savepath") != "/media" {
				t.Error("missing save path")
			}
			_, _ = w.Write([]byte("Ok."))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewQbittorrent(server.URL, "user", "pass", "/media", "ByteMuse", server.Client())
	if e := client.SubmitTorrent(context.Background(), []byte("d4:infod4:name4:testee")); e != nil {
		t.Fatal(e)
	}
	if received != "d4:infod4:name4:testee" {
		t.Fatalf("uploaded=%q", received)
	}
}

// TestQbittorrentNullListIsUnknown 不把服务端无效空响应当作任务已删除。
func TestQbittorrentNullListIsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
		} else {
			w.Write([]byte("null"))
		}
	}))
	defer server.Close()
	if _, err := NewQbittorrent(server.URL, "user", "pass", "", "", server.Client()).HasHash(context.Background(), "hash"); err == nil {
		t.Fatal("null must remain unknown")
	}
}
