package torrentsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const rousiproOrigin = "https://rousi.pro"

var rousiproID = regexp.MustCompile("^[0-9]+$")

// RousiProSearcher searches the authenticated 9KG catalog and retrieves private torrent bytes.
type RousiProSearcher struct {
	client         *http.Client
	origin, cookie string
	key            string
}

// NewRousiProSearcher creates a bounded site client; an origin override is only for controlled tests.
func NewRousiProSearcher(client *http.Client, origin, cookie string) *RousiProSearcher {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if origin == "" {
		origin = rousiproOrigin
	}
	clone := *client
	clone.Jar = nil
	if clone.Timeout == 0 {
		clone.Timeout = 20 * time.Second
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return fmt.Errorf("RousiPro redirect rejected") }
	return &RousiProSearcher{client: &clone, origin: strings.TrimRight(origin, "/"), cookie: cookie}
}

// NewRousiProKeySearcher 使用个人 API Key 的兼容 API，不使用浏览器 Cookie。
func NewRousiProKeySearcher(client *http.Client, origin, key string) *RousiProSearcher {
	s := NewRousiProSearcher(client, origin, "")
	s.key = strings.TrimSpace(key)
	return s
}

// keyAPI 按站点兼容协议检查业务状态，不输出可能含秘密的响应内容。
func (s *RousiProSearcher) keyAPI(ctx context.Context, path string, target any) error {
	client := newNexusKeySearcher(s.client, s.origin, s.key, "RousiPro", "rousipro", "")
	body, err := client.request(ctx, s.origin+path, true, 4<<20)
	if err != nil {
		return err
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("RousiPro 密钥 API 请求失败，请检查 Key 及搜索/下载权限")
	}
	if json.Unmarshal(envelope.Data, target) != nil {
		return fmt.Errorf("RousiPro 密钥 API 数据无效")
	}
	return nil
}

func (s *RousiProSearcher) searchKey(ctx context.Context, code string) ([]Resource, error) {
	results := make([]Resource, 0)
	seen := map[int64]bool{}
	for page := 1; page <= 100; page++ {
		q := url.Values{"keyword": {code}, "category": {"9kg"}, "page": {strconv.Itoa(page)}, "page_size": {"100"}}
		var response struct {
			Page     int `json:"page"`
			Total    int `json:"total"`
			Pages    int `json:"total_pages"`
			Torrents *[]struct {
				ID        int64  `json:"id"`
				Title     string `json:"title"`
				Subtitle  string `json:"subtitle"`
				Category  string `json:"category"`
				Size      int64  `json:"size"`
				Seeders   int    `json:"seeders"`
				Promotion struct {
					Active bool     `json:"is_active"`
					Down   *float64 `json:"down_multiplier"`
				} `json:"promotion"`
			} `json:"torrents"`
		}
		if err := s.keyAPI(ctx, "/api/v1/torrents?"+q.Encode(), &response); err != nil {
			return nil, err
		}
		if response.Torrents == nil || response.Page != page || response.Total < 0 || response.Pages < 0 {
			return nil, fmt.Errorf("RousiPro 密钥搜索分页无效")
		}
		fresh := 0
		for _, item := range *response.Torrents {
			if item.ID <= 0 || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			fresh++
			if item.Category != "9kg" || item.Size <= 0 || !codeInTitle(item.Title+" "+item.Subtitle, code) {
				continue
			}
			upper := strings.ToUpper(item.Title + " " + item.Subtitle)
			chinese := false
			for _, marker := range chineseMarkers {
				if strings.Contains(upper, strings.ToUpper(marker)) {
					chinese = true
					break
				}
			}
			results = append(results, Resource{Kind: "pt", Site: "RousiPro", Title: item.Title, URI: "rousipro:" + strconv.FormatInt(item.ID, 10), SizeMB: float64(item.Size) / (1024 * 1024), Seeders: item.Seeders, Chinese: chinese, UHD: strings.Contains(upper, "4K") || strings.Contains(upper, "2160P"), UC: strings.Contains(upper, "UNCENSORED") || strings.Contains(upper, "无码"), Free: item.Promotion.Active && item.Promotion.Down != nil && *item.Promotion.Down == 0})
		}
		if page == 1 && response.Total == 0 && len(*response.Torrents) == 0 {
			return results, nil
		}
		if response.Pages < page || fresh == 0 {
			return nil, fmt.Errorf("RousiPro 密钥搜索分页为空或重复")
		}
		if page == response.Pages {
			return results, nil
		}
	}
	return nil, fmt.Errorf("RousiPro 密钥搜索超过安全分页上限")
}

