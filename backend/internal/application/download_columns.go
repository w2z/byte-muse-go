package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DownloadSourceSite is one stored source category and its unfiltered task count.
// Value is case-normalized; the empty value groups missing sources.
type DownloadSourceSite struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// downloadSourceSiteValue shares normalization between category aggregation and filtering.
func downloadSourceSiteValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*value))
}

// SourceSites lists every stored category, independent of pagination and active filters.
// It reads persisted tasks only and never contacts a downloader or changes historical data.
func (s *DownloadService) SourceSites(ctx context.Context) ([]DownloadSourceSite, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	page, err := s.repository.List(ctx, ports.DownloadListQuery{All: true})
	if err != nil {
		return nil, err
	}
	groups := map[string]DownloadSourceSite{}
	for _, task := range page.Items {
		value := downloadSourceSiteValue(task.SourceSite)
		label := "未识别"
		if value != "" {
			label = strings.TrimSpace(*task.SourceSite)
		}
		group := groups[value]
		if group.Count == 0 || label < group.Label {
			group.Label = label
		}
		group.Value = value
		group.Count++
		groups[value] = group
	}
	items := make([]DownloadSourceSite, 0, len(groups))
	for _, group := range groups {
		items = append(items, group)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Value == "" || items[j].Value == "" {
			return items[j].Value == "" && items[i].Value != ""
		}
		return items[i].Value < items[j].Value
	})
	return items, nil
}

// downloadColumnKinds is the authoritative allowlist for public table queries.
var downloadColumnKinds = map[string]string{
	"code": "text", "source_site": "site", "downloader": "enum", "transfer_status": "enum",
	"size_bytes": "number", "remaining_bytes": "number", "downloaded_bytes": "number", "download_speed": "number", "upload_speed": "number",
	"download_url": "text", "save_path": "text", "share_ratio": "number", "seeding_seconds": "number", "seeding": "enum", "added_at": "time", "completed_at": "time", "error_message": "text",
}

// validateDownloadColumns rejects unknown columns, invalid bounds and unsupported enum values.
func validateDownloadColumns(q ports.DownloadListQuery) error {
	if q.SortBy != "" && downloadColumnKinds[q.SortBy] == "" || q.SortOrder != "" && q.SortOrder != "asc" && q.SortOrder != "desc" || q.SortBy == "" && q.SortOrder != "" {
		return ErrInvalidDownloadFilter
	}
	for key, values := range q.ColumnFilters {
		kind := downloadColumnKinds[key]
		if kind == "" || len(values) == 0 || kind != "site" && len(values) > 16 {
			return ErrInvalidDownloadFilter
		}
		if kind == "text" && (len(values) != 1 || len(values[0]) > 512) {
			return ErrInvalidDownloadFilter
		}
		if kind == "site" {
			for _, value := range values {
				if len(value) > 512 {
					return ErrInvalidDownloadFilter
				}
			}
		}
		if kind == "enum" {
			allowed := []string{"queued", "searching", "submitted", "downloading", "stalled", "checking", "metadata", "moving", "unknown", "paused", "stopped", "failed", "completed"}
			if key == "seeding" {
				allowed = []string{"completed", "pending", "unknown", "not_required", "not_applicable"}
			}
			for _, v := range values {
				if key == "downloader" {
					// Reuse the supported-client policy; filtering does not constrain the resource protocol.
					if ValidateDownloader(SourceBT, DownloaderKind(v)) != nil {
						return ErrInvalidDownloadFilter
					}
					continue
				}
				if !slices.Contains(allowed, v) {
					return ErrInvalidDownloadFilter
				}
			}
		}
		if kind == "number" || kind == "time" {
			if len(values) != 2 {
				return ErrInvalidDownloadFilter
			}
			var bounds [2]float64
			for i, v := range values {
				if v == "" {
					continue
				}
				n, err := downloadBound(kind, v)
				if err != nil || kind == "number" && n < 0 {
					return ErrInvalidDownloadFilter
				}
				bounds[i] = n
			}
			if values[0] != "" && values[1] != "" && (bounds[0] > bounds[1] || kind == "time" && bounds[0] == bounds[1]) {
				return ErrInvalidDownloadFilter
			}
		}
	}
	return nil
}

func downloadBound(kind, value string) (float64, error) {
	if kind == "time" {
		t, e := time.Parse(time.RFC3339, value)
		return float64(t.Unix()), e
	}
	n, e := strconv.ParseFloat(value, 64)
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, ErrInvalidDownloadFilter
	}
	return n, e
}

func liveDownloadColumn(key string) bool {
	return slices.Contains([]string{"size_bytes", "remaining_bytes", "downloaded_bytes", "download_speed", "upload_speed", "save_path", "share_ratio", "seeding_seconds", "seeding"}, key)
}

