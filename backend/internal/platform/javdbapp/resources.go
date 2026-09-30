package javdbapp

import (
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"

	"bytemuse/backend/internal/platform/torrentsearch"
)

// Search 实现现有 ResourceSearcher：番号必须精确且唯一，磁力统一归为 BT。
// 不猜测相近番号，不在读取资源时执行下载。
func (c *Client) Search(ctx context.Context, code string) ([]torrentsearch.Resource, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" || len(code) > 128 {
		return nil, errors.New("invalid JavDB media code")
	}
	var data struct {
		Movies []Movie `json:"movies"`
	}
	if err := c.getJSON(ctx, "/api/v2/search", url.Values{"q": {code}, "type": {"movie"}, "page": {"1"}, "limit": {"100"}}, &data); err != nil {
		return nil, err
	}
	if data.Movies == nil {
		return nil, errors.New("JavDB search movies missing")
	}
	id := ""
	for _, m := range data.Movies {
		if strings.EqualFold(strings.TrimSpace(m.Number), code) {
			if !validID(m.ID) {
				return nil, errors.New("invalid JavDB search identity")
			}
			if id != "" && id != m.ID {
				return nil, errors.New("ambiguous JavDB movie number")
			}
			id = m.ID
		}
	}
	if id == "" {
		return []torrentsearch.Resource{}, nil
	}
	return c.Magnets(ctx, id)
}

// Magnets 校验 info hash 并保留 MiB 单位；不把缺失做种人数伪造成活跃资源。
func (c *Client) Magnets(ctx context.Context, id string) ([]torrentsearch.Resource, error) {
	if !validID(id) {
		return nil, errors.New("invalid JavDB movie id")
	}
	var data struct {
		Magnets []struct {
			Hash  string  `json:"hash"`
			Name  string  `json:"name"`
			Size  float64 `json:"size"`
			CNSub bool    `json:"cnsub"`
		} `json:"magnets"`
	}
	if err := c.getJSON(ctx, "/api/v1/movies/"+id+"/magnets", nil, &data); err != nil {
		return nil, err
	}
	if data.Magnets == nil {
		return nil, errors.New("JavDB magnets missing")
	}
	result := []torrentsearch.Resource{}
	seen := map[string]bool{}
	for _, m := range data.Magnets {
		hash := strings.ToLower(m.Hash)
		raw, err := hex.DecodeString(hash)
		if err != nil || len(raw) != 20 || m.Size < 0 || seen[hash] {
			continue
		}
		seen[hash] = true
		result = append(result, torrentsearch.Resource{Kind: "bt", Site: "JavDB", Title: m.Name, URI: "magnet:?xt=urn:btih:" + hash, InfoHash: hash, SizeMB: m.Size, Chinese: m.CNSub})
	}
	return result, nil
}
