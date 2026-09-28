package torrentsearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// NexusCookieSearcher 使用已登录 Cookie 搜索完整网页结果，不依赖 RSS 或 PassKey。
type NexusCookieSearcher struct {
	client                             *http.Client
	origin, cookie, site, prefix, path string
	sizeColumn                         int
}

// NewPTTimeSearcher 搜索 PTTime 的 9KG 分区，站点名称沿用排序配置 PTT。
func NewPTTimeSearcher(client *http.Client, origin, cookie string) *NexusCookieSearcher {
	if origin == "" {
		origin = "https://www.pttime.org"
	}
	return newNexusCookieSearcher(client, origin, cookie, "PTT", "pttime", "/adults.php", 5)
}

// NewNicePTSearcher 搜索 NicePT 网页列表并使用 Cookie 取种。
func NewNicePTSearcher(client *http.Client, origin, cookie string) *NexusCookieSearcher {
	if origin == "" {
		origin = "https://www.nicept.net"
	}
	return newNexusCookieSearcher(client, origin, cookie, "NicePT", "nicept", "/torrents.php", 4)
}

func newNexusCookieSearcher(client *http.Client, origin, cookie, site, prefix, path string, sizeColumn int) *NexusCookieSearcher {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	clone.Jar = nil
	if clone.Timeout == 0 {
		clone.Timeout = 20 * time.Second
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &NexusCookieSearcher{&clone, strings.TrimRight(origin, "/"), strings.TrimSpace(cookie), site, prefix, path, sizeColumn}
}

func (s *NexusCookieSearcher) request(ctx context.Context, path string, limit int64) ([]byte, error) {
	if s.cookie == "" {
		return nil, fmt.Errorf("%s Cookie 未配置", s.site)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.origin+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s 请求无效", s.site)
	}
	req.Header.Set("Cookie", s.cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 ByteMuse")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s 请求失败", s.site)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("%s redirect rejected", s.site)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s HTTP %d，请检查 Cookie 或站点连接", s.site, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("%s 响应读取失败或过大", s.site)
	}
	return data, nil
}

// Search 依据页面分页导航读取全部结果；异常页、循环页或超限均明确失败。
func (s *NexusCookieSearcher) Search(ctx context.Context, code string) ([]Resource, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 128 {
		return nil, fmt.Errorf("invalid media code")
	}
	results := make([]Resource, 0)
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		q := url.Values{"search": {code}, "page": {strconv.Itoa(page)}, "incldead": {"0"}, "search_area": {"0"}, "search_mode": {"0"}}
		if s.prefix == "pttime" {
			q.Set("search_area", "1")
		}
		body, err := s.request(ctx, s.path+"?"+q.Encode(), 4<<20)
		if err != nil {
			return nil, err
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
		if err != nil {
			return nil, fmt.Errorf("%s 搜索页面无效", s.site)
		}
		if doc.Find("input[type=password],#login-form,form[action*='login']").Length() > 0 {
			return nil, fmt.Errorf("%s Cookie 已失效", s.site)
		}
		tables := doc.Find("table.torrentname")
		if tables.Length() == 0 {
			text := doc.Text()
			knownEmpty := strings.Contains(text, "结果为0") || strings.Contains(text, "沒有種子") || strings.Contains(text, "没有种子")
			if page == 0 && knownEmpty && doc.Find("input[name=search]").Length() > 0 {
				return results, nil
			}
			return nil, fmt.Errorf("%s 未返回有效种子列表", s.site)
		}
		fresh := 0
		invalid := false
		tables.Each(func(_ int, table *goquery.Selection) {
			row := table.Parent().Parent()
			detail := table.Find("a[href^='details.php?id=']").First()
			href, _ := detail.Attr("href")
			u, e := url.Parse(href)
			if e != nil || !ptfansID.MatchString(u.Query().Get("id")) {
				invalid = true
				return
			}
			id := u.Query().Get("id")
			if seen[id] {
				return
			}
			seen[id] = true
			fresh++
			title := strings.TrimSpace(detail.AttrOr("title", detail.Text()))
			if !codeInTitle(title+" "+table.Text(), code) {
				return
			}
			cells := row.ChildrenFiltered("td")
			if cells.Length() <= s.sizeColumn+1 {
				invalid = true
				return
			}
			sizeText := strings.TrimSpace(cells.Eq(s.sizeColumn).Text())
			for _, unit := range []string{"TB", "GB", "MB", "KB"} {
				sizeText = strings.ReplaceAll(sizeText, unit, " "+unit)
			}
			sizeText = strings.Join(strings.Fields(sizeText), " ")
			sizeText = strings.NewReplacer(" TB", " TiB", " GB", " GiB", " MB", " MiB", " KB", " KiB").Replace(sizeText)
			size := parseSizeMB(sizeText)
			seeders, e := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(cells.Eq(s.sizeColumn+1).Text()), ",", ""))
			if e != nil || seeders < 0 || size <= 0 {
				invalid = true
				return
			}
			upper := strings.ToUpper(title + " " + table.Text())
			chinese := false
			for _, marker := range chineseMarkers {
				if strings.Contains(upper, strings.ToUpper(marker)) {
					chinese = true
					break
				}
			}
			free := table.Find("img.pro_free,img.pro_free2up,img[alt='Free'],img[alt='2X Free'],.promotion.free,.promotion.twoupfree").Length() > 0
			results = append(results, Resource{Kind: "pt", Site: s.site, Title: title, URI: s.prefix + ":" + id, SizeMB: size, Seeders: seeders, Chinese: chinese, UHD: strings.Contains(upper, "4K") || strings.Contains(upper, "2160P"), UC: strings.Contains(upper, "UNCENSORED") || strings.Contains(upper, "无码"), Free: free})
		})
		if invalid || fresh == 0 {
			return nil, fmt.Errorf("%s 种子列表结构无效或分页重复", s.site)
		}
		hasNext := false
		badPage := false
		doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			u, e := url.Parse(href)
			if e != nil || u.IsAbs() || u.Host != "" || (u.Path != "" && u.Path != s.path && u.Path != strings.TrimPrefix(s.path, "/")) {
				return
			}
			raw := u.Query().Get("page")
			if raw == "" {
				return
			}
			n, e := strconv.Atoi(raw)
			if e != nil {
				return
			}
			if n > page {
				hasNext = true
			}
			if strings.Contains(a.Text(), "下一") && n <= page {
				badPage = true
			}
		})
		if badPage {
			return nil, fmt.Errorf("%s 搜索分页导航无效", s.site)
		}
		if !hasNext {
			return results, nil
		}
	}
	return nil, fmt.Errorf("%s 搜索超过安全分页上限", s.site)
}

// Download 只接收站点限定的数字 ID，不转发页面里的秘密下载 URL。
func (s *NexusCookieSearcher) Download(ctx context.Context, reference string) ([]byte, string, error) {
	id := strings.TrimPrefix(reference, s.prefix+":")
	if id == reference || !ptfansID.MatchString(id) || strings.Trim(id, "0") == "" {
		return nil, "", fmt.Errorf("invalid %s resource", s.site)
	}
	data, err := s.request(ctx, "/download.php?id="+id, 4<<20)
	if err != nil {
		return nil, "", err
	}
	hash, err := TorrentInfoHash(data)
	if err != nil {
		return nil, "", fmt.Errorf("%s 返回无效种子文件", s.site)
	}
	return data, hash, nil
}
