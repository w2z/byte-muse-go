package collector

import (
	"context"
	"strconv"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"github.com/PuerkitoBio/goquery"
)

// collectJavDBDetail 只读取详情元数据；番号来自明确字段，不解析磁力或触发外站操作。
func collectJavDBDetail(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	target := "https://javdb.com/v/" + req.Query
	body, err := f.Get(ctx, target)
	if err != nil {
		return ports.CollectionBatch{}, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	if doc.Find("form[action='/users/sign_in']").Length() > 0 {
		return ports.CollectionBatch{}, ErrBlocked
	}
	canonical, _ := doc.Find("link[rel='canonical']").Attr("href")
	if canonical != target {
		return ports.CollectionBatch{}, ErrParse
	}
	m := ports.CollectedMedia{SourceID: req.Query, URL: target, Title: strings.TrimSpace(doc.Find("h2 .current-title").Text())}
	m.PosterURL, _ = doc.Find("img.video-cover").Attr("src")
	valid := true
	doc.Find(".panel-block").Each(func(_ int, s *goquery.Selection) {
		label := strings.TrimSpace(s.Find("strong").First().Text())
		value := s.Find(".value").First()
		switch label {
		case "番號:", "番号:":
			m.Code = strings.TrimSpace(value.Text())
		case "日期:":
			m.ReleaseDate = strings.TrimSpace(value.Text())
			if m.ReleaseDate != "" {
				if _, e := time.Parse("2006-01-02", m.ReleaseDate); e != nil {
					valid = false
				}
			}
		case "時長:", "时长:":
			parts := strings.Fields(value.Text())
			if len(parts) > 0 {
				m.DurationMinutes, _ = strconv.Atoi(parts[0])
			}
		case "類別:", "类别:":
			value.Find("a[href^='/tags?']").Each(func(_ int, a *goquery.Selection) {
				if name := strings.TrimSpace(a.Text()); name != "" {
					m.Tags = append(m.Tags, name)
				}
			})
		case "演員:", "演员:":
			value.Find("a[href^='/actors/']").Each(func(_ int, a *goquery.Selection) {
				if name := strings.TrimSpace(a.Text()); name != "" {
					m.Actors = append(m.Actors, ports.CollectedActor{Name: name})
				}
			})
		}
	})
	if !valid || m.Code == "" || m.Title == "" {
		return ports.CollectionBatch{}, ErrParse
	}
	// 详情页的「類別」是来源给出的权威分类证据，交给统一规则判定。
	m.VideoType = domain.ClassifyVideoType(m.Code, m.Title, m.Tags)
	return ports.CollectionBatch{Items: []ports.CollectedMedia{m}}, nil
}
