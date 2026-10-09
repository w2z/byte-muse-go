package torrentsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NexusKeySearcher 使用 NexusPHP 访问令牌进行全站分页搜索及个人种子下载。
type NexusKeySearcher struct {
	client                             *http.Client
	origin, key, site, prefix, section string
}

// NewPTFansKeySearcher 搜索 PTFans 的 cas（9KG）分区。
func NewPTFansKeySearcher(client *http.Client, origin, key string) *NexusKeySearcher {
	if origin == "" {
		origin = ptfansOrigin
	}
	return newNexusKeySearcher(client, origin, key, "PTFans", "ptfans", "/cas")
}

// NewNicePTKeySearcher 使用 NicePT 的普通种子列表 API。
func NewNicePTKeySearcher(client *http.Client, origin, key string) *NexusKeySearcher {
	if origin == "" {
		origin = "https://www.nicept.net"
	}
	return newNexusKeySearcher(client, origin, key, "NicePT", "nicept", "")
}

func newNexusKeySearcher(client *http.Client, origin, key, site, prefix, section string) *NexusKeySearcher {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	clone.Jar = nil
	if clone.Timeout == 0 {
		clone.Timeout = 20 * time.Second
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &NexusKeySearcher{&clone, strings.TrimRight(origin, "/"), strings.TrimSpace(key), site, prefix, section}
}

// request 不回传原始 URL、站点响应或底层传输错误，防止下载 PassKey 泄漏。
func (s *NexusKeySearcher) request(ctx context.Context, target string, api bool, limit int64) ([]byte, error) {
	if s.key == "" {
		return nil, fmt.Errorf("%s 访问令牌未配置", s.site)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("%s 请求地址无效", s.site)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 ByteMuse")
	if api {
		req.Header.Set("Authorization", "Bearer "+s.key)
		req.Header.Set("Accept", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s 请求失败，请检查连接或重试", s.site)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s HTTP %d", s.site, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("%s 响应读取失败或超出大小限制", s.site)
	}
	return data, nil
}

func (s *NexusKeySearcher) api(ctx context.Context, path string, target any) error {
	data, err := s.request(ctx, s.origin+path, true, 4<<20)
	if err != nil {
		return err
	}
	var envelope struct {
		Ret  *int            `json:"ret"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Ret == nil {
		return fmt.Errorf("%s API 响应格式无效", s.site)
	}
	if *envelope.Ret != 0 {
		return fmt.Errorf("%s API 拒绝请求，请检查令牌及种子列表/详情权限", s.site)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, target) != nil {
		return fmt.Errorf("%s API 数据格式无效", s.site)
	}
	return nil
}

type nexusTorrent struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Subtitle  string `json:"small_descr"`
	Size      int64  `json:"size"`
	Seeders   int    `json:"seeders"`
	Promotion struct {
		Down *float64 `json:"down_multiplier"`
	} `json:"promotion_info"`
}

// Search 遍历全部结果页；分页异常或超限返回错误，不伪装成完整结果。
func (s *NexusKeySearcher) Search(ctx context.Context, code string) ([]Resource, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 128 {
		return nil, fmt.Errorf("invalid media code")
	}
	results := make([]Resource, 0)
	seen := map[int64]bool{}
	for page := 1; page <= 100; page++ {
		q := url.Values{"filter[title]": {code}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var response struct {
			Data *[]nexusTorrent `json:"data"`
			Meta struct {
				Current int `json:"current_page"`
				Last    int `json:"last_page"`
			} `json:"meta"`
		}
		if err := s.api(ctx, "/api/v1/torrents"+s.section+"?"+q.Encode(), &response); err != nil {
			return nil, err
		}
		if response.Data == nil || response.Meta.Current != page || response.Meta.Last < page {
			return nil, fmt.Errorf("%s 搜索分页数据无效", s.site)
		}
		fresh := 0
		for _, item := range *response.Data {
			if item.ID <= 0 || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			fresh++
			if item.Size <= 0 || !codeInTitle(item.Name+" "+item.Subtitle, code) {
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
			results = append(results, Resource{Kind: "pt", Site: s.site, Title: item.Name, URI: s.prefix + ":" + strconv.FormatInt(item.ID, 10), SizeMB: float64(item.Size) / (1024 * 1024), Seeders: item.Seeders, Chinese: chinese, UHD: strings.Contains(upper, "4K") || strings.Contains(upper, "2160P"), UC: strings.Contains(upper, "UNCENSORED") || strings.Contains(upper, "无码"), Free: item.Promotion.Down != nil && *item.Promotion.Down == 0})
		}
		if fresh == 0 && !(page == 1 && response.Meta.Last == 1 && len(*response.Data) == 0) {
			return nil, fmt.Errorf("%s 搜索分页为空或重复", s.site)
		}
		if response.Meta.Last == page {
			return results, nil
		}
	}
	return nil, fmt.Errorf("%s 搜索超过安全分页上限", s.site)
}

// Download 即时获取个人下载地址，仅允许同源 download.php；返回 URL 供任务内部持久化。
func (s *NexusKeySearcher) Download(ctx context.Context, reference string) ([]byte, string, string, error) {
	id := strings.TrimPrefix(reference, s.prefix+":")
	if id == reference || !ptfansID.MatchString(id) || strings.Trim(id, "0") == "" {
		return nil, "", "", fmt.Errorf("invalid %s resource", s.site)
	}
	var detail struct {
		Data struct {
			URL string `json:"download_url"`
		} `json:"data"`
	}
	if err := s.api(ctx, "/api/v1/detail/"+id+"?include_fields%5Btorrent%5D=download_url", &detail); err != nil {
		return nil, "", "", err
	}
	base, _ := url.Parse(s.origin)
	relative, err := url.Parse(detail.Data.URL)
	if err != nil || base == nil || detail.Data.URL == "" {
		return nil, "", "", fmt.Errorf("%s 未返回有效下载地址", s.site)
	}
	target := base.ResolveReference(relative)
	if target.Scheme != base.Scheme || target.Host != base.Host || target.User != nil || target.Fragment != "" || target.Path != "/download.php" {
		return nil, "", "", fmt.Errorf("%s 拒绝非本站下载地址", s.site)
	}
	data, err := s.request(ctx, target.String(), false, 4<<20)
	if err != nil {
		return nil, "", "", err
	}
	hash, err := TorrentInfoHash(data)
	if err != nil {
		return nil, "", "", fmt.Errorf("%s 返回无效种子文件", s.site)
	}
	return data, hash, target.String(), nil
}
