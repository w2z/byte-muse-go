package agent

import (
	"context"
	"strings"
	"unicode"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
)

// 订阅来源标识写入订阅过滤条件，便于后台区分人工、渠道与 Agent 发起的订阅。
const subscriptionSourceKey = "source"

// NormalizeCode 归一化番号：去空白、转大写、统一用短横线连接。
// 渠道文本、工具参数和斜杠命令都必须经过这里，避免同一番号出现多种写法。
func NormalizeCode(raw string) string {
	upper := strings.ToUpper(strings.TrimSpace(raw))
	var builder strings.Builder
	previousDash := false
	for _, char := range upper {
		switch {
		case char == '-' || char == '_' || unicode.IsSpace(char):
			if builder.Len() > 0 && !previousDash {
				builder.WriteRune('-')
				previousDash = true
			}
		default:
			builder.WriteRune(char)
			previousDash = false
		}
	}
	return strings.Trim(builder.String(), "-")
}

// LooksLikeCode 判断一段文本是否像番号：纯 ASCII、含数字且长度合理。
// 用于 Agent 未启用时保留「直接发送番号即订阅」的原有行为。
func LooksLikeCode(raw string) bool {
	text := strings.TrimSpace(raw)
	if len(text) < 3 || len(text) > 24 {
		return false
	}
	hasDigit := false
	for _, char := range text {
		if char > unicode.MaxASCII {
			return false
		}
		if unicode.IsDigit(char) {
			hasDigit = true
			continue
		}
		if !unicode.IsLetter(char) && char != '-' && char != '_' && char != ' ' {
			return false
		}
	}
	return hasDigit && NormalizeCode(text) != ""
}

// SubscribeByCode 按番号创建订阅并返回面向用户的单行结果。
// 订阅受理与下载是两个独立状态：这里只受理订阅，不承诺已开始下载。
func SubscribeByCode(ctx context.Context, deps Deps, code string) string {
	normalized := NormalizeCode(code)
	if normalized == "" {
		return "番号无效，请发送类似 SSIS-001 的番号"
	}
	if deps.Subscriptions == nil {
		return "订阅服务不可用"
	}
	media, ok := lookupMedia(ctx, deps, normalized)
	if !ok {
		return "番号 " + normalized + " 不在媒体库中，请先在采集页入库后再订阅"
	}
	if media.SubscriptionStatus == domain.SubscriptionStatusActive {
		return "番号 " + normalized + " 已在订阅中"
	}
	_, _, err := deps.Subscriptions.Create(ctx, application.CreateSubscriptionCommand{
		IdempotencyKey: "channel:" + normalized,
		MediaID:        media.ID,
		Mode:           domain.SubscriptionModeStrict,
		Filter:         map[string]any{subscriptionSourceKey: "channel"},
		Label:          normalized,
	})
	if err != nil {
		return "番号 " + normalized + " 订阅失败: " + err.Error()
	}
	title := media.Title
	if media.TranslatedTitle != nil && strings.TrimSpace(*media.TranslatedTitle) != "" {
		title = *media.TranslatedTitle
	}
	return "番号 " + normalized + " 已加入订阅。" + strings.TrimSpace(title)
}

// lookupMedia 在本地目录中按番号精确定位影片。
func lookupMedia(ctx context.Context, deps Deps, code string) (domain.Media, bool) {
	if deps.Queries == nil {
		return domain.Media{}, false
	}
	page, err := deps.Queries.Search(ctx, code, 1, 20)
	if err != nil {
		return domain.Media{}, false
	}
	for _, item := range page.Items {
		if NormalizeCode(item.Code) == code {
			return item, true
		}
	}
	return domain.Media{}, false
}
