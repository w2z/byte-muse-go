package javdbapp

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// Movie 保留 App 来源身份及演员图谱，不改变本地订阅或下载状态。
type Movie struct {
	ID          string  `json:"id"`
	Number      string  `json:"number"`
	Title       string  `json:"title"`
	ReleaseDate string  `json:"release_date"`
	Duration    int     `json:"duration"`
	CoverURL    string  `json:"cover_url"`
	ThumbURL    string  `json:"thumb_url"`
	Type        *int    `json:"type"`
	Actors      []Actor `json:"actors"`
	Tags        []struct {
		Name    string `json:"name"`
		NameZHT string `json:"name_zht"`
	} `json:"tags"`
}

func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

// Detail 校验返回的影片身份，防止重定向或错误响应关联到其他影片。
func (c *Client) Detail(ctx context.Context, id string) (Movie, error) {
	if !validID(id) {
		return Movie{}, errors.New("invalid JavDB movie id")
	}
	var data struct {
		Movie Movie `json:"movie"`
	}
	if err := c.getJSON(ctx, "/api/v4/movies/"+id, nil, &data); err != nil {
		return Movie{}, err
	}
	if data.Movie.ID != id || strings.TrimSpace(data.Movie.Number) == "" || strings.TrimSpace(data.Movie.Title) == "" {
		return Movie{}, errors.New("invalid JavDB movie identity")
	}
	return data.Movie, nil
}

// ActorMovies 使用来源演员 ID 查询作品；不能用本地演员名称替代 ID。
func (c *Client) ActorMovies(ctx context.Context, id string, page int) ([]Movie, error) {
	if !validID(id) || page < 1 {
		return nil, errors.New("invalid JavDB actor request")
	}
	var data struct {
		Movies []Movie `json:"movies"`
	}
	params := url.Values{"filter_by": {":a:" + id}, "sort_by": {"release"}, "order_by": {"desc"}, "page": {strconv.Itoa(page)}, "limit": {"24"}}
	if err := c.getJSON(ctx, "/api/v1/movies/tags", params, &data); err != nil {
		return nil, err
	}
	if data.Movies == nil {
		return nil, errors.New("JavDB actor movies missing")
	}
	for _, m := range data.Movies {
		if !validID(m.ID) || strings.TrimSpace(m.Number) == "" {
			return nil, errors.New("invalid JavDB actor movie")
		}
	}
	return data.Movies, nil
}
