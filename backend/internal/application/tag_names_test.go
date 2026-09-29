package application

import (
	"context"
	"errors"
	"testing"
	"unicode/utf8"
)

// fakeTagDictionary 记录字典读写，用于验证解析顺序与登记内容。
type fakeTagDictionary struct {
	catalog    map[string]string
	aliases    map[string]string
	registered [][2]string
}

func newFakeTagDictionary(catalog map[string]string) *fakeTagDictionary {
	return &fakeTagDictionary{catalog: catalog, aliases: map[string]string{}}
}

func (f *fakeTagDictionary) Canonical(_ context.Context, name string) (string, bool, error) {
	_, ok := f.catalog[name]
	return name, ok, nil
}

func (f *fakeTagDictionary) Alias(_ context.Context, alias string) (string, bool, error) {
	value, ok := f.aliases[alias]
	return value, ok, nil
}

func (f *fakeTagDictionary) Register(_ context.Context, alias, canonical string) error {
	f.registered = append(f.registered, [2]string{alias, canonical})
	if _, ok := f.catalog[canonical]; !ok {
		f.catalog[canonical] = "未分类"
	}
	if alias != "" {
		f.aliases[alias] = canonical
	}
	return nil
}

// fakeTranslator 返回预置译文并记录调用，便于断言字典命中时不再翻译。
type fakeTranslator struct {
	results map[string]string
	err     error
	calls   []TranslationRequest
}

func (f *fakeTranslator) Translate(_ context.Context, request TranslationRequest) (string, error) {
	f.calls = append(f.calls, request)
	if f.err != nil {
		return "", f.err
	}
	if value, ok := f.results[request.Text]; ok {
		return value, nil
	}
	return request.Text, nil
}

