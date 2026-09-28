package torrentsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNyaaSearchParsesMatchingRSSAndRejectsOtherCodes(t *testing.T) {
	feed := `<?xml version="1.0"?><rss xmlns:nyaa="https://sukebei.nyaa.si/xmlns/nyaa"><channel><item><title>[中文字幕] SSIS-001 1080p</title><link>https://sukebei.nyaa.si/download/123.torrent</link><guid>https://sukebei.nyaa.si/view/123</guid><nyaa:seeders>12</nyaa:seeders><nyaa:infoHash>0123456789abcdef0123456789abcdef01234567</nyaa:infoHash><nyaa:size>2.0 GiB</nyaa:size></item><item><title>SSIS-0011 false positive</title><link>https://sukebei.nyaa.si/download/124.torrent</link><nyaa:infoHash>1111111111111111111111111111111111111111</nyaa:infoHash></item></channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "rss" || r.URL.Query().Get("q") != "SSIS-001" {
			t.Errorf("wrong query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()
	got, err := NewNyaaSearcher(server.Client(), server.URL).Search(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seeders != 12 || got[0].SizeMB != 2048 || !got[0].Chinese || got[0].Kind != "bt" || got[0].InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("resources: %#v", got)
	}
	if !strings.HasPrefix(got[0].URI, "magnet:?xt=urn:btih:") {
		t.Fatalf("not a magnet: %q", got[0].URI)
	}
}

func TestNyaaSearchRejectsUntrustedFeedLinks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss xmlns:nyaa="https://sukebei.nyaa.si/xmlns/nyaa"><channel><item><title>SSIS-001</title><link>https://evil.invalid/a.torrent</link><nyaa:infoHash>0123456789abcdef0123456789abcdef01234567</nyaa:infoHash></item></channel></rss>`))
	}))
	defer server.Close()
	got, err := NewNyaaSearcher(server.Client(), server.URL).Search(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("accepted untrusted link: %#v", got)
	}
}
