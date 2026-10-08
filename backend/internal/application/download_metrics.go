package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"strings"
	"time"
)

// decorateMetrics 按下载器批量读取当前页，不逐行请求，不将读取失败伪装为零。
func decorateMetrics(ctx context.Context, items []domain.DownloadTask, clients map[string]ports.DownloadController) {
	groups := map[string][]string{}
	for _, task := range items {
		if task.Downloader != nil && task.InfoHash != nil && *task.InfoHash != "" {
			groups[*task.Downloader] = append(groups[*task.Downloader], *task.InfoHash)
		}
	}
	snapshots := map[string]map[string]*domain.DownloadMetrics{}
	for name, hashes := range groups {
		if reader, ok := clients[name].(ports.DownloadMetricsReader); ok {
			if metrics, err := reader.ReadMetrics(ctx, hashes); err == nil {
				snapshots[name] = metrics
			}
		}
	}
	now := time.Now().UTC()
	for i := range items {
		task := &items[i]
		if task.Downloader != nil && task.InfoHash != nil {
			task.Metrics = snapshots[*task.Downloader][strings.ToLower(*task.InfoHash)]
		}
		task.Seeding = assessSeeding(*task, task.Metrics, now)
	}
}

// assessSeeding 统一执行现有站点规则。下载器无法证明期限内历史做种，过期后保持待确认。
// 无 H&R 要求不同于已完成做种；没有站点核验时结果始终只是下载器估算。
func assessSeeding(task domain.DownloadTask, metrics *domain.DownloadMetrics, now time.Time) domain.SeedingAssessment {
	result := domain.SeedingAssessment{Status: "not_applicable", Rule: "非 PT 任务，不适用站点做种要求"}
	if task.SourceKind == nil || *task.SourceKind != "pt" {
		return result
	}
	result = domain.SeedingAssessment{Status: "unknown", Rule: "未配置该站点做种规则，需到站点确认"}
	if task.SourceSite == nil {
		return result
	}
	var seconds int64
	var ratio float64
	var days int
	switch strings.ToLower(strings.TrimSpace(*task.SourceSite)) {
	case "馒头", "mteam", "m-team", "pttime":
		return domain.SeedingAssessment{Status: "not_required", Rule: "无 H&R 要求"}
	case "ptfans":
		seconds, days = 7*86400, 35
		result.Rule = "35 天内累计做种 7 天"
	case "rousipro":
		seconds, ratio = 86400, 1
		result.Rule = "累计做种 ≥24 小时或单种分享率 ≥1.0"
	case "nicept":
		seconds, ratio, days = 3*86400, 2, 12
		result.Rule = "12 天内累计做种 3 天或单种分享率 ≥2.0"
	default:
		return result
	}
	result.Rule += "；依据下载器估算，以站点认定为准"
	if metrics == nil {
		return result
	}
	if !metrics.Complete {
		result.Status = "pending"
		return result
	}
	// 未知期限起点或期限已过，累计值无法证明达标发生在期限之内。
	if days > 0 && (metrics.AddedAt == nil || metrics.AddedAt.After(now) || now.After(metrics.AddedAt.AddDate(0, 0, days))) {
		result.Rule += "；无法核实期限内做种记录"
		return result
	}
	timeMet := metrics.SeedingSeconds != nil && *metrics.SeedingSeconds >= seconds
	ratioMet := ratio > 0 && metrics.ShareRatio != nil && *metrics.ShareRatio >= ratio
	if timeMet || ratioMet {
		result.Status = "completed"
	} else if metrics.SeedingSeconds != nil && (ratio == 0 || metrics.ShareRatio != nil) {
		result.Status = "pending"
	}
	return result
}
