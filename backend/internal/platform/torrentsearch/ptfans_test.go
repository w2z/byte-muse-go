package torrentsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPTFansSearchAndDownload(t *testing.T) {
	torrent := []byte("d8:announce14:http://tracker4:infod4:name4:testee")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "session=demo" {
			t.Errorf("cookie missing")
		}
		switch r.URL.Path {
		case "/special.php":
			if r.URL.Query().Get("search") != "FNS-249" {
				t.Errorf("search query=%q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`<table><tr><th>类型</th></tr><tr><td>9KG</td><td><table class="torrentname"><tr><td><a href="details.php?id=9432&amp;hit=1">FNS-249 中文字幕 4K</a><img class="pro_free" alt="Free"><a href="download.php?id=9432">下载</a></td></tr></table></td><td>0</td><td>today</td><td>4.88<br>GB</td><td>88</td></tr><tr><td>9KG</td><td><table class="torrentname"><tr><td><a href="details.php?id=9433">FNS-2491 wrong</a><a href="download.php?id=9433">下载</a></td></tr></table></td><td>0</td><td>today</td><td>1 GB</td><td>3</td></tr></table>`))
		case "/download.php":
			if r.URL.Query().Get("id") != "9432" {
				t.Errorf("download id=%q", r.URL.Query().Get("id"))
			}
			_, _ = w.Write(torrent)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	site := NewPTFansSearcher(server.Client(), server.URL, "session=demo")
	items, err := site.Search(context.Background(), "FNS-249")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].URI != "ptfans:9432" || items[0].Seeders != 88 || !items[0].Free || !items[0].Chinese || !items[0].UHD || items[0].SizeMB < 4900 {
		t.Fatalf("items=%#v", items)
	}
	got, hash, err := site.Download(context.Background(), items[0].URI)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(torrent) || hash != "1ade8a1a581f338e4fce4ce784da3f7d03f81f3a" {
		t.Fatalf("hash=%s", hash)
	}
}

func TestPTFansRejectsLoginAndForeignResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/special.php" {
			http.Redirect(w, r, "/login.php", http.StatusFound)
		}
	}))
	defer server.Close()
	site := NewPTFansSearcher(server.Client(), server.URL, "session=demo")
	if _, err := site.Search(context.Background(), "FNS-249"); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("login err=%v", err)
	}
	if _, _, err := site.Download(context.Background(), "mteam:9432"); err == nil {
		t.Fatal("foreign reference accepted")
	}
}

func TestPTFansDiscountIsNotFree(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<table><tr><td>9KG</td><td><table class="torrentname"><tr><td><a href="details.php?id=9432&amp;hit=1">FNS-249</a><img alt="50%"><a href="download.php?id=9432">download</a></td></tr></table></td><td>0</td><td>today</td><td>4.88 GB</td><td>88</td></tr></table>`))
	}))
	defer server.Close()
	items, err := NewPTFansSearcher(server.Client(), server.URL, "session=demo").Search(context.Background(), "FNS-249")
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if items[0].Free {
		t.Fatal("50% discount must not be treated as free")
	}
}
