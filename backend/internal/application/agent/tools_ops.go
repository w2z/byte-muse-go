package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
	"bytemuse/backend/internal/scheduler"
)

const (
	// healthErrorLimit 是健康自检附带错误日志的条数。
	healthErrorLimit = 5
)

// downloadStatusFilter 收敛下载状态参数；其他取值按 all 处理，与工具描述一致。
func downloadStatusFilter(raw string) (string, domain.DownloadStatus) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "downloading":
		return "downloading", domain.DownloadStatusDownloading
	case "completed":
		return "completed", domain.DownloadStatusCompleted
	default:
		return "all", ""
	}
}

// downloadTaskBrief 输出一条下载任务的关键字段；缺失值保持 null。
func downloadTaskBrief(task domain.DownloadTask) map[string]any {
	return map[string]any{
		"code":            task.Code,
		"status":          string(task.Status),
		"transfer_status": task.TransferStatus,
		"source_site":     task.SourceSite,
		"downloader":      task.Downloader,
		"added_at":        task.AddedAt,
		"completed_at":    task.CompletedAt,
		"error_message":   task.ErrorMessage,
	}
}

// queryDownloadTasks 在仓储计数与分页前按关联影片过滤。
func queryDownloadTasks(ctx context.Context, deps Deps, rawKeyword, rawStatus string, paging toolPage) (any, error) {
	if deps.Downloads == nil {
		return "下载服务不可用", nil
	}
	keyword := strings.TrimSpace(rawKeyword)
	statusKey, status := downloadStatusFilter(rawStatus)
	page, err := deps.Downloads.ListFiltered(ctx, paging.number, paging.size, ports.DownloadListQuery{Status: status, MediaSearch: keyword})
	if err != nil {
		return nil, fmt.Errorf("查询下载任务失败: %w", err)
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, task := range page.Items {
		items = append(items, downloadTaskBrief(task))
	}
	result := pageResult(paging, page.Total, items)
	result["status"] = statusKey
	if keyword != "" {
		result["keyword"] = keyword
	}
	if len(items) == 0 {
		result["message"] = "没有符合条件的下载任务"
	}
	return result, nil
}

// queryLogs 查询后台日志；级别非法时按“不按级别过滤”处理。
func queryLogs(ctx context.Context, deps Deps, rawKeyword, rawLevel string, paging toolPage) (any, error) {
	keyword := strings.TrimSpace(rawKeyword)
	level := logging.Level(strings.ToLower(strings.TrimSpace(rawLevel)))
	if !level.Valid() {
		level = ""
	}
	records, total, err := deps.Logs.Search(ctx, logging.Query{Level: level, Keyword: keyword, Page: paging.number, PageSize: paging.size})
	if err != nil {
		return nil, fmt.Errorf("查询日志失败: %w", err)
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, map[string]any{
			"time":     record.Time,
			"level":    string(record.Level),
			"category": string(record.Category),
			"message":  record.Message,
		})
	}
	result := pageResult(paging, total, items)
	if level != "" {
		result["level"] = string(level)
	}
	if keyword != "" {
		result["keyword"] = keyword
	}
	return result, nil
}

// querySchedulers 列出定时任务及其 cron 表达式与最近执行状态。
func querySchedulers(_ context.Context, deps Deps, paging toolPage) (any, error) {
	if deps.Scheduler == nil {
		return "调度器不可用", nil
	}
	tasks := deps.Scheduler.Tasks()
	window := pageWindow(tasks, paging)
	items := make([]map[string]any, 0, len(window))
	for _, task := range window {
		items = append(items, map[string]any{
			"name":     task.Name,
			"cron":     task.Spec,
			"last_run": task.LastRun,
			"running":  task.Running,
		})
	}
	return pageResult(paging, len(tasks), items), nil
}

