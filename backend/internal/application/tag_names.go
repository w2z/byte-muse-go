package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// tagTranslationPrompt 是标签归一化专用提示词：只做字符转换，把标签统一成简体中文。
// 标签是短词而非句子，与影片标题提示词分开维护，避免标题提示词把标签扩写成描述。
// 本地小模型在「翻译」措辞下会改写已经是简体的标签（如 乳交→亲密接触），因此这里明确限定为转换，
// 并给出少量示例锚定高频标签的通行中文写法，让英文、日文标签收敛到字典里已有的简体标签名。
const tagTranslationPrompt = `你是影视标签的繁简转换工具。只做字符转换，禁止改写、禁止同义替换、禁止扩写。
规则：
1. 输入已经是简体中文时必须原样返回，一个字都不能改。
2. 繁体字换成对应简体字；日文假名、日文汉字、英文词换成通行的中文标签词。
3. 不改变词序和词数，不加修饰、不加解释。
4. 数字与字母缩写原样保留，例如 4K、VR、OL、SM、FHD、R-15。
5. 只输出结果本身，不要引号、句号、换行或任何说明。
繁简转换示例：
中出 -> 中出
乳交 -> 乳交
多P -> 多P
女上位 -> 女上位
素人作品 -> 素人作品
單體作品 -> 单体作品
介紹影片 -> 介绍影片
高畫質 -> 高画质
已婚婦女 -> 已婚妇女
企畫 -> 企画
亂倫 -> 乱伦
裸體圍裙 -> 裸体围裙
賽車女郎 -> 赛车女郎
主觀視角 -> 主观视角
日文与英文示例：
Big Tits -> 巨乳
Huge Butt -> 大屁股
Breasts -> 美乳
Butt -> 美臀
Squirting -> 潮吹
Facials -> 颜射
Slender -> 苗条
Older Sister -> 姐姐
Married Woman -> 已婚妇女
Slut -> 荡妇
Titty Fuck -> 乳交
Blowjob -> 口交
Handjob -> 手淫
Creampie -> 中出
Amateur -> 业余
Solowork -> 单体作品
Omnibus -> 综合短篇
Documentary -> 纪录片
POV -> 主观视角
Deep Throating -> 深喉
Kiss -> 接吻
Shaved -> 剃毛
Cuckold -> 出轨
Promiscuity -> 滥交
Cowgirl -> 女上位
Best -> 精选综合
Beautiful Girl -> 美少女
Ultra-Huge Tits -> 超乳
Over 4 hours -> 4小时以上
ハイクオリティVR -> 高画质VR
フルハイビジョン(FHD) -> 全高清(FHD)
お風呂 -> 浴室
VR専用 -> VR专用
Fカップ -> F杯
初音ミク -> 初音未来`

// maxTagNameRunes 是权威标签名的长度上限，超过说明模型夹带了说明而非标签。
const maxTagNameRunes = 40

// tagConversionPrompt 用于纯汉字标签：这类标签只需要逐字繁简转换，不涉及跨语言翻译。
// 用翻译提示词处理纯汉字标签时，本地小模型会顺手改写用词（乳交→亲密接触），因此单独走逐字转换提示词。
// 示例同时锚定「已是简体时原样返回」，实测 75 个样本无长度失配、无改写。
const tagConversionPrompt = `把输入的标签逐字转换成简体字，一个字对一个字，字数必须完全相同。不许改写、不许换词、不许增删、不许解释。只输出转换后的标签。
示例：
給女性觀眾 -> 给女性观众
子宮頸 -> 子宫颈
白人女優 -> 白人女优
車掌小姐 -> 车掌小姐
近親相姦 -> 近亲相奸
單體作品 -> 单体作品
高畫質 -> 高画质
中出 -> 中出
乳交 -> 乳交
多P -> 多P
女上位 -> 女上位
素人作品 -> 素人作品`

// tagProbeText 是翻译引擎连通性探针，本身就是简体中文，正确结果必须原样返回。
const tagProbeText = "制服"

// TagNameService 是「来源标签 → 库内权威标签名」的唯一实现。
// 先查库（字典与别名），确认不存在才翻译成简体中文并登记，避免同一标签的多种写法继续入库。
type TagNameService struct {
	dictionary ports.TagNameDictionary
	translator TranslationClient
}

