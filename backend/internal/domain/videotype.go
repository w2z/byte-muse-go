package domain

import (
	"regexp"
	"strings"
)

// 影片类型取值。与 media.video_type 的 CHECK 约束、OpenAPI 的 VideoType 枚举一一对应。
const (
	VideoTypeCensored          = "censored"           // 有码
	VideoTypeUncensored        = "uncensored"         // 无码
	VideoTypeUncensoredCracked = "uncensored_cracked" // 无码破解
	VideoTypeLeaked            = "leaked"             // 流出
)

// videoTypeKeywordRules 是显式证据规则，按优先级从高到低排列，先命中先返回。
// 「无码破解」「无码流出」同时包含「无码」，因此更具体的规则必须排在「无码」之前。
var videoTypeKeywordRules = []struct {
	value    string
	keywords []string
}{
	{VideoTypeLeaked, []string{"流出", "leak"}},
	{VideoTypeUncensoredCracked, []string{"破解", "crack"}},
	{VideoTypeUncensored, []string{"无码", "無碼", "无修正", "無修正", "步兵", "uncensored"}},
	{VideoTypeCensored, []string{"有码", "有碼", "censored"}},
}

// uncensoredCodePrefixes 收录长期只发行无码作品的番号前缀（规范化后为「大写、仅保留字母数字」）。
// 这些系列由固定的无码片商运营，命中即判为无码；有码片商的番号一律不在此列，避免把有码作品误判。
var uncensoredCodePrefixes = []string{
	"HEYZO", "SIRO", "1PONDO", "10MUSUME", "CARIBBEANCOM", "PACOPACOMAMA", "HEYDOUGA", "MURAKAMI",
}

// tokyoHotCodePattern 匹配东京热（Tokyo Hot）番号 n1234 / N-1234 形态。
var tokyoHotCodePattern = regexp.MustCompile(`^N-?\d{3,4}$`)

// codeNormalizer 去掉番号中的分隔符，用于前缀比对。
var codeNormalizer = strings.NewReplacer("-", "", "_", "", " ", "", ".", "")

// ClassifyVideoType 判定影片类型，规则按可信度从高到低：
//  1. 类别标签中出现明确类型词（流出 / 无码破解 / 无码 / 有码）；
//  2. 标题中出现明确类型词；
//  3. 番号属于长期只发行无码作品的系列；
//  4. 其余带番号的作品按行业常态归为有码。
//
// 既无明确证据、也没有番号时返回空字符串，由调用方保存 NULL（未分类），不使用猜测值。
func ClassifyVideoType(code, title string, tags []string) string {
	for _, rule := range videoTypeKeywordRules {
		for _, tag := range tags {
			if containsVideoTypeKeyword(tag, rule.keywords) {
				return rule.value
			}
		}
	}
	for _, rule := range videoTypeKeywordRules {
		if containsVideoTypeKeyword(title, rule.keywords) {
			return rule.value
		}
	}
	normalized := strings.ToUpper(codeNormalizer.Replace(strings.TrimSpace(code)))
	if normalized == "" {
		return ""
	}
	if tokyoHotCodePattern.MatchString(normalized) {
		return VideoTypeUncensored
	}
	for _, prefix := range uncensoredCodePrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return VideoTypeUncensored
		}
	}
	return VideoTypeCensored
}

func containsVideoTypeKeyword(value string, keywords []string) bool {
	lowered := strings.ToLower(value)
	for _, keyword := range keywords {
		if strings.Contains(lowered, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
