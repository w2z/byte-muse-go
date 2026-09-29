// Package downloadclient provides bounded clients for configured torrent downloaders.
package downloadclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// Qbittorrent submits magnets and reconciles submissions by info hash.
type Qbittorrent struct {
	origin, username, password, path, category string
	client                                     *http.Client
}

// TransferState is a read-only snapshot of one torrent in the downloader.
type TransferState struct {
	Hash        string
	Status      string
	AddedAt     *time.Time
	CompletedAt *time.Time
}

// ListTransferStates reads qBittorrent transfer metadata without changing any torrent.
func (c *Qbittorrent) ListTransferStates(ctx context.Context) ([]TransferState, error) {
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, http.MethodGet, "/api/v2/torrents/info", nil)
	if err != nil {
		return nil, err
	}
	var values []struct {
		Hash         string  `json:"hash"`
		State        string  `json:"state"`
		Progress     float64 `json:"progress"`
		AddedOn      int64   `json:"added_on"`
		CompletionOn int64   `json:"completion_on"`
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	items := make([]TransferState, 0, len(values))
	for _, value := range values {
		if value.Hash == "" {
			continue
		}
		item := TransferState{Hash: strings.ToLower(value.Hash), Status: transferStatus(value.State, value.Progress, value.CompletionOn)}
		if value.AddedOn > 0 {
			at := time.Unix(value.AddedOn, 0).UTC()
			item.AddedAt = &at
		}
		if value.CompletionOn > 0 {
			at := time.Unix(value.CompletionOn, 0).UTC()
			item.CompletedAt = &at
		}
		items = append(items, item)
	}
	return items, nil
}

func transferStatus(state string, progress float64, completed int64) string {
	if strings.HasPrefix(state, "error") || state == "missingFiles" {
		return "failed"
	}
	if completed > 0 && progress >= 1 {
		return "completed"
	}
	if strings.HasPrefix(state, "stopped") {
		return "stopped"
	}
	if strings.HasPrefix(state, "paused") {
		return "paused"
	}
	return "downloading"
}

// NewQbittorrent configures one client without performing network activity.
func NewQbittorrent(origin, username, password, path, category string, client *http.Client) *Qbittorrent {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	clone := *client
	if clone.Timeout == 0 {
		clone.Timeout = 20 * time.Second
	}
	jar, _ := cookiejar.New(nil)
	clone.Jar = jar
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return fmt.Errorf("download client redirect rejected")
	}
	return &Qbittorrent{origin: strings.TrimRight(origin, "/"), username: username, password: password, path: path, category: category, client: &clone}
}

// requestRaw 返回状态码与受限长度的响应体，供需要区分具体 2xx 语义的调用方使用。
func (c *Qbittorrent) requestRaw(ctx context.Context, method, path string, form url.Values) (int, []byte, error) {
	target := c.origin + path
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return 0, nil, e
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, e := c.client.Do(req)
	if e != nil {
		return 0, nil, e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil {
		return 0, nil, e
	}
	if len(raw) > 2<<20 {
		return 0, nil, fmt.Errorf("qBittorrent response too large")
	}
	return response.StatusCode, raw, nil
}

// request 只接受 2xx；调用方按需校验响应体。qBittorrent 5.x 的登录成功返回 204 空响应，
// 其余接口仍返回 200，因此这里不把状态码固定为 200。
func (c *Qbittorrent) request(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	status, raw, e := c.requestRaw(ctx, method, path, form)
	if e != nil {
		return nil, e
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("qBittorrent HTTP %d", status)
	}
	return raw, nil
}

// qbAcceptedBody 判断写操作是否成功：qBittorrent 4.x/5.x 返回 "Ok."，5.x 登录返回空响应，
// 其余非空内容（例如 "Fails."）一律视为失败。
func qbAcceptedBody(raw []byte) bool {
	body := strings.TrimSpace(string(raw))
	return body == "" || body == "Ok."
}

func (c *Qbittorrent) login(ctx context.Context) error {
	if c.origin == "" || c.username == "" || c.password == "" {
		return fmt.Errorf("qBittorrent is not configured")
	}
	status, raw, e := c.requestRaw(ctx, http.MethodPost, "/api/v2/auth/login", url.Values{"username": {c.username}, "password": {c.password}})
	if e != nil {
		return e
	}
	if status < 200 || status >= 300 || !qbAcceptedBody(raw) {
		return fmt.Errorf("qBittorrent login rejected")
	}
	return nil
}

// HasHash checks whether the requested torrent already exists after any ambiguous submission.
func (c *Qbittorrent) HasHash(ctx context.Context, hash string) (bool, error) {
	if e := c.login(ctx); e != nil {
		return false, e
	}
	raw, e := c.request(ctx, http.MethodGet, "/api/v2/torrents/info?hashes="+url.QueryEscape(hash), nil)
	if e != nil {
		return false, e
	}
	var items []struct {
		Hash string `json:"hash"`
	}
	if e = json.Unmarshal(raw, &items); e != nil {
		return false, e
	}
	for _, item := range items {
		if strings.EqualFold(item.Hash, hash) {
			return true, nil
		}
	}
	return false, nil
}

// Submit adds one magnet; callers persist intent and reconcile the hash before retrying.
func (c *Qbittorrent) Submit(ctx context.Context, magnet string) error {
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:") {
		return fmt.Errorf("invalid magnet")
	}
	if e := c.login(ctx); e != nil {
		return e
	}
	form := url.Values{"urls": {magnet}, "savepath": {c.path}, "category": {c.category}, "tags": {"BYTE_MUSE"}}
	raw, e := c.request(ctx, http.MethodPost, "/api/v2/torrents/add", form)
	if e != nil {
		return e
	}
	if !qbAcceptedBody(raw) {
		return fmt.Errorf("qBittorrent add rejected")
	}
	return nil
}

// SubmitTorrent uploads the exact authenticated .torrent bytes; the client never fetches the private URL itself.
func (c *Qbittorrent) SubmitTorrent(ctx context.Context, torrent []byte) error {
	if len(torrent) == 0 || len(torrent) > 4<<20 {
		return fmt.Errorf("invalid torrent file size")
	}
	if e := c.login(ctx); e != nil {
		return e
	}
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	part, e := writer.CreateFormFile("torrents", "bytemuse.torrent")
	if e != nil {
		return e
	}
	if _, e = part.Write(torrent); e != nil {
		return e
	}
	for key, value := range map[string]string{"savepath": c.path, "category": c.category, "tags": "BYTE_MUSE"} {
		if e = writer.WriteField(key, value); e != nil {
			return e
		}
	}
	if e = writer.Close(); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/api/v2/torrents/add", strings.NewReader(body.String()))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response, e := c.client.Do(req)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 1024))
	if e != nil {
		return e
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !qbAcceptedBody(raw) {
		return fmt.Errorf("qBittorrent torrent add rejected")
	}
	return nil
}
