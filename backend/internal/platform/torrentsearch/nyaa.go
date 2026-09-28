// Package torrentsearch contains resource-site adapters used by subscription downloads.
package torrentsearch

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const nyaaOrigin = "https://sukebei.nyaa.si"

var infoHashPattern = regexp.MustCompile("(?i)^[0-9a-f]{40}$")
var chineseMarkers = []string{"中文", "字幕", "中字", "CHS", "CHT", "CNSUB"}

// Resource is one normalized torrent candidate. URI is a magnet for public BT resources.
type Resource struct {
	Kind     string
	Site     string
	Title    string
	URI      string
	InfoHash string
	SizeMB   float64
	Seeders  int
	Chinese  bool
	UHD      bool
	UC       bool
	Free     bool
}

// NyaaSearcher reads Sukebei's RSS and only returns torrents whose title contains the requested code.
type NyaaSearcher struct {
	client *http.Client
	origin string
}

// NewNyaaSearcher constructs a bounded RSS client. A custom origin is used by controlled tests.
func NewNyaaSearcher(client *http.Client, origin string) *NyaaSearcher {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if origin == "" {
		origin = nyaaOrigin
	}
	return &NyaaSearcher{client: client, origin: strings.TrimRight(origin, "/")}
}

type nyaaFeed struct {
	Items []struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		Hash    string `xml:"infoHash"`
		Size    string `xml:"size"`
		Seeders int    `xml:"seeders"`
	} `xml:"channel>item"`
}

// Search reads one RSS response with a fixed size limit and validates every returned candidate.
func (s *NyaaSearcher) Search(ctx context.Context, code string) ([]Resource, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 128 {
		return nil, fmt.Errorf("invalid media code")
	}
	query := url.Values{"page": {"rss"}, "q": {code}}
	endpoint := s.origin + "/?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ByteMuse/1.0")
	client := *s.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return fmt.Errorf("RSS redirect rejected") }
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Nyaa RSS unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Nyaa RSS HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 2<<20 {
		return nil, fmt.Errorf("Nyaa RSS too large")
	}
	var feed nyaaFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse Nyaa RSS: %w", err)
	}
	out := make([]Resource, 0, len(feed.Items))
	for _, item := range feed.Items {
		if !codeInTitle(item.Title, code) || !infoHashPattern.MatchString(item.Hash) {
			continue
		}
		link, e := url.Parse(item.Link)
		if e != nil || link.Scheme != "https" || link.Host != "sukebei.nyaa.si" || !strings.HasPrefix(link.Path, "/download/") || !strings.HasSuffix(link.Path, ".torrent") {
			continue
		}
		title := strings.TrimSpace(item.Title)
		hash := strings.ToLower(item.Hash)
		text := strings.ToUpper(title)
		chinese := false
		for _, marker := range chineseMarkers {
			if strings.Contains(text, strings.ToUpper(marker)) {
				chinese = true
				break
			}
		}
		out = append(out, Resource{Kind: "bt", Site: "Nyaa BT", Title: title, URI: "magnet:?xt=urn:btih:" + hash + "&dn=" + url.QueryEscape(title), InfoHash: hash, SizeMB: parseSizeMB(item.Size), Seeders: item.Seeders, Chinese: chinese, UHD: strings.Contains(text, "4K") || strings.Contains(text, "2160P"), UC: strings.Contains(text, "UNCENSORED")})
	}
	return out, nil
}

func codeInTitle(title, code string) bool {
	title = strings.ToUpper(title)
	code = strings.ToUpper(code)
	for start := 0; start < len(title); {
		found := strings.Index(title[start:], code)
		if found < 0 {
			return false
		}
		idx := start + found
		end := idx + len(code)
		if (idx == 0 || !isCodeChar(title[idx-1])) && (end == len(title) || !isCodeChar(title[end])) {
			return true
		}
		start = idx + 1
	}
	return false
}

func isCodeChar(b byte) bool { return b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' }

func parseSizeMB(raw string) float64 {
	fields := strings.Fields(raw)
	if len(fields) != 2 {
		return 0
	}
	n, e := strconv.ParseFloat(fields[0], 64)
	if e != nil || n < 0 {
		return 0
	}
	switch fields[1] {
	case "KiB":
		return n / 1024
	case "MiB":
		return n
	case "GiB":
		return n * 1024
	case "TiB":
		return n * 1024 * 1024
	default:
		return 0
	}
}
