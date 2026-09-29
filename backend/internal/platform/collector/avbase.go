package collector

import (
	"bytemuse/backend/internal/ports"
	"context"
	"encoding/json"
	"github.com/PuerkitoBio/goquery"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// avbaseActor 保留源站演员明确名称与头像，不展开别名建立重复演员。
type avbaseActor struct {
	Name  string `json:"name"`
	Image string `json:"image_url"`
}
type avbaseWork struct {
	ID     string        `json:"work_id"`
	Title  string        `json:"title"`
	Date   string        `json:"min_date"`
	Actors []avbaseActor `json:"actors"`
	Casts  []struct {
		Actor avbaseActor `json:"actor"`
	} `json:"casts"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Products []struct {
		Image string `json:"image_url"`
	} `json:"products"`
}

// collectAVBase 读取 Next.js SSR 资料，日期取影片 min_date 而不是 updated_at。
func collectAVBase(ctx context.Context, f Fetcher, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	path := "/works"
	expectedPage := path
	q := url.Values{"q": {req.Query}, "page": {strconv.Itoa(req.Page)}}
	if req.Kind == "actor" {
		path = "/talents/" + url.PathEscape(req.Query)
		expectedPage = "/talents/[name]"
		q.Del("q")
	}
	if req.Kind == "date" {
		path = "/works/date/" + req.Query
		expectedPage = "/works/date/[date]"
		q.Del("q")
	}
	if req.Kind == "detail" {
		path += "/" + url.PathEscape(req.Query)
		q = nil
	}
	target := "https://www.avbase.net" + path
	if q != nil {
		target += "?" + q.Encode()
	}
	body, e := f.Get(ctx, target)
	if e != nil {
		return ports.CollectionBatch{}, e
	}
	d, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	var envelope struct {
		Page  string `json:"page"`
		Props struct {
			PageProps struct {
				Works *[]avbaseWork `json:"works"`
				Work  *avbaseWork   `json:"work"`
				Page  int           `json:"page"`
				Total int           `json:"total"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if json.Unmarshal([]byte(d.Find("#__NEXT_DATA__").Text()), &envelope) != nil {
		return ports.CollectionBatch{}, ErrParse
	}
	p := envelope.Props.PageProps
	var works []avbaseWork
	if req.Kind == "detail" {
		if envelope.Page != "/works/[canonical_id]" || p.Work == nil || !strings.EqualFold(p.Work.ID, req.Query) {
			return ports.CollectionBatch{}, ErrParse
		}
		works = []avbaseWork{*p.Work}
	} else {
		if envelope.Page != expectedPage || p.Works == nil || p.Page != req.Page {
			return ports.CollectionBatch{}, ErrParse
		}
		works = *p.Works
	}
	b := ports.CollectionBatch{}
	for _, w := range works {
		if !sourceIDPattern.MatchString(w.ID) || strings.TrimSpace(w.Title) == "" {
			return ports.CollectionBatch{}, ErrParse
		}
		m := ports.CollectedMedia{SourceID: w.ID, Code: w.ID, Title: w.Title, URL: "https://www.avbase.net/works/" + url.PathEscape(w.ID)}
		if w.Date != "" {
			raw := strings.Split(w.Date, " (")[0]
			date, e := time.Parse("Mon Jan 02 2006 15:04:05 GMT-0700", raw)
			if e != nil {
				date, e = time.Parse(time.RFC3339, w.Date)
			}
			if e != nil {
				return ports.CollectionBatch{}, ErrParse
			}
			m.ReleaseDate = date.Format("2006-01-02")
		}
		if len(w.Products) > 0 {
			m.PosterURL = w.Products[0].Image
		}
		actors := w.Actors
		for _, cast := range w.Casts {
			actors = append(actors, cast.Actor)
		}
		for _, a := range actors {
			if strings.TrimSpace(a.Name) != "" {
				m.Actors = append(m.Actors, ports.CollectedActor{Name: a.Name, Photo: a.Image})
			}
		}
		for _, tag := range append(w.Tags, w.Genres...) {
			if strings.TrimSpace(tag.Name) != "" {
				m.Tags = append(m.Tags, tag.Name)
			}
		}
		b.Items = append(b.Items, m)
	}
	return b, nil
}
