package domain

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// NormalizeCode 归一化番号：去空白、转大写、统一用短横线连接。
// 渠道文本、工具参数、文件名扫描与斜杠命令都必须经过这里，避免同一番号出现多种写法。
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

// 从文件名识别番号的候选规则。番号形态是「标签 + 数字」，标签与数字之间可能带分隔符，也可能直接相连：
//   - 带分隔符：SSIS-001、ABP_123、heyzo 1234、1pondo-123456；
//   - 直接相连：SSIS001、ABP123；
//   - 东京热：n1234、N-1234。
//
// 标签至少 2 个字符、数字 2 到 6 位，避免把 4K、HDR 这类短标记当成番号。
var (
	codeSeparatedPattern = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])([0-9a-z]{2,15})[-_ ](\d{2,6})(?:[^0-9]|$)`)
	codeJoinedPattern    = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])([a-z]{2,15})(\d{2,6})(?:[^0-9]|$)`)
	tokyoHotNamePattern  = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])n[-_ ]?(\d{3,4})(?:[^0-9]|$)`)
)

// codeNoiseLabels 是文件名里常见的非番号标签：分辨率、编码、来源、容器、字幕等。
// 命中即跳过该候选并继续向后寻找，避免把 HDTV-1080 这类组合当成番号。
var codeNoiseLabels = map[string]bool{
	"HDTV": true, "WEB": true, "WEBDL": true, "WEBRIP": true, "BLURAY": true, "BDRIP": true,
	"BRRIP": true, "DVDRIP": true, "HDRIP": true, "REMUX": true, "HD": true, "SD": true,
	"FHD": true, "UHD": true, "X264": true, "X265": true, "H264": true, "H265": true,
	"HEVC": true, "AVC": true, "MPEG": true, "MPEG2": true, "MPEG4": true, "XVID": true,
	"DIVX": true, "VP9": true, "AV1": true, "AAC": true, "AC3": true, "EAC3": true,
	"DTS": true, "DDP": true, "TRUEHD": true, "FLAC": true, "HDR": true, "HDR10": true,
	"SDR": true, "HLG": true, "DOLBY": true, "ATMOS": true, "10BIT": true, "8BIT": true,
	"CHS": true, "CHT": true, "JPN": true, "ENG": true, "SUB": true, "SUBS": true,
	"REPACK": true, "PROPER": true, "EXTENDED": true, "UNRATED": true, "COMPLETE": true,
	"PART": true, "DISC": true, "MULTI": true, "UNCENSORED": true, "CENSORED": true,
	"LEAK": true, "LEAKED": true, "FIX": true,
}

// codeNoiseDigits 是常见的分辨率数字：它们是画质标记，不是番号序号。
var codeNoiseDigits = map[string]bool{
	"360": true, "480": true, "540": true, "576": true, "720": true, "1080": true,
	"1440": true, "2160": true, "4320": true,
}

// ExtractCode 从文件名或标题中识别番号并返回归一化结果；识别不到返回空串。
//
// 判定取「最先出现的合法候选」，而不是按规则优先级，保证 SSIS-001-CD1.mp4 得到 SSIS-001 而不是 CD1；
// 分辨率数字、发行年份与常见编码标签会被跳过，继续向后寻找真正的番号。
// 文件扩展名不参与裁剪：常见视频扩展名本身不含「字母+数字」形态，不会产生候选。
// 调用方必须把空串当作「无法识别」并显式跳过，不能凭空生成番号。
func ExtractCode(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	best := codeCandidate{}
	for _, pattern := range []*regexp.Regexp{codeSeparatedPattern, codeJoinedPattern} {
		for _, match := range pattern.FindAllStringSubmatchIndex(text, -1) {
			best = pickCodeCandidate(best, match[0], text[match[2]:match[3]], text[match[4]:match[5]])
		}
	}
	for _, match := range tokyoHotNamePattern.FindAllStringSubmatchIndex(text, -1) {
		best = pickCodeCandidate(best, match[0], "N", text[match[2]:match[3]])
	}
	if best.label == "" {
		return ""
	}
	return NormalizeCode(best.label + "-" + best.digits)
}

// codeCandidate 是文件名中的一个番号候选；start 用于在多个候选之间取最先出现的那个。
type codeCandidate struct {
	start  int
	label  string
	digits string
}

// pickCodeCandidate 保留位置更靠前、且标签与序号都可信的候选。
func pickCodeCandidate(current codeCandidate, start int, label, digits string) codeCandidate {
	if current.label != "" && current.start <= start {
		return current
	}
	if !plausibleCodeLabel(label) || codeNoiseDigits[digits] || looksLikeYear(digits) {
		return current
	}
	return codeCandidate{start: start, label: label, digits: digits}
}

// plausibleCodeLabel 判断标签是否可能是番号前缀：至少包含一个字母，且不是常见编码、来源与容器标签。
func plausibleCodeLabel(label string) bool {
	upper := strings.ToUpper(strings.TrimSpace(label))
	if upper == "" || codeNoiseLabels[upper] {
		return false
	}
	for _, char := range upper {
		if char >= 'A' && char <= 'Z' {
			return true
		}
	}
	return false
}

// looksLikeYear 判断序号是否像发行年份（1900-2099 的四位数）；年份属于发行信息，不是番号序号。
func looksLikeYear(digits string) bool {
	if len(digits) != 4 {
		return false
	}
	value, err := strconv.Atoi(digits)
	if err != nil {
		return false
	}
	return value >= 1900 && value <= 2099
}
