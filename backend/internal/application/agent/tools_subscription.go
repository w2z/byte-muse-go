package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

const (
	// subscriptionResultLimit 是订阅类列表回填模型的最大条数。
	subscriptionResultLimit = 50
	// dateLayout 是追新起始日期的统一格式，与演员、标签订阅的存储口径一致。
	dateLayout = "2006-01-02"
)

// splitSubscribeCodes 拆分多番号输入；空格不参与拆分，避免把「SSIS 001」这类容错写法拆坏。
func splitSubscribeCodes(raw string) []string {
	fields := strings.FieldsFunc(raw, func(char rune) bool {
		switch char {
		case ',', '，', '、', ';', '；', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	})
	codes := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			codes = append(codes, trimmed)
		}
	}
	return codes
}

// addSubscribe 按番号逐个创建订阅；每条结果独立返回，避免一个失败掩盖其余成功。
func addSubscribe(ctx context.Context, deps Deps, raw string) (any, error) {
	codes := splitSubscribeCodes(raw)
	if len(codes) == 0 {
		return "请提供至少一个番号，多个番号用逗号分隔", nil
	}
	lines := make([]string, 0, len(codes))
	for _, code := range codes {
		lines = append(lines, SubscribeByCode(ctx, deps, code))
	}
	return strings.Join(lines, "\n"), nil
}

// cancelSubscribe 取消某个番号的有效订阅；订阅状态转换仍由 SubscriptionService 负责。
func cancelSubscribe(ctx context.Context, deps Deps, raw string) (any, error) {
	code := NormalizeCode(raw)
	if code == "" {
		return "请提供要取消的番号，例如 SSIS-001", nil
	}
	if deps.Subscriptions == nil {
		return "订阅服务不可用", nil
	}
	page, err := deps.Subscriptions.List(ctx, 1, ports.MaxPageSize, domain.SubscriptionStatusActive)
	if err != nil {
		return nil, fmt.Errorf("查询订阅失败: %w", err)
	}
	for _, item := range page.Items {
		if item.Media == nil || NormalizeCode(item.Media.Code) != code {
			continue
		}
		if _, err := deps.Subscriptions.Cancel(ctx, item.ID); err != nil {
			return nil, fmt.Errorf("取消订阅失败: %w", err)
		}
		return "番号 " + code + " 的订阅已取消", nil
	}
	return "番号 " + code + " 没有有效订阅", nil
}

// subscriptionCodeTitle 取订阅对应影片的番号与标题；标题优先使用译文。
func subscriptionCodeTitle(item domain.Subscription) (string, string) {
	if item.Media == nil {
		return "", ""
	}
	code := strings.TrimSpace(item.Media.Code)
	title := strings.TrimSpace(item.Media.Title)
	if item.Media.TranslatedTitle != nil && strings.TrimSpace(*item.Media.TranslatedTitle) != "" {
		title = strings.TrimSpace(*item.Media.TranslatedTitle)
	}
	return code, title
}

// querySubscribes 列出当前有效影片订阅。
func querySubscribes(ctx context.Context, deps Deps) (any, error) {
	if deps.Subscriptions == nil {
		return "订阅服务不可用", nil
	}
	page, err := deps.Subscriptions.List(ctx, 1, subscriptionResultLimit, domain.SubscriptionStatusActive)
	if err != nil {
		return nil, fmt.Errorf("查询订阅失败: %w", err)
	}
	if len(page.Items) == 0 {
		return "当前没有有效订阅", nil
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		code, title := subscriptionCodeTitle(item)
		items = append(items, map[string]any{
			"code":       code,
			"title":      title,
			"mode":       string(item.Mode),
			"created_at": item.CreatedAt,
		})
	}
	return map[string]any{"total": page.Total, "returned": len(items), "items": items}, nil
}

// addActorSubscribe 保存演员追新起始日期；空日期按当天计算。
func addActorSubscribe(ctx context.Context, deps Deps, rawName, rawDate string) (any, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "请提供演员名称", nil
	}
	if deps.Actors == nil {
		return "演员服务不可用", nil
	}
	limitDate := normalizeLimitDate(rawDate)
	item, err := deps.Actors.SaveSubscription(ctx, name, limitDate)
	switch {
	case err == nil:
		display := item.Name
		if strings.TrimSpace(display) == "" {
			display = name
		}
		return "演员 " + display + " 已订阅，追新起始日期 " + limitDate, nil
	case errors.Is(err, application.ErrInvalidActorLimitDate):
		return "日期格式无效，请使用 YYYY-MM-DD", nil
	case errors.Is(err, ports.ErrActorNotFound):
		return "未找到演员: " + name, nil
	default:
		return nil, fmt.Errorf("保存演员订阅失败: %w", err)
	}
}

