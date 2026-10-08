package downloadclient

import (
	"bytemuse/backend/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ReadMetrics 精确批量查询当前页任务；每批最多 50 个 hash，防止 URL 和响应无限增长。
// 不写下载器或数据库；缺失字段保留 nil，任何一批失败不返回部分成功快照。
func (c *Qbittorrent) ReadMetrics(ctx context.Context, hashes []string) (map[string]*domain.DownloadMetrics, error) {
	result := map[string]*domain.DownloadMetrics{}
	unique := []string{}
	seen := map[string]bool{}
	for _, hash := range hashes {
		hash = strings.ToLower(hash)
		if !validHash(hash) {
			return nil, fmt.Errorf("无效的任务 hash")
		}
		if !seen[hash] {
			seen[hash] = true
			unique = append(unique, hash)
		}
	}
	if len(unique) == 0 {
		return result, nil
	}
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	for offset := 0; offset < len(unique); offset += 50 {
		batch := unique[offset:min(offset+50, len(unique))]
		raw, err := c.request(ctx, http.MethodGet, "/api/v2/torrents/info?hashes="+url.QueryEscape(strings.Join(batch, "|")), nil)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			Hash           string   `json:"hash"`
			Size           *int64   `json:"size"`
			Remaining      *int64   `json:"amount_left"`
			Downloaded     *int64   `json:"downloaded"`
			DownloadSpeed  *int64   `json:"dlspeed"`
			UploadSpeed    *int64   `json:"upspeed"`
			SavePath       *string  `json:"save_path"`
			Ratio          *float64 `json:"ratio"`
			SeedingSeconds *int64   `json:"seeding_time"`
			Progress       float64  `json:"progress"`
			CompletionOn   int64    `json:"completion_on"`
			AddedOn        int64    `json:"added_on"`
		}
		if err = json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			hash := strings.ToLower(row.Hash)
			if !seen[hash] {
				continue
			}
			metric := &domain.DownloadMetrics{SizeBytes: nonnegative(row.Size), RemainingBytes: nonnegative(row.Remaining), DownloadedBytes: nonnegative(row.Downloaded), DownloadSpeed: nonnegative(row.DownloadSpeed), UploadSpeed: nonnegative(row.UploadSpeed), SavePath: row.SavePath, ShareRatio: row.Ratio, SeedingSeconds: nonnegative(row.SeedingSeconds), Complete: row.Progress >= 1 && row.CompletionOn > 0}
			if metric.ShareRatio != nil && *metric.ShareRatio < 0 {
				metric.ShareRatio = nil
			}
			if row.AddedOn > 0 {
				at := time.Unix(row.AddedOn, 0).UTC()
				metric.AddedAt = &at
			}
			result[hash] = metric
		}
	}
	return result, nil
}

// nonnegative 将下载器未知数值哨兵转换为空，避免误显示为真实负数或零。
func nonnegative(value *int64) *int64 {
	if value != nil && *value < 0 {
		return nil
	}
	return value
}
