package torrentsearch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRousiProKeySearchDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("key leaked cookie")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/torrents":
			if r.Header.Get("Authorization") != "Bearer fake-key" || r.URL.Query().Get("keyword") != "TEST-123" || r.URL.Query().Get("category") != "9kg" {
				t.Error("wrong key query")
			}
			fmt.Fprint(w, `{"code":0,"data":{"page":1,"page_size":100,"total":1,"total_pages":1,"torrents":[{"id":12,"category":"9kg","title":"TEST-123","size":1048576,"seeders":3,"promotion":{"is_active":true,"down_multiplier":0}}]}}`)
		case "/api/v1/torrents/12":
			fmt.Fprint(w, `{"code":0,"data":{"download_url":"/api/compat/moviepilot/v1/torrents/12/download?capability=fake-secret"}}`)
		case "/api/compat/moviepilot/v1/torrents/12/download":
			if r.Header.Get("Authorization") != "" || r.URL.Query().Get("capability") != "fake-secret" {
				t.Error("wrong download auth")
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			fmt.Fprint(w, "d8:announce14:http://tracker4:infod4:name4:testee")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	s := NewRousiProKeySearcher(srv.Client(), srv.URL, "fake-key")
	items, err := s.Search(context.Background(), "TEST-123")
	if err != nil || len(items) != 1 || !items[0].Free || items[0].URI != "rousipro:12" {
		t.Fatalf("items=%v err=%v", items, err)
	}
	_, hash, downloadURL, err := s.Download(context.Background(), items[0].URI)
	if downloadURL != srv.URL+"/api/compat/moviepilot/v1/torrents/12/download?capability=fake-secret" {
		t.Fatal("actual URL missing")
	}
	if err != nil || hash != "1ade8a1a581f338e4fce4ce784da3f7d03f81f3a" {
		t.Fatalf("hash=%s err=%v", hash, err)
	}
}

func TestRousiProKeyLiveOnlyFetch(t *testing.T) {
	key := os.Getenv("BYTEMUSE_TEST_ROUSIPRO_KEY")
	if key == "" {
		t.Skip("未提供临时Key")
	}
	s := NewRousiProKeySearcher(nil, "", key)
	items, err := s.Search(context.Background(), "DMX-0031")
	if err != nil || len(items) == 0 {
		t.Fatalf("matches=%d err=%v", len(items), err)
	}
	data, hash, _, err := s.Download(context.Background(), items[0].URI)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("matches=%d bytes=%d infohash=%s sha256=%x", len(items), len(data), hash, sha256.Sum256(data))
}

func TestRousiProKeyRejectsFailuresAndUnsafeURLs(t *testing.T) {
	for _, body := range []string{`{"code":1,"message":"fake-secret"}`, `{}`, `{"code":0,"data":{"download_url":"https://evil.invalid/?capability=fake-secret"}}`, `{"code":0,"data":{"download_url":"/api/compat/moviepilot/v1/torrents/13/download?capability=fake-secret"}}`} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, body) }))
		_, _, _, err := NewRousiProKeySearcher(srv.Client(), srv.URL, "fake-key").Download(context.Background(), "rousipro:12")
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "fake-secret") || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}

func TestRousiProKeyPagination(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		page := r.URL.Query().Get("page")
		fmt.Fprintf(w, `{"code":0,"data":{"page":%s,"page_size":100,"total":101,"total_pages":2,"torrents":[{"id":%s,"title":"TEST-123","category":"9kg","size":1048576,"seeders":1}]}}`, page, page)
	}))
	defer srv.Close()
	items, err := NewRousiProKeySearcher(srv.Client(), srv.URL, "fake-key").Search(context.Background(), "TEST-123")
	if err != nil || len(items) != 2 || calls != 2 {
		t.Fatalf("items=%v calls=%d err=%v", items, calls, err)
	}
}