// cancelActorSubscribe 取消演员订阅，只清除追新日期。
func cancelActorSubscribe(ctx context.Context, deps Deps, rawName string) (any, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "请提供演员名称", nil
	}
	if deps.Actors == nil {
		return "演员服务不可用", nil
	}
	item, err := deps.Actors.CancelSubscription(ctx, name)
	switch {
	case err == nil:
		display := item.Name
		if strings.TrimSpace(display) == "" {
			display = name
		}
		return "演员 " + display + " 的订阅已取消", nil
	case errors.Is(err, ports.ErrActorNotFound):
		return "未找到演员: " + name, nil
	default:
		return nil, fmt.Errorf("取消演员订阅失败: %w", err)
	}
}

// queryActorSubscribes 列出已订阅演员；仓储的订阅状态取值是 active（limit_date 非空）。
func queryActorSubscribes(ctx context.Context, deps Deps) (any, error) {
	if deps.Actors == nil {
		return "演员服务不可用", nil
	}
	page, err := deps.Actors.List(ctx, 1, subscriptionResultLimit, "active", "")
	if err != nil {
		return nil, fmt.Errorf("查询演员订阅失败: %w", err)
	}
	if len(page.Items) == 0 {
		return "当前没有订阅中的演员", nil
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{"name": item.Name, "photo": item.Photo, "limit_date": item.LimitDate})
	}
	return map[string]any{"total": page.Total, "returned": len(items), "items": items}, nil
}

// addTagSubscribe 保存标签订阅规则并触发一次追新；追新失败时规则已保存，如实说明。
func addTagSubscribe(ctx context.Context, deps Deps, rawName, rawDate string) (any, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "请提供标签名称，先用 query_tags 确认真实标签名", nil
	}
	if deps.Tags == nil {
		return "标签服务不可用", nil
	}
	limitDate := normalizeLimitDate(rawDate)
	item, err := deps.Tags.SaveSubscription(ctx, name, limitDate)
	display := strings.TrimSpace(item.Name)
	if display == "" {
		display = name
	}
	switch {
	case err == nil:
		return "标签 " + display + " 已订阅，追新起始日期 " + limitDate, nil
	case errors.Is(err, application.ErrTagFollowPending):
		return "标签 " + display + " 的订阅规则已保存，追新未完成，将由定时任务继续处理", nil
	case errors.Is(err, ports.ErrTagNotFound):
		return "未找到标签: " + name, nil
	case errors.Is(err, application.ErrInvalidTagRule):
		return "日期格式无效，请使用 YYYY-MM-DD", nil
	default:
		return nil, fmt.Errorf("保存标签订阅失败: %w", err)
	}
}

// cancelTagSubscribe 取消标签追新，不取消该标签下已有的影片订阅。
func cancelTagSubscribe(ctx context.Context, deps Deps, rawName string) (any, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "请提供标签名称", nil
	}
	if deps.Tags == nil {
		return "标签服务不可用", nil
	}
	item, err := deps.Tags.CancelSubscription(ctx, name)
	display := strings.TrimSpace(item.Name)
	if display == "" {
		display = name
	}
	switch {
	case err == nil:
		return "标签 " + display + " 的追新已取消，已有影片订阅不受影响", nil
	case errors.Is(err, ports.ErrTagNotFound):
		return "未找到标签: " + name, nil
	default:
		return nil, fmt.Errorf("取消标签订阅失败: %w", err)
	}
}

// queryTagSubscribes 列出已订阅标签；仓储的订阅状态取值是 active（limit_date 非空）。
func queryTagSubscribes(ctx context.Context, deps Deps) (any, error) {
	if deps.Tags == nil {
		return "标签服务不可用", nil
	}
	page, err := deps.Tags.List(ctx, 1, subscriptionResultLimit, "", "active")
	if err != nil {
		return nil, fmt.Errorf("查询标签订阅失败: %w", err)
	}
	if len(page.Items) == 0 {
		return "当前没有订阅中的标签", nil
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{
			"name":        item.Name,
			"media_count": item.MediaCount,
			"category":    item.Category,
			"limit_date":  item.LimitDate,
		})
	}
	return map[string]any{"total": page.Total, "returned": len(items), "items": items}, nil
}

// downloadSubscribe 触发一次订阅下载受理，只报告受理条数，不承诺下载完成。
func downloadSubscribe(ctx context.Context, deps Deps) (any, error) {
	if deps.SubscriptionDownloads == nil {
		return "订阅下载服务不可用", nil
	}
	count, err := deps.SubscriptionDownloads.RunActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("订阅下载执行失败: %w", err)
	}
	if count == 0 {
		return "没有需要下载的订阅", nil
	}
	return fmt.Sprintf("已提交 %d 条订阅下载，请在下载页查看进度", count), nil
}