// runCronJob 立即触发一个已配置的定时任务；触发是异步的，只报告是否受理。
func runCronJob(_ context.Context, deps Deps, rawName string) (any, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "请提供定时任务名称，可用任务见 query_schedulers", nil
	}
	if deps.Scheduler == nil {
		return "调度器不可用", nil
	}
	err := deps.Scheduler.RunNow(name)
	switch {
	case err == nil:
		return "已触发定时任务: " + name + "（异步执行，可在任务列表查看最近执行时间）", nil
	case errors.Is(err, scheduler.ErrTaskNotFound):
		return "未知定时任务: " + name + "，可用任务见 query_schedulers", nil
	default:
		return "定时任务触发失败: " + err.Error(), nil
	}
}

// queryHealth 汇总版本、渠道与能力配置状态，并附带最近错误日志。
// 只回答“是否已接入”，Deps 不持有任何凭据，因此绝不会回显密钥或令牌。
func queryHealth(ctx context.Context, deps Deps) (any, error) {
	result := map[string]any{
		"version": versionOf(deps),
		"channels": map[string]string{
			"telegram": capabilityLabel(channelConfigured(deps, ports.ChannelTelegram)),
			"wechat":   capabilityLabel(channelConfigured(deps, ports.ChannelWeChat)),
		},
		"capabilities": map[string]string{
			"影片查询": capabilityLabel(deps.Queries != nil),
			"演员订阅": capabilityLabel(deps.Actors != nil),
			"标签订阅": capabilityLabel(deps.Tags != nil),
			"订阅管理": capabilityLabel(deps.Subscriptions != nil),
			"订阅下载": capabilityLabel(deps.SubscriptionDownloads != nil),
			"下载任务": capabilityLabel(deps.Downloads != nil),
			"资源搜索": capabilityLabel(deps.Torrents != nil),
			"媒体库":  capabilityLabel(deps.Catalog != nil),
			"调度器":  capabilityLabel(deps.Scheduler != nil),
			"日志":   capabilityLabel(deps.Logs != nil),
		},
		"openai": "工具上下文不包含 Agent 配置，请在设置页确认「对话 Agent」的启用状态、接口地址与模型",
	}
	records, _, err := deps.Logs.Search(ctx, logging.Query{Level: logging.LevelError, Page: 1, PageSize: healthErrorLimit})
	if err != nil {
		return nil, fmt.Errorf("查询错误日志失败: %w", err)
	}
	if len(records) > 0 {
		items := make([]map[string]any, 0, len(records))
		for _, record := range records {
			items = append(items, map[string]any{
				"time":     record.Time,
				"level":    string(record.Level),
				"category": string(record.Category),
				"message":  record.Message,
			})
		}
		result["recent_errors"] = items
	}
	return result, nil
}

// channelConfigured 判断渠道是否已注册发送器；Deps 不暴露凭据，只回答可用性。
func channelConfigured(deps Deps, channel string) bool {
	if deps.Channels == nil {
		return false
	}
	_, ok := deps.Channels.Sender(channel)
	return ok
}

// capabilityLabel 把可用性转成中文摘要，避免暴露配置细节。
func capabilityLabel(available bool) string {
	if available {
		return "已配置"
	}
	return "未配置"
}

// getVersion 返回运行版本，未注入时回退 dev。
func getVersion(_ context.Context, deps Deps) (any, error) {
	return versionOf(deps), nil
}

// sendMessage 在当前对话渠道追加发送一条纯文本消息；不发送图片或文件。
func sendMessage(ctx context.Context, deps Deps, rawMessage string) (any, error) {
	message := strings.TrimSpace(rawMessage)
	if message == "" {
		return "请提供要发送的内容", nil
	}
	msg, ok := MessageFromContext(ctx)
	if !ok || deps.Channels == nil {
		return "当前没有可回复的渠道", nil
	}
	sender, ok := deps.Channels.Sender(msg.Channel)
	if !ok {
		return "当前没有可回复的渠道", nil
	}
	if err := sender.SendText(ctx, msg.ChatID, message); err != nil {
		return nil, fmt.Errorf("发送消息失败: %w", err)
	}
	return "消息已发送", nil
}
