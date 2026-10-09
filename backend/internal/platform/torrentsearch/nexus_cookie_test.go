package torrentsearch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCookieSitesPaginateAndDownload(t *testing.T) {
	for _, site := range []string{"pttime", "nicept", "ptfans"} {
		t.Run(site, func(t *testing.T) {
			path := "/torrents.php"
			if site == "ptfans" {
				path = "/special.php"
			}
			padding := ""
			if site == "pttime" {
				path = "/adults.php"
				padding = "<td>0</td>"
			}
			searches := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "session=fake" || r.Header.Get("Authorization") != "" {
					t.Error("wrong Cookie mode")
				}
				switch r.URL.Path {
				case path:
					if r.URL.Query().Get("incldead") != "0" {
						t.Error("search excluded dead torrents")
					}
					searches++
					if r.URL.Query().Get("search") != "TEST-123" {
						t.Error("wrong keyword")
					}
					id := "12"
					if r.URL.Query().Get("page") == "1" {
						id = "13"
					}
					fmt.Fprintf(w, `<form><input name="search"></form><table class="torrents"><tr><td>cat</td><td><table class="torrentname"><tr><td><a href="details.php?id=%s" title="TEST-123 中文字幕">TEST-123 中文字幕</a><font class="promotion free">免费</font><a href="download.php?id=%s&amp;passkey=secret">下载</a></td></tr></table></td><td>0</td>%s<td>today</td><td>2<br>GB</td><td><a href="details.php?id=%s#seeders">8</a></td></tr></table>`, id, id, padding, id)
					if id == "12" {
						fmt.Fprint(w, `<a href="?search=TEST-123&amp;page=1">下一页 &gt;&gt;</a>`)
					}
				case "/download.php":
					if r.URL.Query().Get("passkey") != "" {
						t.Error("secret URL reused")
					}
					fmt.Fprint(w, "d8:announce14:http://tracker4:infod4:name4:testee")
				default:
					t.Errorf("path=%s", r.URL.Path)
				}
			}))
			defer srv.Close()
			var adapter interface {
				Search(context.Context, string) ([]Resource, error)
				Download(context.Context, string) ([]byte, string, string, error)
			} = NewNicePTSearcher(srv.Client(), srv.URL, "session=fake")
			if site == "ptfans" {
				adapter = NewPTFansSearcher(srv.Client(), srv.URL, "session=fake")
			}
			if site == "pttime" {
				adapter = NewPTTimeSearcher(srv.Client(), srv.URL, "session=fake")
			}
			items, err := adapter.Search(context.Background(), "TEST-123")
			if err != nil || len(items) != 2 || searches != 2 {
				t.Fatalf("items=%v searches=%d err=%v", items, searches, err)
			}
			if items[0].URI != site+":12" || !items[0].Free || !items[0].Chinese || items[0].SizeMB != 2048 || items[0].Seeders != 8 {
				t.Fatalf("resource=%+v", items[0])
			}
			_, hash, downloadURL, err := adapter.Download(context.Background(), items[0].URI)
			if downloadURL != srv.URL+"/download.php?id=12" {
				t.Fatal("actual URL missing")
			}
			if err != nil || hash == "" {
				t.Fatalf("download %v", err)
			}
		})
	}
}

func TestCookieSitesRejectBadPages(t *testing.T) {
	for _, body := range []string{`<html>Cloudflare challenge</html>`, `<form action="takelogin.php"><input type="password"></form>`, `<table class="torrents"><tr><td>broken table</td></tr></table>`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		_, err := NewPTTimeSearcher(srv.Client(), srv.URL, "session=fake").Search(context.Background(), "TEST-123")
		srv.Close()
		if err == nil {
			t.Fatal("bad page treated as empty")
		}
	}
}

func TestCookieSitesKnownEmpty(t *testing.T) {
	for _, body := range []string{`<form><input name="search"></form>结果为0，请更换关键词或更换分类。`, `<form><input name="search"></form>沒有種子。請用準確的關鍵字重試。`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		items, err := NewPTTimeSearcher(srv.Client(), srv.URL, "session=fake").Search(context.Background(), "TEST-123")
		srv.Close()
		if err != nil || len(items) != 0 {
			t.Fatalf("empty err=%v", err)
		}
	}
}