// NewTagNameService 绑定标签字典与可选翻译器；translator 为 nil 表示翻译未启用。
func NewTagNameService(dictionary ports.TagNameDictionary, translator TranslationClient) *TagNameService {
	return &TagNameService{dictionary: dictionary, translator: translator}
}

// Resolve 逐个归一来源标签名，返回「原始值 → 权威名」映射，供采集入库使用。
// 字典命中即视为标签已存在，直接复用；只有库内完全没有的标签才翻译成简体中文并登记。
// 翻译失败或译文无效时保留规范化后的原文，保证单个标签问题不阻断整批入库。
func (s *TagNameService) Resolve(ctx context.Context, names []string) (map[string]string, error) {
	return s.resolveNames(ctx, names, false)
}

// Normalize 逐个归一库内既有标签名，返回「原始值 → 权威名」映射，供历史标签一次性收敛使用。
// 与 Resolve 的唯一差别是字典里的既有名称同样要翻译：对标站字典本身是繁体，
// 若沿用「字典命中即视为已存在」，繁体名会与翻译后的简体名并存，重复标签无法消除。
func (s *TagNameService) Normalize(ctx context.Context, names []string) (map[string]string, error) {
	return s.resolveNames(ctx, names, true)
}

// resolveNames 逐个解析标签，每个原始值只解析一次；retranslate 决定字典中的既有名称是否也要翻译。
func (s *TagNameService) resolveNames(ctx context.Context, names []string, retranslate bool) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, raw := range names {
		if _, done := resolved[raw]; done {
			continue
		}
		name := normalizeTagName(raw)
		if name == "" {
			resolved[raw] = ""
			continue
		}
		canonical, err := s.resolveOne(ctx, name, retranslate)
		if err != nil {
			return nil, err
		}
		resolved[raw] = canonical
	}
	return resolved, nil
}

// Probe 用固定探针确认翻译引擎可用。
// 批量归一化命令在改写前先调用它，避免引擎不可用时命令静默跑完却没有任何标签被翻译。
func (s *TagNameService) Probe(ctx context.Context) error {
	if s.translator == nil {
		return errors.New("translation engine is disabled")
	}
	value, err := s.translator.Translate(ctx, TranslationRequest{Text: tagProbeText, TargetLanguage: "zh-CN", Prompt: tagPrompt(tagProbeText)})
	if err != nil {
		return err
	}
	if _, ok := SanitizeTagName(tagProbeText, value); !ok {
		return fmt.Errorf("translation probe returned an invalid tag %q", value)
	}
	return nil
}

// resolveOne 依次尝试字典命中、别名命中、翻译登记。
// 已登记的别名始终优先复用，保证同一写法多次归一得到同一结果，也不会重复调用翻译引擎。
func (s *TagNameService) resolveOne(ctx context.Context, name string, retranslate bool) (string, error) {
	if !retranslate {
		canonical, found, err := s.dictionary.Canonical(ctx, name)
		if err != nil {
			return "", err
		}
		if found {
			return canonical, nil
		}
	}
	canonical, found, err := s.dictionary.Alias(ctx, name)
	if err != nil {
		return "", err
	}
	if found {
		return canonical, nil
	}
	if s.translator == nil {
		return name, nil
	}
	value, ok := s.translate(ctx, name)
	if !ok {
		return name, nil
	}
	if err = s.dictionary.Register(ctx, name, value); err != nil {
		return "", err
	}
	return value, nil
}

// translate 调用翻译引擎并把输出校验成可直接入库的标签；失败或译文无效时返回 ok=false，调用方保留原文。
func (s *TagNameService) translate(ctx context.Context, name string) (string, bool) {
	han := hanOnly(name)
	translated, err := s.translator.Translate(ctx, TranslationRequest{Text: name, TargetLanguage: "zh-CN", Prompt: tagPrompt(name)})
	if err != nil {
		logging.Error(logging.CategoryCollection, "标签翻译失败，保留原文", "tag", name, "error", err.Error())
		return "", false
	}
	value, ok := SanitizeTagName(name, translated)
	if !ok && han {
		// 模型会把生僻纯汉字标签按词补全（舔陰 → 舔阴部），整词结果被拒时逐字重试一次。
		if converted, done := s.convertRunes(ctx, name); done {
			return converted, true
		}
	}
	if !ok {
		logging.Error(logging.CategoryCollection, "标签译文无效，保留原文", "tag", name)
		return "", false
	}
	return value, true
}