// listColumns filters and sorts the full candidate snapshot before slicing the requested page.
// No SQL is built from column input. Live-data failures abort instead of returning misleading totals.
func (s *DownloadService) listColumns(ctx context.Context, page, pageSize int, q ports.DownloadListQuery) (Page[domain.DownloadTask], error) {
	if err := validateDownloadColumns(q); err != nil {
		return Page[domain.DownloadTask]{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	q.All = true
	result, err := s.repository.List(ctx, q)
	if err != nil {
		return Page[domain.DownloadTask]{}, err
	}
	live := liveDownloadColumn(q.SortBy)
	for key := range q.ColumnFilters {
		live = live || liveDownloadColumn(key)
	}
	clients := map[string]ports.DownloadController{}
	if live {
		// Apply persistent-column predicates first to avoid querying unrelated torrents.
		pre := ports.DownloadListQuery{ColumnFilters: map[string][]string{}}
		for key, values := range q.ColumnFilters {
			if !liveDownloadColumn(key) {
				pre.ColumnFilters[key] = values
			}
		}
		result.Items = filterDownloadColumns(result.Items, pre)
		if s.clients != nil {
			clients, err = s.clients(ctx)
			if err != nil {
				return Page[domain.DownloadTask]{}, fmt.Errorf("读取下载器配置失败: %w", err)
			}
		}
		if err = decorateMetrics(ctx, result.Items, clients); err != nil {
			return Page[domain.DownloadTask]{}, err
		}
	}
	items := filterDownloadColumns(result.Items, q)
	total := len(items)
	start := min((page-1)*pageSize, total)
	end := min(start+pageSize, total)
	items = items[start:end]
	if live {
		caps := map[string][]string{}
		for i := range items {
			task := &items[i]
			name := ""
			if task.Downloader != nil {
				name = *task.Downloader
			}
			if _, ok := caps[name]; !ok {
				if client := clients[name]; client != nil {
					caps[name], _ = client.Capabilities(ctx)
				} else {
					caps[name] = nil
				}
			}
			task.AvailableActions = downloadActions(*task, caps[name])
		}
	} else {
		s.decorateActions(ctx, items)
	}
	return Page[domain.DownloadTask]{Page: page, PageSize: pageSize, Total: total, Items: nonNil(items)}, nil
}

type downloadColumnValue struct {
	text   string
	number float64
	valid  bool
}

// downloadValue compares raw byte/second values, never formatted strings; missing values sort last.
func downloadValue(t domain.DownloadTask, key string) downloadColumnValue {
	text := func(p *string) downloadColumnValue {
		if p == nil || *p == "" {
			return downloadColumnValue{}
		}
		return downloadColumnValue{text: strings.ToLower(*p), valid: true}
	}
	integer := func(p *int64) downloadColumnValue {
		if p == nil {
			return downloadColumnValue{}
		}
		return downloadColumnValue{number: float64(*p), valid: true}
	}
	date := func(p *time.Time) downloadColumnValue {
		if p == nil {
			return downloadColumnValue{}
		}
		return downloadColumnValue{number: float64(p.Unix()), valid: true}
	}
	switch key {
	case "code":
		return text(t.Code)
	case "source_site":
		value := downloadSourceSiteValue(t.SourceSite)
		return downloadColumnValue{text: value, valid: value != ""}
	case "downloader":
		return text(t.Downloader)
	case "download_url":
		return text(t.DownloadURL)
	case "error_message":
		return text(t.ErrorMessage)
	case "transfer_status":
		if t.TransferStatus != nil {
			return text(t.TransferStatus)
		}
		v := string(t.Status)
		return text(&v)
	case "added_at":
		return date(t.AddedAt)
	case "completed_at":
		return date(t.CompletedAt)
	case "seeding":
		return text(&t.Seeding.Status)
	}
	m := t.Metrics
	if m == nil {
		return downloadColumnValue{}
	}
	switch key {
	case "size_bytes":
		return integer(m.SizeBytes)
	case "remaining_bytes":
		return integer(m.RemainingBytes)
	case "downloaded_bytes":
		return integer(m.DownloadedBytes)
	case "download_speed":
		return integer(m.DownloadSpeed)
	case "upload_speed":
		return integer(m.UploadSpeed)
	case "seeding_seconds":
		return integer(m.SeedingSeconds)
	case "save_path":
		return text(m.SavePath)
	case "share_ratio":
		if m.ShareRatio != nil {
			return downloadColumnValue{number: *m.ShareRatio, valid: true}
		}
	}
	return downloadColumnValue{}
}

// filterDownloadColumns combines columns with AND and enum choices with OR, preserving stable ID ties.
func filterDownloadColumns(items []domain.DownloadTask, q ports.DownloadListQuery) []domain.DownloadTask {
	out := make([]domain.DownloadTask, 0, len(items))
	for _, task := range items {
		match := true
		for key, values := range q.ColumnFilters {
			v := downloadValue(task, key)
			kind := downloadColumnKinds[key]
			if kind == "site" {
				match = slices.ContainsFunc(values, func(value string) bool { return downloadSourceSiteValue(&value) == v.text })
				if !match {
					break
				}
				continue
			}
			if !v.valid {
				match = false
				break
			}
			switch kind {
			case "text":
				match = strings.Contains(v.text, strings.ToLower(values[0]))
			case "enum":
				match = slices.Contains(values, v.text)
			default:
				if values[0] != "" {
					n, _ := downloadBound(kind, values[0])
					match = v.number >= n
				}
				if match && values[1] != "" {
					n, _ := downloadBound(kind, values[1])
					match = v.number <= n
					if kind == "time" {
						match = v.number < n
					}
				}
			}
			if !match {
				break
			}
		}
		if match {
			out = append(out, task)
		}
	}
	if q.SortBy != "" {
		sort.SliceStable(out, func(i, j int) bool {
			a, b := downloadValue(out[i], q.SortBy), downloadValue(out[j], q.SortBy)
			if a.valid != b.valid {
				return a.valid
			}
			if !a.valid {
				return out[i].ID < out[j].ID
			}
			cmp := strings.Compare(a.text, b.text)
			if kind := downloadColumnKinds[q.SortBy]; kind == "number" || kind == "time" {
				cmp = 0
				if a.number < b.number {
					cmp = -1
				} else if a.number > b.number {
					cmp = 1
				}
			}
			if cmp == 0 {
				return out[i].ID < out[j].ID
			}
			if q.SortOrder == "desc" {
				return cmp > 0
			}
			return cmp < 0
		})
	}
	return out
}
