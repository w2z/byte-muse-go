package application

import "testing"

func TestSanitizeTranslatedTitle(t *testing.T) {
	cases := []struct {
		name       string
		title      string
		translated string
		wantOK     bool
	}{
		{"正常译文", "初撮り", "初次拍摄", true},
		{"空译文", "初撮り", "   ", false},
		{"拒答", "初撮り", "根据中国法律法规，无法提供翻译服务。", false},
		{"免责声明", "初撮り", "该影片内容涉及不适宜的成人主题。", false},
		{"提示词回显", "初撮り", "你是一位专业的日本影片翻译专员，翻译如下：初次拍摄", false},
		{"夹带说明", "初撮り", "我已按照专业翻译规范处理该内容，最终翻译如下：初次拍摄", false},
		{"换行", "初撮り", "初次拍摄\n（说明）", false},
		{"夹带剧情概述", "髪楽園No.11 茶髪三人組前編", "《发乐园No.11 茶发三人组前篇》讲述了三位染着茶色头发的年轻人之间的故事。他们因共同的兴趣而相识，逐渐建立起深厚的友谊。在前篇中，观众将看到他们初次相遇的情景以及彼此间逐渐萌生的情感纽带，为后续剧情的发展埋下伏笔。", false},
		{"短标题剧情概述", "初撮り", "讲述了初次拍摄的故事", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := SanitizeTranslatedTitle(tc.title, tc.translated)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (value=%q)", ok, tc.wantOK, got)
			}
			if ok && got == "" {
				t.Fatal("valid translation must not be empty")
			}
		})
	}
}