// convertRunes 逐字请求繁简转换，作为整词转换被模型改写时的兜底。
// 只接受 1:1 的单个汉字结果：模型对单字仍会扩写（闆 → 老板），这类结果必须丢弃并保留原字。
// 返回 done=false 表示没有任何一个字被成功转换，调用方应保留原文。
func (s *TagNameService) convertRunes(ctx context.Context, name string) (string, bool) {
	var builder strings.Builder
	changed := false
	for _, r := range name {
		if r == ' ' {
			builder.WriteRune(r)
			continue
		}
		translated, err := s.translator.Translate(ctx, TranslationRequest{Text: string(r), TargetLanguage: "zh-CN", Prompt: tagConversionPrompt})
		if err != nil {
			return "", false
		}
		value := []rune(strings.TrimSpace(translated))
		if len(value) != 1 || !unicode.Is(unicode.Han, value[0]) {
			builder.WriteRune(r)
			continue
		}
		if value[0] != r {
			changed = true
		}
		builder.WriteRune(value[0])
	}
	if !changed {
		return "", false
	}
	return builder.String(), true
}

// SanitizeTagName 校验模型输出是否为可直接入库的标签译文。
// 返回 ok=false 表示输出无效（提示词回显、拒答、夹带说明、换行或明显超长），调用方必须保留原文。
// 与标题译文共用同一批噪声短语，但按标签长度重新设限。
func SanitizeTagName(raw, translated string) (string, bool) {
	value := strings.TrimSpace(translated)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	if utf8.RuneCountInString(value) > maxTagNameRunes {
		return "", false
	}
	lowered := strings.ToLower(value)
	for _, marker := range translationNoiseMarkers {
		if strings.Contains(lowered, strings.ToLower(marker)) {
			return "", false
		}
	}
	// 标签不使用句末标点与成对引号；命中说明模型在译文之外补充了句子。
	if strings.ContainsAny(value, "。！？；”“‘’【】《》〈〉") {
		return "", false
	}
	// 逗号是 legacy_media_metadata.genres 的分隔符，箭头是提示词示例的格式；命中说明输出不能直接入库。
	if strings.ContainsRune(value, ',') || strings.Contains(value, "->") || strings.ContainsRune(value, '→') {
		return "", false
	}
	// 纯汉字标签只做繁简转换，转换前后字数必然相同；字数变化说明模型改写了用词，必须保留原文。
	if hanOnly(raw) && utf8.RuneCountInString(value) != utf8.RuneCountInString(raw) {
		return "", false
	}
	// 标签译文与原文同量级；明显超出说明模型夹带了说明或剧情概述。
	if limit := utf8.RuneCountInString(raw)*3 + 8; utf8.RuneCountInString(value) > limit {
		return "", false
	}
	return value, true
}

// tagPrompt 按标签形态选择提示词：纯汉字标签只做逐字繁简转换，含假名或拉丁字母的标签才走跨语言翻译。
func tagPrompt(name string) string {
	if hanOnly(name) {
		return tagConversionPrompt
	}
	return tagTranslationPrompt
}

// hanOnly 报告标签是否只由汉字与空格组成。这类标签只需繁简转换，不涉及跨语言翻译。
func hanOnly(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if r != ' ' && !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

// normalizeTagName 统一来源标签的空白、零宽字符与全角字母数字，作为字典键与翻译输入。
// 只做字符级规范化，不做跨语言猜测；语义合并交由字典与翻译处理。
func normalizeTagName(raw string) string {
	var builder strings.Builder
	previousSpace := false
	for _, r := range raw {
		switch {
		case r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff':
			continue
		case unicode.IsSpace(r):
			if builder.Len() == 0 || previousSpace {
				continue
			}
			previousSpace = true
			builder.WriteRune(' ')
		default:
			previousSpace = false
			builder.WriteRune(foldFullWidth(r))
		}
	}
	return strings.TrimSpace(builder.String())
}

// foldFullWidth 把全角字母与数字折叠为半角，保证「ＶＲ」与「VR」判定为同一标签。
// 只处理字母数字：全角标点折叠成半角会与 legacy_media_metadata.genres 的逗号分隔符冲突，
// 把对标站字典里的「和服，喪服」改写成无法按标签解析的值。
func foldFullWidth(r rune) rune {
	switch {
	case r >= 0xFF10 && r <= 0xFF19, r >= 0xFF21 && r <= 0xFF3A, r >= 0xFF41 && r <= 0xFF5A:
		return r - 0xFEE0
	default:
		return r
	}
}
