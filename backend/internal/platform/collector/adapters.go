package collector

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"github.com/PuerkitoBio/goquery"
)

var sourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var datePattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// collectJavDB 仅解析已确认的搜索和榜单卡片，不把登录页视为空结果。
func collectJavDB(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	if req.Kind == "detail" {
		return collectJavDBDetail(ctx, f, req)
	}
	q := url.Values{"page": {strconv.Itoa(req.Page)}}
	path := "/search"
	if req.Kind == "rank" {
		path = "/rankings/movies"
		q.Set("p", req.Period)
		q.Set("t", "censored")
	} else {
		q.Set("q", req.Query)
		q.Set("f", "all")
	}
	body, err := f.Get(ctx, "https://javdb.com"+path+"?"+q.Encode())
	if err != nil {
		return ports.CollectionBatch{}, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	if doc.Find("form[action='/users/sign_in']").Length() > 0 || strings.Contains(doc.Find("title").Text(), "登入") {
		return ports.CollectionBatch{}, ErrCookieRequired
	}
	if doc.Find(".movie-list").Length() != 1 {
		return ports.CollectionBatch{}, ErrParse
	}
	batch := ports.CollectionBatch{}
	valid := true
	doc.Find(".movie-list .item").Each(func(_ int, s *goquery.Selection) {
		a := s.Find("a.box").First()
		href, _ := a.Attr("href")
		id := strings.TrimPrefix(href, "/v/")
		code := strings.TrimSpace(a.Find(".video-title strong").Text())
		title, _ := a.Attr("title")
		if !strings.HasPrefix(href, "/v/") || !sourceIDPattern.MatchString(id) || code == "" || strings.TrimSpace(title) == "" {
			valid = false
			return
		}
		poster, _ := a.Find(".cover img").Attr("src")
		date := datePattern.FindString(a.Find(".meta").Text())
		if date != "" {
			if _, e := time.Parse("2006-01-02", date); e != nil {
				valid = false
				return
			}
		}
		// 榜单请求明确限定 t=censored，直接采用来源分类；搜索结果没有类别字段，
		// 交回统一的 domain.ClassifyVideoType 判定，不在解析器里另写一套规则。
		videoType := domain.VideoTypeCensored
		if req.Kind != "rank" {
			videoType = domain.ClassifyVideoType(code, strings.TrimSpace(title), nil)
		}
		batch.Items = append(batch.Items, ports.CollectedMedia{SourceID: id, URL: "https://javdb.com" + href, Code: code, Title: strings.TrimSpace(title), PosterURL: poster, ReleaseDate: date, VideoType: videoType})
	})
	if !valid {
		return ports.CollectionBatch{}, ErrParse
	}
	batch.HasMore = doc.Find("a.pagination-next[href]:not([disabled])").Length() > 0
	return batch, nil
}

type netflavVideo struct {
	ID       string   `json:"videoId"`
	Code     string   `json:"code"`
	Title    string   `json:"title"`
	Poster   string   `json:"preview"`
	PosterHD string   `json:"preview_hp"`
	Date     string   `json:"videoDate"`
	Duration string   `json:"duration"`
	Actors   []string `json:"actors"`
	Tags     []string `json:"tags"`
}

// collectNetflav 读取 SSR 资料，明确区分发行日期 videoDate 与入站时间 sourceDate。
func collectNetflav(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	path := "/search"
	q := url.Values{"type": {"title"}, "keyword": {req.Query}, "page": {strconv.Itoa(req.Page)}}
	if req.Kind == "detail" {
		path = "/video"
		q = url.Values{"id": {req.Query}}
	}
	body, e := f.Get(ctx, "https://netflav.com"+path+"?"+q.Encode())
	if e != nil {
		return ports.CollectionBatch{}, e
	}
	doc, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	var envelope struct {
		Page  string `json:"page"`
		Props struct {
			State struct {
				Search *struct {
					Docs  *[]netflavVideo `json:"docs"`
					Page  int             `json:"page"`
					Pages int             `json:"pages"`
				} `json:"search"`
				Video *struct {
					Data    *netflavVideo `json:"data"`
					IsError bool          `json:"isError"`
				} `json:"video"`
			} `json:"initialState"`
		} `json:"props"`
	}
	if json.Unmarshal([]byte(doc.Find("script#__NEXT_DATA__").Text()), &envelope) != nil || envelope.Page != path {
		return ports.CollectionBatch{}, ErrParse
	}
	batch := ports.CollectionBatch{}
	var videos []netflavVideo
	if req.Kind == "detail" {
		v := envelope.Props.State.Video
		if v == nil || v.IsError || v.Data == nil || v.Data.ID != req.Query {
			return batch, ErrParse
		}
		videos = []netflavVideo{*v.Data}
	} else {
		s := envelope.Props.State.Search
		if s == nil || s.Docs == nil || s.Page != req.Page {
			return batch, ErrParse
		}
		videos = *s.Docs
		batch.HasMore = s.Page < s.Pages
	}
	for _, v := range videos {
		if !sourceIDPattern.MatchString(v.ID) || strings.TrimSpace(v.Title) == "" {
			return ports.CollectionBatch{}, ErrParse
		}
		m := ports.CollectedMedia{SourceID: v.ID, URL: "https://netflav.com/video?id=" + url.QueryEscape(v.ID), Code: v.Code, Title: v.Title, PosterURL: v.PosterHD, Tags: localizedValues(v.Tags)}
		m.VideoType = domain.ClassifyVideoType(m.Code, m.Title, m.Tags)
		if m.PosterURL == "" {
			m.PosterURL = v.Poster
		}
		if v.Date != "" {
			parsed, e := time.Parse(time.RFC3339, v.Date)
			if e != nil {
				return ports.CollectionBatch{}, ErrParse
			}
			m.ReleaseDate = parsed.Format("2006-01-02")
		}
		for _, name := range localizedValues(v.Actors) {
			m.Actors = append(m.Actors, ports.CollectedActor{Name: name})
		}
		for _, unit := range []string{"分鐘", "分钟", "分"} {
			if strings.HasSuffix(v.Duration, unit) {
				m.DurationMinutes, _ = strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(v.Duration, unit)))
				break
			}
		}
		batch.Items = append(batch.Items, m)
	}
	return batch, nil
}

// localizedValues 优先日文原名，避免把同一演员的语言别名建立为多个演员。
func localizedValues(values []string) []string {
	var selected []string
	for _, v := range values {
		if strings.HasPrefix(v, "jp:") {
			selected = append(selected, strings.TrimPrefix(v, "jp:"))
		}
	}
	if len(selected) == 0 {
		for _, v := range values {
			if !strings.Contains(v, ":") {
				selected = append(selected, v)
			}
		}
	}
	result := []string{}
	seen := map[string]bool{}
	for _, v := range selected {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
