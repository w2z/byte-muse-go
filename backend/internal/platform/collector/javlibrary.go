package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"github.com/PuerkitoBio/goquery"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// collectJavLibrary 使用明确的影片 ID、详情分区和下一页地址，不读取评论及用户资料。
func collectJavLibrary(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	const base = "https://www.javlibrary.com/cn/"
	target := base + "vl_searchbyid.php?" + url.Values{"keyword": {req.Query}, "page": {strconv.Itoa(req.Page)}}.Encode()
	if req.Kind == "rank" {
		paths := map[string]string{"wanted": "vl_mostwanted.php", "bestrated": "vl_bestrated.php", "newrelease": "vl_newrelease.php", "newentries": "vl_newentries.php"}
		target = base + paths[req.Period] + "?page=" + strconv.Itoa(req.Page)
	} else if req.Kind == "detail" {
		target = base + req.Query + ".html"
	}
	body, e := f.Get(ctx, target)
	if e != nil {
		return ports.CollectionBatch{}, e
	}
	d, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	b := ports.CollectionBatch{}
	if d.Find("#video_id .text").Length() > 0 {
		href, _ := d.Find("#video_title a").First().Attr("href")
		id, absolute := libraryIdentity(href)
		if id == "" || req.Kind == "detail" && id != req.Query {
			return b, ErrParse
		}
		m := ports.CollectedMedia{SourceID: id, URL: absolute, Code: strings.TrimSpace(d.Find("#video_id .text").Text()), Title: strings.TrimSpace(d.Find("#video_title a").First().Text()), ReleaseDate: strings.TrimSpace(d.Find("#video_date .text").Text())}
		m.PosterURL, _ = d.Find("#video_jacket_img").Attr("src")
		m.DurationMinutes, _ = strconv.Atoi(strings.TrimSpace(d.Find("#video_length .text").Text()))
		d.Find("#video_genres a").Each(func(_ int, a *goquery.Selection) { m.Tags = append(m.Tags, strings.TrimSpace(a.Text())) })
		d.Find("#video_cast .star a").Each(func(_ int, a *goquery.Selection) {
			m.Actors = append(m.Actors, ports.CollectedActor{Name: strings.TrimSpace(a.Text())})
		})
		if m.ReleaseDate != "" {
			if _, e = time.Parse("2006-01-02", m.ReleaseDate); e != nil {
				return b, ErrParse
			}
		}
		if m.Code == "" || m.Title == "" {
			return b, ErrParse
		}
		b.Items = append(b.Items, m)
		return b, nil
	}
	if req.Kind == "detail" || d.Find(".videothumblist").Length() == 0 {
		return b, ErrParse
	}
	valid := true
	d.Find(".videothumblist .video").Each(func(_ int, s *goquery.Selection) {
		a := s.Find("a[href]").First()
		href, _ := a.Attr("href")
		id, absolute := libraryIdentity(href)
		m := ports.CollectedMedia{SourceID: id, URL: absolute, Code: strings.TrimSpace(a.Find(".id").Text()), Title: strings.TrimSpace(a.Find(".title").Text())}
		m.PosterURL, _ = a.Find("img").Attr("src")
		if m.SourceID == "" || m.Code == "" || m.Title == "" {
			valid = false
			return
		}
		b.Items = append(b.Items, m)
	})
	d.Find(".page_selector a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		u, e := url.Parse(href)
		if e == nil {
			n, _ := strconv.Atoi(u.Query().Get("page"))
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

// libraryIdentity 仅接受该站已观测到的 HTML 影片路由，返回规范身份。
func libraryIdentity(href string) (string, string) {
	base, _ := url.Parse("https://www.javlibrary.com/cn/")
	u, e := url.Parse(href)
	if e != nil {
		return "", ""
	}
	u = base.ResolveReference(u)
	if u.Scheme != "https" || u.Host != base.Host || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/cn/") || !strings.HasSuffix(u.Path, ".html") {
		return "", ""
	}
	id := strings.TrimSuffix(path.Base(u.Path), ".html")
	if !strings.HasPrefix(id, "jav") || !sourceIDPattern.MatchString(id) {
		return "", ""
	}
	return id, base.String() + id + ".html"
}
