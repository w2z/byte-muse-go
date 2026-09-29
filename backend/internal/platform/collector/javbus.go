package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"github.com/PuerkitoBio/goquery"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// collectJavBusSearch 使用搜索列表中的 date 番号字段，不从标题猜测媒体身份。
func collectJavBusSearch(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	target := "https://www.javbus.com/search/" + url.PathEscape(req.Query) + "/" + strconv.Itoa(req.Page)
	body, e := f.Get(ctx, target)
	if e != nil {
		return ports.CollectionBatch{}, e
	}
	d, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	if d.Find("#waterfall").Length() != 1 {
		return ports.CollectionBatch{}, ErrParse
	}
	b := ports.CollectionBatch{}
	valid := true
	d.Find("#waterfall .movie-box").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		u, e := url.Parse(href)
		if e != nil || u.Host != "www.javbus.com" || u.Scheme != "https" || u.RawQuery != "" {
			valid = false
			return
		}
		code := strings.TrimSpace(a.Find(".photo-info date").First().Text())
		id := strings.TrimPrefix(u.Path, "/")
		title, _ := a.Find("img").Attr("title")
		if !sourceIDPattern.MatchString(id) || !strings.EqualFold(code, id) || strings.TrimSpace(title) == "" {
			valid = false
			return
		}
		m := ports.CollectedMedia{SourceID: id, Code: code, Title: title, URL: u.String(), ReleaseDate: strings.TrimSpace(a.Find(".photo-info date").Eq(1).Text())}
		m.PosterURL, _ = a.Find("img").Attr("src")
		if m.ReleaseDate != "" {
			if _, e = time.Parse("2006-01-02", m.ReleaseDate); e != nil {
				valid = false
				return
			}
		}
		b.Items = append(b.Items, m)
	})
	if !valid {
		return ports.CollectionBatch{}, ErrParse
	}
	b.HasMore = d.Find("a#next[href]").Length() > 0
	return b, nil
}

// collectJavBusDetail 只读公开详情字段，不采集磁力链接或下载资源。
func collectJavBusDetail(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	target := "https://www.javbus.com/" + url.PathEscape(req.Query)
	body, e := f.Get(ctx, target)
	if e != nil {
		return ports.CollectionBatch{}, e
	}
	d, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	m := ports.CollectedMedia{SourceID: req.Query, URL: target, Title: strings.TrimSpace(d.Find("h3").First().Text())}
	d.Find(".info p").Each(func(_ int, p *goquery.Selection) {
		label := strings.TrimSpace(p.Find(".header").First().Text())
		value := strings.TrimSpace(strings.TrimPrefix(p.Text(), label))
		switch label {
		case "識別碼:", "识别码:":
			m.Code = strings.TrimSpace(p.Find("span").Eq(1).Text())
		case "發行日期:", "发行日期:":
			m.ReleaseDate = value
		case "長度:", "长度:":
			digits := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(value, "分鐘"), "分钟"))
			m.DurationMinutes, _ = strconv.Atoi(digits)
		}
	})
	d.Find(".info .genre a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		u, e := url.Parse(href)
		if e != nil {
			return
		}
		name := strings.TrimSpace(a.Text())
		if name == "" {
			return
		}
		if strings.HasPrefix(u.Path, "/genre/") {
			m.Tags = append(m.Tags, name)
		} else if strings.HasPrefix(u.Path, "/star/") {
			m.Actors = append(m.Actors, ports.CollectedActor{Name: name})
		}
	})
	poster, _ := d.Find("a.bigImage").Attr("href")
	base, _ := url.Parse(target)
	if u, e := url.Parse(poster); e == nil && poster != "" {
		m.PosterURL = base.ResolveReference(u).String()
	}
	if m.Title == "" || !strings.EqualFold(m.Code, req.Query) || d.Find(".info").Length() == 0 {
		return ports.CollectionBatch{}, ErrParse
	}
	if m.ReleaseDate != "" {
		if _, e = time.Parse("2006-01-02", m.ReleaseDate); e != nil {
			return ports.CollectionBatch{}, ErrParse
		}
	}
	return ports.CollectionBatch{Items: []ports.CollectedMedia{m}}, nil
}