func TestRousiProSearchAndDownload(t *testing.T) {
	torrent := []byte("d8:announce14:http://tracker4:infod4:name4:testee")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "session=demo" {
			t.Errorf("cookie missing")
		}
		switch r.URL.Path {
		case "/api/v1/torrents":
			q := r.URL.Query()
			if q.Get("query") != "AVJI-123" || q.Get("category_id") != "9kg" || q.Get("search_scope") != "title_subtitle" || q.Get("limit") != "25" || q.Get("offset") != "0" {
				t.Errorf("search query=%q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"items\":[{\"category\":{\"id\":\"9kg\",\"name\":\"9KG\"},\"id\":4411,\"name\":\"[AV Jiali]_AVJI-123\",\"subtitle\":\"\",\"seeders\":7,\"size_bytes\":3904747161,\"promotion\":\"free\"},{\"category\":{\"id\":\"movie\"},\"id\":55,\"name\":\"AVJI-123 wrong category\",\"seeders\":1,\"size_bytes\":1000},{\"category\":{\"id\":\"9kg\"},\"id\":56,\"name\":\"AVJI-1234\",\"seeders\":1,\"size_bytes\":1000}],\"limit\":25,\"offset\":0,\"total\":3}"))
		case "/api/v1/torrents/4411/download":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(torrent)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	site := NewRousiProSearcher(server.Client(), server.URL, "session=demo")
	items, err := site.Search(context.Background(), "AVJI-123")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Site != "RousiPro" || items[0].URI != "rousipro:4411" || items[0].Seeders != 7 || items[0].SizeMB < 3700 || !items[0].Free {
		t.Fatalf("items=%#v", items)
	}
	got, hash, _, err := site.Download(context.Background(), items[0].URI)
	if err != nil || string(got) != string(torrent) || hash != "1ade8a1a581f338e4fce4ce784da3f7d03f81f3a" {
		t.Fatalf("download err=%v hash=%s", err, hash)
	}
}

func TestRousiProRejectsLoginAndForeignReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/torrents" {
			http.Redirect(w, r, "/login", http.StatusFound)
		}
	}))
	defer server.Close()
	site := NewRousiProSearcher(server.Client(), server.URL, "session=demo")
	if _, err := site.Search(context.Background(), "AVJI-123"); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("login err=%v", err)
	}
	if _, _, _, err := site.Download(context.Background(), "ptfans:4411"); err == nil {
		t.Fatal("foreign reference accepted")
	}
}

func TestRousiProRejectsInvalidTorrentAndEmptyCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>login</html>"))
	}))
	defer server.Close()
	if _, err := NewRousiProSearcher(server.Client(), server.URL, "").Search(context.Background(), "AVJI-123"); err == nil {
		t.Fatal("empty cookie accepted")
	}
	if _, _, _, err := NewRousiProSearcher(server.Client(), server.URL, "session=demo").Download(context.Background(), "rousipro:4411"); err == nil {
		t.Fatal("html accepted as torrent")
	}
}

func TestRousiProSearchContinuesAfterFirstPage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("offset") {
		case "0":
			_, _ = w.Write([]byte("{\"items\":[{\"category\":{\"id\":\"9kg\"},\"id\":1,\"name\":\"AVJI-123\",\"size_bytes\":1024}],\"limit\":25,\"offset\":0,\"total\":26}"))
		case "25":
			_, _ = w.Write([]byte("{\"items\":[{\"category\":{\"id\":\"9kg\"},\"id\":26,\"name\":\"AVJI-123\",\"size_bytes\":2048}],\"limit\":25,\"offset\":25,\"total\":26}"))
		default:
			t.Errorf("unexpected offset %q", r.URL.Query().Get("offset"))
		}
	}))
	defer server.Close()
	items, err := NewRousiProSearcher(server.Client(), server.URL, "session=demo").Search(context.Background(), "AVJI-123")
	if err != nil || len(items) != 2 || requests != 2 {
		t.Fatalf("items=%#v requests=%d err=%v", items, requests, err)
	}
}
