package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"github.com/PuerkitoBio/goquery"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var supjavIDPattern = regexp.MustCompile(`^/[0-9]+.html$`)
var jableVideoPath = regexp.MustCompile(`^/videos/([A-Za-z0-9_-]+)/$`)
var jableNextPage = regexp.MustCompile(`(?:^|;)from:([0-9]+)`)

// collectJableSearch 从确认的搜索容器读取卡片；页码由源站分页参数判断。
func collectJableSearch(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	target := "https://jable.tv/search/"
	if req.Page > 1 {
		target += strconv.Itoa(req.Page) + "/"
	}
	target += "?" + url.Values{"q": {req.Query}}.Encode()
	body, e := f.Get(ctx, target)
	if e != nil {
		return ports.CollectionBatch{}, e
	}
	d, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	root := d.Find("#list_videos_videos_list_search_result")
	if root.Length() != 1 {
		return ports.CollectionBatch{}, ErrParse
	}
	b := ports.CollectionBatch{}
	valid := true
	root.Find(".video-img-box").Each(func(_ int, s *goquery.Selection) {
		a := s.Find(".detail .title a").First()
		href, _ := a.Attr("href")
		u, e := url.Parse(href)
		if e != nil || u.Scheme != "https" || u.Host != "jable.tv" || u.RawQuery != "" {
			valid = false
			return
		}
		matches := jableVideoPath.FindStringSubmatch(u.Path)
		if len(matches) != 2 {
			valid = false
			return
		}
		m := ports.CollectedMedia{SourceID: matches[1], URL: u.String(), Title: strings.TrimSpace(a.Text())}
		m.PosterURL, _ = s.Find("img").First().Attr("data-src")
		if m.Title == "" {
			valid = false
			return
		}
		b.Items = append(b.Items, m)
	})
	d.Find(".pagination a[data-parameters]").Each(func(_ int, a *goquery.Selection) {
		p, _ := a.Attr("data-parameters")
		v := jableNextPage.FindStringSubmatch(p)
		if len(v) == 2 {
			n, _ := strconv.Atoi(v[1])
			if n > req.Page {
				b.HasMore = true
			}
		}
	})
	if !valid {
		return ports.CollectionBatch{}, ErrParse
	}
	return b, nil
}

// collectStreamMetadata 读取 Jable/SupJav 的详情；不请求播放器、视频流或推断番号。
func collectStreamMetadata(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	base := "https://" + req.Source + ".com"
	if req.Source == "jable" {
		base = "https://jable.tv"
	}
	target := base + "/" + req.Query + ".html"
	if req.Source == "jable" {
		target = base + "/videos/" + req.Query + "/"
	}
	body, e := f.Get(ctx, target)
	if e != nil {
		return ports.CollectionBatch{}, e
	}
	d, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	canonical, _ := d.Find("link[rel=canonical]").Attr("href")
	if canonical != target {
		return ports.CollectionBatch{}, ErrParse
	}
	m := ports.CollectedMedia{SourceID: req.Query, URL: target}
	m.PosterURL, _ = d.Find("meta[property='og:image']").Attr("content")
	if req.Source == "jable" {
		m.Title = strings.TrimSpace(d.Find("#site-content .header-left h4").First().Text())
		d.Find("#site-content .tags a[href]").Each(func(_ int, a *goquery.Selection) { m.Tags = append(m.Tags, strings.TrimSpace(a.Text())) })
		d.Find("#site-content a.model img").Each(func(_ int, img *goquery.Selection) {
			name, _ := img.Attr("title")
			photo, _ := img.Attr("src")
			if name != "" {
				m.Actors = append(m.Actors, ports.CollectedActor{Name: name, Photo: photo})
			}
		})
	} else {
		m.Title = strings.TrimSpace(d.Find(".archive-title h1").Text())
		d.Find(".tags a[href]").Each(func(_ int, a *goquery.Selection) { m.Tags = append(m.Tags, strings.TrimSpace(a.Text())) })
		d.Find("a[href*='/category/cast/']").Each(func(_ int, a *goquery.Selection) {
			name := strings.TrimSpace(a.Text())
			if name != "" {
				m.Actors = append(m.Actors, ports.CollectedActor{Name: name})
			}
		})
	}
	if m.Title == "" {
		return ports.CollectionBatch{}, ErrParse
	}
	return ports.CollectionBatch{Items: []ports.CollectedMedia{m}}, nil
}

// collectSupJavSearch 仅处理搜索正文卡片，发布日期不是影片发行日期，不能映射。
func collectSupJavSearch(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	target := "https://supjav.com/"
	if req.Page > 1 {
		target += "page/" + strconv.Itoa(req.Page)
	}
	target += "?" + url.Values{"s": {req.Query}}.Encode()
	body, e := f.Get(ctx, target)
	if e != nil {
		return ports.CollectionBatch{}, e
	}
	d, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	b := ports.CollectionBatch{}
	valid := true
	d.Find(".post").Each(func(_ int, s *goquery.Selection) {
		a := s.Find("h3 a[rel=bookmark]").First()
		href, _ := a.Attr("href")
		u, e := url.Parse(href)
		if e != nil || u.Scheme != "https" || u.Host != "supjav.com" || !supjavIDPattern.MatchString(u.Path) || u.RawQuery != "" {
			valid = false
			return
		}
		m := ports.CollectedMedia{SourceID: strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".html"), URL: u.String(), Title: strings.TrimSpace(a.Text())}
		m.PosterURL, _ = s.Find("img.thumb").Attr("src")
		if m.Title == "" {
			valid = false
			return
		}
		b.Items = append(b.Items, m)
	})
	if !valid || (len(b.Items) == 0 && d.Find("body.search.search-no-results .content").Length() != 1) {
		return ports.CollectionBatch{}, ErrParse
	}
	b.HasMore = d.Find(".pagination .next-page a[href]").Length() > 0
	return b, nil
}