// TestTagNameNormalizeTranslatesDictionaryName 验证历史归一化会翻译字典里的既有名称，入库解析则直接复用。
func TestTagNameNormalizeTranslatesDictionaryName(t *testing.T) {
	dictionary := newFakeTagDictionary(map[string]string{"高畫質": "类别"})
	translator := &fakeTranslator{results: map[string]string{"高畫質": "高画质"}}
	service := NewTagNameService(dictionary, translator)
	resolved, err := service.Resolve(context.Background(), []string{"高畫質"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["高畫質"] != "高畫質" || len(translator.calls) != 0 {
		t.Fatalf("入库解析不应翻译字典已存在的标签: %v calls=%d", resolved, len(translator.calls))
	}
	normalized, err := service.Normalize(context.Background(), []string{"高畫質"})
	if err != nil {
		t.Fatal(err)
	}
	if normalized["高畫質"] != "高画质" || len(translator.calls) != 1 {
		t.Fatalf("normalized=%v calls=%d", normalized, len(translator.calls))
	}
	if len(dictionary.registered) != 1 || dictionary.registered[0] != [2]string{"高畫質", "高画质"} {
		t.Fatalf("登记=%v", dictionary.registered)
	}
	// 别名登记后重复归一直接复用，不再调用翻译引擎。
	if _, err = service.Normalize(context.Background(), []string{"高畫質"}); err != nil {
		t.Fatal(err)
	}
	if len(translator.calls) != 1 {
		t.Fatalf("重复归一仍调用翻译: %d", len(translator.calls))
	}
}

// TestTagNameResolvePrefersDictionary 验证字典与别名命中时不再调用翻译引擎。
func TestTagNameResolvePrefersDictionary(t *testing.T) {
	dictionary := newFakeTagDictionary(map[string]string{"巨乳": "体型"})
	dictionary.aliases["Big Tits"] = "巨乳"
	translator := &fakeTranslator{results: map[string]string{}}
	service := NewTagNameService(dictionary, translator)
	resolved, err := service.Resolve(context.Background(), []string{"巨乳", "Big Tits", " Big Tits "})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["巨乳"] != "巨乳" || resolved["Big Tits"] != "巨乳" || resolved[" Big Tits "] != "巨乳" {
		t.Fatalf("resolved=%v", resolved)
	}
	if len(translator.calls) != 0 {
		t.Fatalf("字典命中仍触发翻译: %+v", translator.calls)
	}
}

// TestTagNameResolveTranslatesNewTag 验证库内不存在的标签先翻译成简体中文再登记，并按标签形态选用提示词。
// 纯汉字标签只需逐字繁简转换，含日文假名或拉丁字母的标签才走跨语言翻译提示词。
func TestTagNameResolveTranslatesNewTag(t *testing.T) {
	dictionary := newFakeTagDictionary(map[string]string{})
	translator := &fakeTranslator{results: map[string]string{"Big Tits": "巨乳", "高畫質": "高画质"}}
	service := NewTagNameService(dictionary, translator)
	resolved, err := service.Resolve(context.Background(), []string{"Big Tits", "高畫質"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["Big Tits"] != "巨乳" || resolved["高畫質"] != "高画质" {
		t.Fatalf("resolved=%v", resolved)
	}
	if len(translator.calls) != 2 || translator.calls[0].Prompt != tagTranslationPrompt || translator.calls[1].Prompt != tagConversionPrompt {
		t.Fatalf("提示词选择错误: %+v", translator.calls)
	}
	if len(dictionary.registered) != 2 || dictionary.registered[0] != [2]string{"Big Tits", "巨乳"} || dictionary.registered[1] != [2]string{"高畫質", "高画质"} {
		t.Fatalf("登记=%v", dictionary.registered)
	}
	if _, ok := dictionary.catalog["巨乳"]; !ok {
		t.Fatal("权威名未写入字典")
	}
}

// TestTagNameResolveKeepsRawOnFailure 验证译文无效或引擎不可用时保留原文且不登记未确认的权威名。
func TestTagNameResolveKeepsRawOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		translator TranslationClient
	}{
		{name: "翻译未启用"},
		{name: "译文无效", translator: &fakeTranslator{results: map[string]string{"高畫質": "以下是翻译结果：高画质。"}}},
		{name: "引擎报错", translator: &fakeTranslator{err: errors.New("engine unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dictionary := newFakeTagDictionary(map[string]string{})
			service := NewTagNameService(dictionary, tc.translator)
			resolved, err := service.Resolve(context.Background(), []string{"高畫質"})
			if err != nil {
				t.Fatal(err)
			}
			if resolved["高畫質"] != "高畫質" {
				t.Fatalf("resolved=%v", resolved)
			}
			if len(dictionary.registered) != 0 || len(dictionary.catalog) != 0 {
				t.Fatalf("登记=%v catalog=%v", dictionary.registered, dictionary.catalog)
			}
		})
	}
}

// TestSanitizeTagNameRejectsHanRewrite 验证纯汉字标签只接受等长的繁简转换，避免模型把简体标签改写成别的词。
func TestSanitizeTagNameRejectsHanRewrite(t *testing.T) {
	for _, tc := range []struct {
		raw, translated string
		want            string
		ok              bool
	}{
		{raw: "乳交", translated: "亲密接触", ok: false},
		{raw: "中出", translated: "中出术", ok: false},
		{raw: "單體作品", translated: "单体作品", want: "单体作品", ok: true},
		{raw: "已婚婦女", translated: "已婚妇女", want: "已婚妇女", ok: true},
		{raw: "OL", translated: "职场女性", want: "职场女性", ok: true},
	} {
		value, ok := SanitizeTagName(tc.raw, tc.translated)
		if ok != tc.ok || value != tc.want {
			t.Fatalf("raw=%q translated=%q value=%q ok=%v want=%q/%v", tc.raw, tc.translated, value, ok, tc.want, tc.ok)
		}
	}
}

// perRuneTranslator 模拟会把生僻纯汉字标签按词补全的模型：整词结果多一个字，单字请求才给出正确的繁简转换。
type perRuneTranslator struct{}

var perRuneResults = map[string]string{"陰": "阴", "闆": "老板", "楓": "枫"}

func (perRuneTranslator) Translate(_ context.Context, request TranslationRequest) (string, error) {
	if value, ok := perRuneResults[request.Text]; ok {
		return value, nil
	}
	if utf8.RuneCountInString(request.Text) > 1 {
		return request.Text + "部", nil
	}
	return request.Text, nil
}

// TestTagNameResolveFallsBackToPerRune 验证整词转换被模型改写时逐字兜底，并且只接受 1:1 的汉字结果。
func TestTagNameResolveFallsBackToPerRune(t *testing.T) {
	dictionary := newFakeTagDictionary(map[string]string{})
	service := NewTagNameService(dictionary, perRuneTranslator{})
	resolved, err := service.Resolve(context.Background(), []string{"舔陰", "老闆娘"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["舔陰"] != "舔阴" {
		t.Fatalf("整词被改写时应逐字转换: %v", resolved)
	}
	// 闆 的逐字结果是扩写而非 1:1，必须保留原字，不能拼出「老老板娘」。
	if resolved["老闆娘"] != "老闆娘" {
		t.Fatalf("逐字结果非 1:1 时应保留原文: %v", resolved)
	}
}

// TestNormalizeTagName 验证空白、零宽字符与全角字符在查库和翻译前就已统一。
func TestNormalizeTagName(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"  ＶＲ  ", "VR"},
		{"Big  Tits", "Big Tits"},
		{"巨乳\u200b", "巨乳"},
		{"和服，喪服", "和服，喪服"},
		{"", ""},
		{"  ", ""},
	} {
		if got := normalizeTagName(tc.raw); got != tc.want {
			t.Fatalf("normalize(%q)=%q want=%q", tc.raw, got, tc.want)
		}
	}
}

// TestSanitizeTagName 验证标签译文校验只接受可直接入库的短标签。
func TestSanitizeTagName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		translated string
		want       string
		ok         bool
	}{
		{name: "正常", translated: " 巨乳 ", want: "巨乳", ok: true},
		{name: "空", translated: "  ", ok: false},
		{name: "换行", translated: "巨乳\n说明", ok: false},
		{name: "提示词回显", translated: "以下是翻译", ok: false},
		{name: "夹带句子", translated: "巨乳。", ok: false},
		{name: "含逗号", translated: "和服,丧服", ok: false},
		{name: "示例回显", translated: "巨乳 -> 巨乳", ok: false},
		{name: "超长", translated: "这是一个非常长的标签说明用来描述影片内容而不是标签本身还会继续写下去直到超过上限", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, ok := SanitizeTagName("Big Tits", tc.translated)
			if ok != tc.ok || value != tc.want {
				t.Fatalf("value=%q ok=%v want=%q/%v", value, ok, tc.want, tc.ok)
			}
		})
	}
}
