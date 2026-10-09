package torrentsearch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// TestNexusKeyLiveOnlyFetch 仅在显式提供环境凭据时读取源站；不连接下载客户端、不落盘种子。
func TestNexusKeyLiveOnlyFetch(t *testing.T) {
	for _, site := range []string{"PTFANS", "NICEPT"} {
		t.Run(site, func(t *testing.T) {
			key := os.Getenv("BYTEMUSE_TEST_" + site + "_KEY")
			if key == "" {
				t.Skip("未提供临时实测令牌")
			}
			code := "JUR-772"
			adapter := NewNicePTKeySearcher(nil, "", key)
			if site == "PTFANS" {
				code = "112325_001"
				adapter = NewPTFansKeySearcher(nil, "", key)
			}
			items, err := adapter.Search(context.Background(), code)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) == 0 {
				t.Fatal("真实搜索无匹配，未进行下载验证")
			}
			data, hash, downloadURL, err := adapter.Download(context.Background(), items[0].URI)
			if downloadURL == "" {
				t.Fatal("actual URL missing")
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("site=%s results=%d reference=%s bytes=%d infohash=%s sha256=%x", site, len(items), items[0].URI, len(data), hash, sha256.Sum256(data))
		})
	}
}

// 验证真实 HTTP 边界：令牌用于 API，个人下载地址仅用于本站取种。
func TestNexusKeySearchDownload(t *testing.T) {
	for _, site := range []string{"ptfans", "nicept"} {
		t.Run(site, func(t *testing.T) {
			listPath := "/api/v1/torrents"
			if site == "ptfans" {
				listPath += "/cas"
			}
			pages := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "" {
					t.Error("key mode sent cookie")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case listPath:
					pages++
					if r.Header.Get("Authorization") != "Bearer fake-token" || r.URL.Query().Get("filter[title]") != "TEST-123" {
						t.Error("wrong key search request")
					}
					if r.URL.Query().Get("page") == "1" {
						fmt.Fprint(w, `{"ret":0,"data":{"data":[{"id":12,"name":"TEST-123 中文字幕 4K","small_descr":"","size":1048576,"seeders":8,"promotion_info":{"down_multiplier":0}},{"id":14,"name":"TEST-1234","size":1048576}],"meta":{"current_page":1,"last_page":2}}}`)
					} else {
						fmt.Fprint(w, `{"ret":0,"data":{"data":[{"id":13,"name":"TEST-123","size":2097152,"seeders":2,"promotion_info":{"down_multiplier":0.5}}],"meta":{"current_page":2,"last_page":2}}}`)
					}
				case "/api/v1/detail/12":
					if r.Header.Get("Authorization") != "Bearer fake-token" || r.URL.Query().Get("include_fields[torrent]") != "download_url" {
						t.Error("wrong detail request")
					}
					fmt.Fprint(w, `{"ret":0,"data":{"data":{"download_url":"/download.php?id=12&passkey=fake-passkey"}}}`)
				case "/download.php":
					if r.URL.Query().Get("passkey") != "fake-passkey" || r.Header.Get("Authorization") != "" {
						t.Error("wrong download credential boundary")
					}
					w.Header().Set("Content-Type", "application/x-bittorrent")
					fmt.Fprint(w, "d8:announce14:http://tracker4:infod4:name4:testee")
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			client := srv.Client()
			client.Jar, _ = cookiejar.New(nil)
			u, _ := url.Parse(srv.URL)
			client.Jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "old"}})
			adapter := NewNicePTKeySearcher(client, srv.URL, "fake-token")
			if site == "ptfans" {
				adapter = NewPTFansKeySearcher(client, srv.URL, "fake-token")
			}
			items, err := adapter.Search(context.Background(), "TEST-123")
			if err != nil || len(items) != 2 || pages != 2 {
				t.Fatalf("items=%v pages=%d err=%v", items, pages, err)
			}
			if items[0].URI != site+":12" || items[0].SizeMB != 1 || items[0].Seeders != 8 || !items[0].Free || !items[0].Chinese || !items[0].UHD || items[1].Free {
				t.Fatalf("wrong resources: %+v", items)
			}
			data, hash, downloadURL, err := adapter.Download(context.Background(), items[0].URI)
			if downloadURL != srv.URL+"/download.php?id=12&passkey=fake-passkey" {
				t.Fatal("actual URL missing")
			}
			if err != nil || len(data) == 0 || hash != "1ade8a1a581f338e4fce4ce784da3f7d03f81f3a" {
				t.Fatalf("download hash=%s err=%v", hash, err)
			}
		})
	}
}

func TestNexusKeyRejectsInvalidSearchResponses(t *testing.T) {
	for _, body := range []string{`{"ret":-1,"msg":"fake-secret"}`, `{}`, `{"ret":0,"data":{}}`, `<html>login</html>`, `{"ret":0,"data":{"data":[],"meta":{"current_page":1,"last_page":2}}}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		_, err := NewNicePTKeySearcher(srv.Client(), srv.URL, "fake-secret").Search(context.Background(), "TEST-123")
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "fake-secret") {
			t.Fatalf("unsafe/missing error: %v", err)
		}
	}
}

func TestNexusKeyEmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ret":0,"data":{"data":[],"meta":{"current_page":1,"last_page":1}}}`)
	}))
	defer srv.Close()
	items, err := NewNicePTKeySearcher(srv.Client(), srv.URL, "fake").Search(context.Background(), "TEST-123")
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestNexusKeyRejectsRepeatedFinalPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ret":0,"data":{"data":[{"id":12,"name":"TEST-123","size":1048576}],"meta":{"current_page":%s,"last_page":2}}}`, r.URL.Query().Get("page"))
	}))
	defer srv.Close()
	if _, err := NewNicePTKeySearcher(srv.Client(), srv.URL, "fake").Search(context.Background(), "TEST-123"); err == nil {
		t.Fatal("repeated final page accepted as complete")
	}
}

func TestNexusKeyRejectsUnsafeDownloads(t *testing.T) {
	for _, link := range []string{"https://evil.invalid/download.php?passkey=fake-secret", "//evil.invalid/download.php", "/account/delete", "/download.php?id=12#secret"} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			fmt.Fprintf(w, `{"ret":0,"data":{"data":{"download_url":%q}}}`, link)
		}))
		_, _, _, err := NewNicePTKeySearcher(srv.Client(), srv.URL, "fake").Download(context.Background(), "nicept:12")
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "fake-secret") || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}

func TestNexusKeyRejectsRedirectAndBadTorrent(t *testing.T) {
	for _, redirect := range []bool{true, false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				fmt.Fprint(w, `{"ret":0,"data":{"data":{"download_url":"/download.php?id=12&passkey=fake-secret"}}}`)
				return
			}
			if redirect {
				http.Redirect(w, r, "/login?secret=fake-secret", 302)
			} else {
				fmt.Fprint(w, "not a torrent")
			}
		}))
		_, _, _, err := NewNicePTKeySearcher(srv.Client(), srv.URL, "fake").Download(context.Background(), "nicept:12")
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "fake-secret") {
			t.Fatalf("unsafe/missing error: %v", err)
		}
	}
}
