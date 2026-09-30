// Package javdbapp implements the read-only JavDB mobile API used for richer
// metadata. It deliberately keeps transport concerns here so HTML collectors
// and actor synchronisation can share the same signed request contract.
package javdbapp

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	appVersion       = "1.9.28"
	appVersionNumber = "10928"
	userAgent        = "Dart/3.4 (dart:io)"
	signatureSuffix  = "lpw6vgqzsp"
	signaturePrefix  = "71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e7d5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa"
	maxResponseBytes = 4 << 20
)

// Actor is the actor projection returned by the JavDB App API. NameZHT is
// retained as an alias candidate; AvatarURL is the only image URL persisted.
type Actor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NameZHT   string `json:"name_zht"`
	AvatarURL string `json:"avatar_url"`
}

// Client performs bounded, signed, read-only App API requests. The caller
// supplies an HTTP client so proxy, cookie and route policy stay outside this package.
type Client struct {
	mu         sync.Mutex
	routes     []string
	next       time.Time
	cooldown   time.Time
	http       *http.Client
	host       string
	deviceUUID string
}

var bootstrapHosts = []string{"https://jdforrepam.com", "https://apidd.spthgb.com", "https://apidd.czssdgz.com", "https://javdb.com"}
var errNetwork = errors.New("JavDB App API network unavailable")

// ErrRateLimited 表示源站冷却期，调用方不能通过网页回退绕过限流。
var ErrRateLimited = errors.New("JavDB App API rate limited")

type httpError int

func (e httpError) Error() string        { return fmt.Sprintf("JavDB App API status %d", int(e)) }
func (e httpError) Is(target error) bool { return e == 429 && target == ErrRateLimited }

// NewAutomatic 使用固定的公开 API 线路，代理由调用方配置；成功线路在进程内复用。
func NewAutomatic(client *http.Client) (*Client, error) {
	c, err := NewClient(client, bootstrapHosts[0], "")
	if err != nil {
		return nil, err
	}
	c.routes = append([]string{}, bootstrapHosts...)
	return c, nil
}

// NewClient 接受代码装配的单一 API 地址，不接受终端用户 URL；受控测试可传本地服务。
// 它克隆传输配置，禁止重定向，不修改调用方客户端。
func NewClient(httpClient *http.Client, host, deviceUUID string) (*Client, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	clone := *httpClient
	if clone.Timeout == 0 {
		clone.Timeout = 20 * time.Second
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("JavDB App redirect rejected") }
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(host), "/"))
	if err != nil || parsed.Host == "" || parsed.Path != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid JavDB App host")
	}
	if strings.TrimSpace(deviceUUID) == "" {
		deviceUUID = newDeviceUUID()
	}
	return &Client{http: &clone, host: parsed.String(), deviceUUID: deviceUUID}, nil
}

func newDeviceUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("bytemuse-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// MovieActors reads actors from /api/v4/movies/{id}; malformed or unsuccessful
// envelopes are returned as errors so callers can explicitly choose fallback behavior.
func (c *Client) MovieActors(ctx context.Context, movieID string) ([]Actor, error) {
	movieID = strings.TrimSpace(movieID)
	if !validID(movieID) {
		return nil, errors.New("invalid JavDB movie id")
	}
	var data struct {
		Movie struct {
			Actors []Actor `json:"actors"`
		} `json:"movie"`
	}
	if err := c.getJSON(ctx, "/api/v4/movies/"+movieID, nil, &data); err != nil {
		return nil, err
	}
	if data.Movie.Actors == nil {
		return nil, errors.New("JavDB App API response has no actors")
	}
	return data.Movie.Actors, nil
}

// getJSON 统一签名、响应上限和信封检查；不向调用方暴露代理错误中的凭据。
func (c *Client) getJSON(ctx context.Context, path string, params url.Values, target any) error {
	c.mu.Lock()
	host := c.host
	routes := append([]string{}, c.routes...)
	c.mu.Unlock()
	err := c.request(ctx, host, path, params, target)
	if !retryable(err) {
		return err
	}
	for _, next := range routes {
		if next == host {
			continue
		}
		err = c.request(ctx, next, path, params, target)
		if err == nil {
			c.mu.Lock()
			c.host = next
			c.mu.Unlock()
			return nil
		}
		if !retryable(err) {
			return err
		}
	}
	return err
}

func retryable(err error) bool {
	var status httpError
	return errors.Is(err, errNetwork) || (errors.As(err, &status) && (status == 502 || status == 503 || status == 504))
}

func (c *Client) request(ctx context.Context, host, path string, params url.Values, target any) error {
	c.mu.Lock()
	if time.Now().Before(c.cooldown) {
		c.mu.Unlock()
		return httpError(429)
	}
	start := c.next
	if start.Before(time.Now()) {
		start = time.Now()
	}
	c.next = start.Add(500 * time.Millisecond)
	c.mu.Unlock()
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	c.mu.Lock()
	limited := time.Now().Before(c.cooldown)
	c.mu.Unlock()
	if limited {
		return httpError(429)
	}
	query := url.Values{
		"app_channel": {"official"}, "app_version": {appVersion}, "app_version_number": {appVersionNumber},
		"platform": {"android"}, "system_version": {"13"}, "device_model": {"Pixel 6"}, "device_name": {"Pixel"}, "device_uuid": {c.deviceUUID},
	}
	for key, values := range params {
		query[key] = values
	}
	endpoint := host + path + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("invalid JavDB App request")
	}
	req.Header.Set("accept-language", "zh-TW")
	req.Header.Set("connection", "keep-alive")
	req.Header.Set("user-agent", userAgent)
	req.Header.Set("jdsignature", signature(time.Now().Unix()))
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errNetwork
	}
	defer response.Body.Close()
	if response.StatusCode == 429 {
		delay := 30 * time.Second
		if seconds, e := time.ParseDuration(response.Header.Get("Retry-After") + "s"); e == nil && seconds > 0 {
			delay = seconds
		} else if until, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil && time.Until(until) > 0 {
			delay = time.Until(until)
		}
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
		c.mu.Lock()
		c.cooldown = time.Now().Add(delay)
		c.mu.Unlock()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return httpError(response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("JavDB App API read failed")
	}
	if len(body) > maxResponseBytes {
		return errors.New("JavDB App API response too large")
	}
	var envelope struct {
		Success json.RawMessage `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(body, &envelope); err != nil {
		return errors.New("invalid JavDB App JSON response")
	}
	if !successValue(envelope.Success) {
		return errors.New("JavDB App API returned unsuccessful response")
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("JavDB App API response has no data")
	}
	if json.Unmarshal(envelope.Data, target) != nil {
		return errors.New("invalid JavDB App data")
	}
	return nil
}

func successValue(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var n int
	return json.Unmarshal(raw, &n) == nil && n == 1
}

func signature(seconds int64) string {
	value := fmt.Sprintf("%d", seconds)
	sum := md5.Sum([]byte(value + signaturePrefix))
	return value + "." + signatureSuffix + "." + hex.EncodeToString(sum[:])
}