func (s *RousiProSearcher) downloadKey(ctx context.Context, id string) ([]byte, string, error) {
	var detail struct {
		URL string `json:"download_url"`
	}
	if err := s.keyAPI(ctx, "/api/v1/torrents/"+id, &detail); err != nil {
		return nil, "", err
	}
	base, _ := url.Parse(s.origin)
	relative, err := url.Parse(detail.URL)
	if err != nil || base == nil || detail.URL == "" {
		return nil, "", fmt.Errorf("RousiPro 未返回下载能力链接")
	}
	target := base.ResolveReference(relative)
	if target.Scheme != base.Scheme || target.Host != base.Host || target.User != nil || target.Fragment != "" || target.Path != "/api/compat/moviepilot/v1/torrents/"+id+"/download" || target.Query().Get("capability") == "" {
		return nil, "", fmt.Errorf("RousiPro 拒绝无效或非本站下载地址")
	}
	client := newNexusKeySearcher(s.client, s.origin, s.key, "RousiPro", "rousipro", "")
	data, err := client.request(ctx, target.String(), false, 4<<20)
	if err != nil {
		return nil, "", err
	}
	hash, err := TorrentInfoHash(data)
	if err != nil {
		return nil, "", fmt.Errorf("RousiPro 返回无效种子文件")
	}
	return data, hash, nil
}

func (s *RousiProSearcher) request(ctx context.Context, path string, maxBytes int64) ([]byte, string, error) {
	if strings.TrimSpace(s.cookie) == "" {
		return nil, "", fmt.Errorf("RousiPro cookie is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.origin+path, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Cookie", s.cookie)
	req.Header.Set("Accept", "application/json, application/x-bittorrent")
	req.Header.Set("User-Agent", "Mozilla/5.0 ByteMuse")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("RousiPro request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, "", fmt.Errorf("RousiPro login required (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("RousiPro HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, "", fmt.Errorf("RousiPro response too large")
	}
	return body, resp.Header.Get("Content-Type"), nil
}

type rousiproResults struct {
	Items []struct {
		Category struct {
			ID string `json:"id"`
		} `json:"category"`
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Subtitle  string `json:"subtitle"`
		Seeders   int    `json:"seeders"`
		SizeBytes int64  `json:"size_bytes"`
		Promotion string `json:"promotion"`
	} `json:"items"`
	Total int `json:"total"`
}

// Search queries the 9KG catalog and keeps only exact code-token matches from authenticated JSON results.
func (s *RousiProSearcher) Search(ctx context.Context, code string) ([]Resource, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 128 {
		return nil, fmt.Errorf("invalid media code")
	}
	if s.key != "" {
		return s.searchKey(ctx, code)
	}
	const pageSize, maxPages = 25, 20
	results := make([]Resource, 0)
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		offset := pageNumber * pageSize
		query := url.Values{
			"limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}, "query": {code},
			"search_scope": {"title_subtitle"}, "category_id": {"9kg"}, "sort": {"published_desc"},
		}
		body, contentType, err := s.request(ctx, "/api/v1/torrents?"+query.Encode(), 2<<20)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(strings.ToLower(contentType), "application/json") {
			return nil, fmt.Errorf("RousiPro search did not return JSON; login may be required")
		}
		var page rousiproResults
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse RousiPro search: %w", err)
		}
		for _, item := range page.Items {
			if item.ID <= 0 || item.Category.ID != "9kg" || !codeInTitle(item.Name+" "+item.Subtitle, code) || item.SizeBytes <= 0 {
				continue
			}
			upper := strings.ToUpper(item.Name + " " + item.Subtitle)
			chinese := false
			for _, marker := range chineseMarkers {
				if strings.Contains(upper, strings.ToUpper(marker)) {
					chinese = true
					break
				}
			}
			results = append(results, Resource{
				Kind: "pt", Site: "RousiPro", Title: strings.TrimSpace(item.Name),
				URI:    "rousipro:" + strconv.FormatInt(item.ID, 10),
				SizeMB: float64(item.SizeBytes) / (1024 * 1024), Seeders: item.Seeders,
				Chinese: chinese, UHD: strings.Contains(upper, "4K") || strings.Contains(upper, "2160P"),
				UC:   strings.Contains(upper, "UNCENSORED") || strings.Contains(item.Name+item.Subtitle, "无码"),
				Free: item.Promotion == "free" || item.Promotion == "2xfree",
			})
		}
		if offset+pageSize >= page.Total {
			return results, nil
		}
		if len(page.Items) == 0 {
			return nil, fmt.Errorf("RousiPro search returned an empty page before total")
		}
	}
	return nil, fmt.Errorf("RousiPro search exceeds %d pages", maxPages)
}

// Download accepts only a source-qualified numeric ID and validates downloaded torrent metainfo.
func (s *RousiProSearcher) Download(ctx context.Context, reference string) ([]byte, string, error) {
	id := strings.TrimPrefix(reference, "rousipro:")
	if id == reference || !rousiproID.MatchString(id) || strings.Trim(id, "0") == "" {
		return nil, "", fmt.Errorf("invalid RousiPro resource")
	}
	if s.key != "" {
		return s.downloadKey(ctx, id)
	}
	data, contentType, err := s.request(ctx, "/api/v1/torrents/"+id+"/download", 4<<20)
	if err != nil {
		return nil, "", err
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") || strings.Contains(strings.ToLower(contentType), "application/json") {
		return nil, "", fmt.Errorf("RousiPro torrent endpoint returned %s", contentType)
	}
	hash, err := TorrentInfoHash(data)
	if err != nil {
		return nil, "", fmt.Errorf("invalid RousiPro torrent: %w", err)
	}
	return data, hash, nil
}
