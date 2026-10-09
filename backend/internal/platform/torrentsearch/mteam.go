package torrentsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const mteamOrigin = "https://api.m-team.cc"

var mteamID = regexp.MustCompile("^[0-9]+$")

// MTeamSearcher uses the official authenticated search API for private torrents.
type MTeamSearcher struct {
	client      *http.Client
	origin, key string
}

// NewMTeamSearcher configures M-Team without exposing its key to callers.
func NewMTeamSearcher(client *http.Client, origin, key string) *MTeamSearcher {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if origin == "" {
		origin = mteamOrigin
	}
	clone := *client
	if clone.Timeout == 0 {
		clone.Timeout = 20 * time.Second
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return fmt.Errorf("M-Team redirect rejected") }
	return &MTeamSearcher{client: &clone, origin: strings.TrimRight(origin, "/"), key: key}
}

// Search returns bounded adult-mode results and treats API errors as errors, never empty search results.
func (s *MTeamSearcher) Search(ctx context.Context, code string) ([]Resource, error) {
	if strings.TrimSpace(s.key) == "" {
		return nil, fmt.Errorf("M-Team API key is not configured")
	}
	if code == "" || len(code) > 128 {
		return nil, fmt.Errorf("invalid media code")
	}
	payload, _ := json.Marshal(map[string]any{"pageNumber": 1, "pageSize": 50, "keyword": code, "mode": "adult"})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, s.origin+"/api/torrent/search", bytes.NewReader(payload))
	if e != nil {
		return nil, e
	}
	req.Header.Set("x-api-key", s.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 ByteMuse")
	response, e := s.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("M-Team search unavailable: %w", e)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("M-Team search HTTP %d", response.StatusCode)
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(body) > 2<<20 {
		return nil, fmt.Errorf("M-Team response too large")
	}
	var envelope struct {
		Code json.RawMessage `json:"code"`
		Data struct {
			Items []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Size     string `json:"size"`
				Seeders  string `json:"seeders"`
				Discount string `json:"discount"`
			} `json:"data"`
		} `json:"data"`
	}
	if e = json.Unmarshal(body, &envelope); e != nil {
		return nil, fmt.Errorf("parse M-Team search: %w", e)
	}
	status := strings.Trim(string(envelope.Code), "\" ")
	if status != "0" && status != "200" {
		return nil, fmt.Errorf("M-Team search rejected")
	}
	out := make([]Resource, 0, len(envelope.Data.Items))
	for _, item := range envelope.Data.Items {
		if !mteamID.MatchString(item.ID) || !codeInTitle(item.Name, code) {
			continue
		}
		size, _ := strconv.ParseFloat(item.Size, 64)
		seeders, _ := strconv.Atoi(item.Seeders)
		title := strings.TrimSpace(item.Name)
		upper := strings.ToUpper(title)
		chinese := strings.Contains(title, "中文") || strings.Contains(title, "字幕") || strings.Contains(title, "中字")
		out = append(out, Resource{Kind: "pt", Site: "馒头", Title: title, URI: "mteam:" + item.ID, SizeMB: size / (1024 * 1024), Seeders: seeders, Chinese: chinese, UHD: strings.Contains(upper, "4K") || strings.Contains(upper, "2160P"), UC: strings.Contains(upper, "UNCENSORED"), Free: strings.Contains(strings.ToUpper(item.Discount), "FREE")})
	}
	return out, nil
}

// Download obtains a short-lived authenticated URL, reads the private torrent, and hashes its raw info dictionary.
// Returns bytes, info hash and the actual URL for internal task persistence; the URL can expire.
func (s *MTeamSearcher) Download(ctx context.Context, reference string) ([]byte, string, string, error) {
	id := strings.TrimPrefix(reference, "mteam:")
	if reference == id || !mteamID.MatchString(id) {
		return nil, "", "", fmt.Errorf("invalid M-Team resource")
	}
	if strings.TrimSpace(s.key) == "" {
		return nil, "", "", fmt.Errorf("M-Team API key is not configured")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, s.origin+"/api/torrent/genDlToken?id="+id, nil)
	if e != nil {
		return nil, "", "", e
	}
	req.Header.Set("x-api-key", s.key)
	req.Header.Set("User-Agent", "Mozilla/5.0 ByteMuse")
	response, e := s.client.Do(req)
	if e != nil {
		return nil, "", "", fmt.Errorf("M-Team download token unavailable: %w", e)
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	_ = response.Body.Close()
	if e != nil {
		return nil, "", "", e
	}
	if response.StatusCode != 200 || len(body) > 64<<10 {
		return nil, "", "", fmt.Errorf("M-Team download token rejected")
	}
	var token struct {
		Code json.RawMessage `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if e = json.Unmarshal(body, &token); e != nil {
		return nil, "", "", e
	}
	status := strings.Trim(string(token.Code), "\" ")
	if status != "0" && status != "200" {
		return nil, "", "", fmt.Errorf("M-Team download token rejected")
	}
	var rawURL string
	if e = json.Unmarshal(token.Data, &rawURL); e != nil {
		var obj struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(token.Data, &obj) != nil {
			return nil, "", "", fmt.Errorf("M-Team download URL missing")
		}
		rawURL = obj.URL
	}
	target, e := url.Parse(rawURL)
	origin, _ := url.Parse(s.origin)
	if e != nil || target.Host != origin.Host || target.Scheme != origin.Scheme || target.User != nil || target.Fragment != "" {
		return nil, "", "", fmt.Errorf("M-Team download URL host rejected")
	}
	get, e := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if e != nil {
		return nil, "", "", e
	}
	get.Header.Set("User-Agent", "Mozilla/5.0 ByteMuse")
	torrentResponse, e := s.client.Do(get)
	if e != nil {
		return nil, "", "", fmt.Errorf("M-Team torrent unavailable: %w", e)
	}
	defer torrentResponse.Body.Close()
	if torrentResponse.StatusCode != 200 {
		return nil, "", "", fmt.Errorf("M-Team torrent HTTP %d", torrentResponse.StatusCode)
	}
	torrent, e := io.ReadAll(io.LimitReader(torrentResponse.Body, (4<<20)+1))
	if e != nil {
		return nil, "", "", e
	}
	if len(torrent) > 4<<20 {
		return nil, "", "", fmt.Errorf("M-Team torrent too large")
	}
	hash, e := TorrentInfoHash(torrent)
	if e != nil {
		return nil, "", "", e
	}
	return torrent, hash, rawURL, nil
}
