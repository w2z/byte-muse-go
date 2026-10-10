package downloadclient

import (
	"bytemuse/backend/internal/ports"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Capabilities 按 qBittorrent 主版本区分暂停和停止，不通过失败写请求探测能力。
func (c *Qbittorrent) Capabilities(ctx context.Context) ([]string, error) {
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, http.MethodGet, "/api/v2/app/version", nil)
	if err != nil {
		return nil, err
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(raw)), "v")
	major, err := strconv.Atoi(strings.Split(version, ".")[0])
	if err != nil || major < 4 || major > 5 {
		return nil, fmt.Errorf("不支持的 qBittorrent 版本")
	}
	caps := []string{"resume", "retry", "delete", "delete_files"}
	if major >= 5 {
		caps = append(caps, "stop")
	} else {
		caps = append(caps, "pause")
	}
	return caps, nil
}

func validHash(hash string) bool {
	if len(hash) != 40 && len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// Observe 按精确 hash 查询单个任务；不存在返回 nil，拒绝 all 和复合选择器。
func (c *Qbittorrent) Observe(ctx context.Context, hash string) (*ports.TransferState, error) {
	if !validHash(hash) {
		return nil, fmt.Errorf("无效的任务 hash")
	}
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, http.MethodGet, "/api/v2/torrents/info?hashes="+url.QueryEscape(hash), nil)
	if err != nil {
		return nil, err
	}
	var items []struct {
		Hash         string
		State        string
		Progress     float64
		AddedOn      int64 `json:"added_on"`
		CompletionOn int64 `json:"completion_on"`
	}
	if err = json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	if items == nil {
		return nil, fmt.Errorf("qBittorrent 任务列表响应无效")
	}
	for _, item := range items {
		if strings.EqualFold(item.Hash, hash) {
			result := &ports.TransferState{Hash: strings.ToLower(hash), Status: transferStatus(item.State, item.Progress, item.CompletionOn)}
			if item.AddedOn > 0 {
				at := time.Unix(item.AddedOn, 0).UTC()
				result.AddedAt = &at
			}
			if item.CompletionOn > 0 {
				at := time.Unix(item.CompletionOn, 0).UTC()
				result.CompletedAt = &at
			}
			return result, nil
		}
	}
	return nil, nil
}

// Control 仅向精确任务发送一次命令；调用方负责持久化和结果回查。
func (c *Qbittorrent) Control(ctx context.Context, hash, action string) error {
	if !validHash(hash) {
		return fmt.Errorf("无效的任务 hash")
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(caps, action) {
		return ports.ErrDownloadAction
	}
	endpoint := action
	form := url.Values{"hashes": {hash}}
	switch action {
	case "resume", "retry":
		endpoint = "resume"
		if slices.Contains(caps, "stop") {
			endpoint = "start"
		}
	case "delete", "delete_files":
		endpoint = "delete"
		form.Set("deleteFiles", strconv.FormatBool(action == "delete_files"))
	}
	_, err = c.request(ctx, http.MethodPost, "/api/v2/torrents/"+endpoint, form)
	return err
}
