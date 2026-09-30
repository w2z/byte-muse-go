package collector

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/javdbapp"
	"bytemuse/backend/internal/ports"
	"github.com/PuerkitoBio/goquery"
)

// collectJavDBDetail 只读取详情元数据；番号来自明确字段，不解析磁力或触发外站操作。
func collectJavDBDetail(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	if app, ok := f.(interface {
		MovieDetail(context.Context, string) (javdbapp.Movie, error)
	}); ok {
		movie, err := app.MovieDetail(ctx, req.Query)
		if err == nil {
			return ports.CollectionBatch{Items: []ports.CollectedMedia{appCollectedMovie(movie)}}, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ports.CollectionBatch{}, err
		}
		if errors.Is(err, javdbapp.ErrRateLimited) {
			return ports.CollectionBatch{}, ErrRateLimited
		}
		logging.Info(logging.CategoryCollection, "JavDB App 详情读取失败，尝试网页详情")
	}
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

// appCollectedMovie 转换已校验的 App 资料；别名只用于目录查询，不合并历史演员。
func appCollectedMovie(movie javdbapp.Movie) ports.CollectedMedia {
	m := ports.CollectedMedia{SourceID: movie.ID, URL: "https://javdb.com/v/" + movie.ID, Code: movie.Number, Title: movie.Title, PosterURL: movie.CoverURL, ReleaseDate: movie.ReleaseDate, DurationMinutes: movie.Duration, Actors: []ports.CollectedActor{}, Tags: []string{}}
	if m.PosterURL == "" {
		m.PosterURL = movie.ThumbURL
	}
	for _, a := range movie.Actors {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = strings.TrimSpace(a.NameZHT)
		}
		if name == "" {
			continue
		}
		actor := ports.CollectedActor{SourceID: a.ID, Name: name, Photo: strings.TrimSpace(a.AvatarURL)}
		if alias := strings.TrimSpace(a.NameZHT); alias != "" && alias != name {
			actor.Aliases = []string{alias}
		}
		m.Actors = append(m.Actors, actor)
	}
	for _, tag := range movie.Tags {
		name := strings.TrimSpace(tag.NameZHT)
		if name == "" {
			name = strings.TrimSpace(tag.Name)
		}
		if name != "" {
			m.Tags = append(m.Tags, name)
		}
	}
	m.VideoType = domain.ClassifyVideoType(m.Code, m.Title, m.Tags)
	if m.VideoType == "" && movie.Type != nil {
		switch *movie.Type {
		case 0:
			m.VideoType = "censored"
		case 1:
			m.VideoType = "uncensored"
		}
	}
	return m
}

// collectJavDBActor 只采集指定演员的作品页，结果交给现有逐影片事务队列。
func collectJavDBActor(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	app, ok := f.(interface {
		ActorMovies(context.Context, string, int) ([]javdbapp.Movie, error)
	})
	if !ok {
		return ports.CollectionBatch{}, ErrUnavailable
	}
	movies, err := app.ActorMovies(ctx, req.Query, req.Page)
	if err != nil {
		return ports.CollectionBatch{}, err
	}
	batch := ports.CollectionBatch{Items: []ports.CollectedMedia{}, Actors: []ports.CollectedActor{}, HasMore: len(movies) >= 24}
	for _, m := range movies {
		batch.Items = append(batch.Items, appCollectedMovie(m))
	}
	return batch, nil
}
