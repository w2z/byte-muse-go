package domain

import "testing"

func TestClassifyVideoType(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		title string
		tags  []string
		want  string
	}{
		{"无码标签优先于有码番号", "SSIS-001", "テスト", []string{"無碼"}, VideoTypeUncensored},
		{"无码破解优先于无码", "SSIS-001", "テスト", []string{"无码破解"}, VideoTypeUncensoredCracked},
		{"流出优先于无码", "SSIS-001", "テスト", []string{"无码流出"}, VideoTypeLeaked},
		{"英文标签", "ABC-123", "Test", []string{"Uncensored"}, VideoTypeUncensored},
		{"有码标签", "ABC-123", "Test", []string{"有碼"}, VideoTypeCensored},
		{"标题明确无码", "ABC-123", "【無修正】流出作品", nil, VideoTypeLeaked},
		{"东京热番号", "n1234", "test", nil, VideoTypeUncensored},
		{"东京热带分隔符", "N-1234", "test", nil, VideoTypeUncensored},
		{"HEYZO 系列", "HEYZO-3000", "test", nil, VideoTypeUncensored},
		{"SIRO 系列", "SIRO-4000", "test", nil, VideoTypeUncensored},
		{"普通番号默认有码", "SSIS-001", "test", nil, VideoTypeCensored},
		{"无番号无证据不分类", "", "test", nil, ""},
		{"无番号但有明确标签", "", "test", []string{"流出"}, VideoTypeLeaked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyVideoType(tc.code, tc.title, tc.tags); got != tc.want {
				t.Fatalf("ClassifyVideoType(%q,%q,%v)=%q want %q", tc.code, tc.title, tc.tags, got, tc.want)
			}
		})
	}
}

func TestClassifyVideoTypeOutputIsValid(t *testing.T) {
	for _, code := range []string{"SSIS-001", "n1234", "HEYZO-3000", "", "GYUTTO-001"} {
		if got := ClassifyVideoType(code, "title", nil); got != "" && !ValidVideoType(got) {
			t.Fatalf("ClassifyVideoType(%q) returned invalid type %q", code, got)
		}
	}
}
