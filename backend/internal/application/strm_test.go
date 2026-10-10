package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/clouddrive"
)

// TestStrmLongVideoName 验证 NAS 的字节限制、UTF-8 完整性以及相同前缀的资源不会因截断合并。
func TestStrmLongVideoName(t *testing.T) {
	prefix := strings.Repeat("日文影片", 40)
	first := strmVideoName(prefix + "A.mkv")
	second := strmVideoName(prefix + "B.mkv")
	if len(first) > 255 || !utf8.ValidString(first) || !strings.HasSuffix(first, ".strm") {
		t.Fatalf("不可写入 NAS 的名称：%d bytes, valid=%v", len(first), utf8.ValidString(first))
	}
	if first == second || first != strmVideoName(prefix+"A.mkv") {
		t.Fatal("缩短名称必须稳定且保留不同名称的区分")
	}
	if got := strmVideoName("ABC-123-C.mp4"); got != "ABC-123-C.strm" {
		t.Fatalf("普通名称被改变：%s", got)
	}
}

// strmPan115Stub 按目录 ID 返回预设的 115 目录内容，使 strm 生成与播放解析可离线验证。
type strmPan115Stub struct {
	pages    map[string]domain.Pan115FilePage
	playURL  string
	playErr  error
	playFile string
	playUA   string
}

func (s *strmPan115Stub) Files(_ context.Context, directoryID string, _, _ int) (domain.Pan115FilePage, error) {
	page, ok := s.pages[directoryID]
	if !ok {
		return domain.Pan115FilePage{}, errors.New("未知的 115 目录")
	}
	return page, nil
}

func (s *strmPan115Stub) PlayURL(_ context.Context, fileID, userAgent string) (string, error) {
	s.playFile, s.playUA = fileID, userAgent
	if s.playErr != nil {
		return "", s.playErr
	}
	return s.playURL, nil
}

// strmCloudStub 提供 CloudDrive2 的目录列表与直链结果。
type strmCloudStub struct {
	configured bool
	entries    map[string][]clouddrive.Entry
	download   clouddrive.Download
	requested  []string
}

func (s *strmCloudStub) Configured(context.Context) bool { return s.configured }

func (s *strmCloudStub) ListSubFiles(_ context.Context, path string) ([]clouddrive.Entry, error) {
	s.requested = append(s.requested, path)
	items, ok := s.entries[path]
	if !ok {
		return nil, errors.New("未知的 CloudDrive2 目录")
	}
	return items, nil
}

func (s *strmCloudStub) DownloadURL(_ context.Context, _ string, _ bool) (clouddrive.Download, error) {
	return s.download, nil
}

// strmTestSettings 返回固定设置快照；调用方按需覆盖键值。
func strmTestSettings(values map[string]string) func(context.Context) (map[string]string, error) {
	return func(context.Context) (map[string]string, error) { return values, nil }
}

// strmTestMappings 把映射序列化成设置值。
func strmTestMappings(t *testing.T, mappings []domain.StrmMapping) string {
	t.Helper()
	raw, err := json.Marshal(mappings)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func newStrmTestService(t *testing.T, root string, pan115 strmPan115API, cloud strmCloudDriveAPI, values map[string]string) *StrmService {
	t.Helper()
	service, err := NewStrmService(pan115, cloud, strmTestSettings(values))
	if err != nil {
		t.Fatal(err)
	}
	service.root = filepath.Clean(root)
	return service
}

// TestParseStrmMappings 覆盖映射设置的合法与非法输入，保证保存校验与运行期扫描同一套规则。
func TestParseStrmMappings(t *testing.T) {
	valid := strmTestMappings(t, []domain.StrmMapping{
		{Kind: "115", ID: "100", Path: "/影片", LocalPath: "/movies/"},
		{Kind: "cd2", ID: "/115/影片", Path: "/115/影片", LocalPath: "/cloud"},
	})
	mappings, err := parseStrmMappings(valid)
	if err != nil {
		t.Fatalf("合法映射被拒绝: %v", err)
	}
	if len(mappings) != 2 || mappings[0].LocalPath != "/movies" {
		t.Fatalf("映射解析结果不符: %+v", mappings)
	}
	if len(mappings[0].Formats) != len(defaultStrmFormats) {
		t.Fatalf("缺少 formats 时应使用默认格式: %+v", mappings[0].Formats)
	}

	normalized, err := parseStrmMappings(`[{"kind":"115","id":"1","path":"/a","local_path":"/b","formats":[" .MP4 ","..Mkv","mp4",""]}]`)
	if err != nil {
		t.Fatalf("格式规范化失败: %v", err)
	}
	if got, want := strings.Join(normalized[0].Formats, ","), "mp4,mkv"; got != want {
		t.Fatalf("格式规范化结果 %q，期望 %q", got, want)
	}
	for _, tc := range []struct{ name, raw string }{
		{"空值表示未配置", "  "},
		{"非数组", `{"kind":"115"}`},
		{"未知网盘类型", `[{"kind":"pan","id":"1","path":"/a","local_path":"/b"}]`},
		{"缺少网盘目录", `[{"kind":"115","id":"","path":"/a","local_path":"/b"}]`},
		{"本地路径非绝对", `[{"kind":"115","id":"1","path":"/a","local_path":"b"}]`},
		{"重复映射", `[{"kind":"115","id":"1","path":"/a","local_path":"/b"},{"kind":"115","id":"1","path":"/a","local_path":"/b"}]`},
		{"重复本地目录", `[{"kind":"115","id":"1","path":"/a","local_path":"/b"},{"kind":"cd2","id":"/c","path":"/c","local_path":"/b"}]`},
		{"没有生成格式", `[{"kind":"115","id":"1","path":"/a","local_path":"/b","formats":[]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStrmMappings(tc.raw)
			if tc.raw == "  " {
				if err != nil || got != nil {
					t.Fatalf("空值应表示无映射: %v %+v", err, got)
				}
				return
			}
			if err == nil {
				t.Fatalf("非法映射未被拒绝: %s", tc.raw)
			}
		})
	}
}

// TestResolveStrmPath 约束本地目录浏览器只能访问 strm 根目录以下的内容。
func TestResolveStrmPath(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		relative string
		wantPath string
		wantErr  bool
	}{
		{"", "/", false},
		{"/movies", "/movies", false},
		{"movies/2026", "/movies/2026", false},
		{"/movies/../cloud", "/cloud", false},
		{"../etc", "/etc", false},
		{`..\etc`, "/etc", false},
		{"a\x00b", "", true},
	} {
		absolute, normalized, err := resolveStrmPath(root, tc.relative)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q 应被拒绝", tc.relative)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 解析失败: %v", tc.relative, err)
		}
		if normalized != tc.wantPath {
			t.Fatalf("%q 规范化结果 %q，期望 %q", tc.relative, normalized, tc.wantPath)
		}
		if !withinStrmRoot(root, absolute) {
			t.Fatalf("%q 解析到了根目录之外: %s", tc.relative, absolute)
		}
	}
}

// TestStrmRootIgnoresLegacySetting 验证旧根目录设置不能改变浏览、创建和生成的共同边界。
func TestStrmRootIgnoresLegacySetting(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	legacy := t.TempDir()
	pan115 := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"100": {Files: []domain.Pan115File{{ID: "f1", PickCode: "pc-1", Name: "A.mkv"}}},
	}}
	service := newStrmTestService(t, root, pan115, nil, map[string]string{
		"STRM_ROOT":            legacy,
		strmPathsSettingKey:    strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "100", Path: "/影片", LocalPath: "/movies"}}),
		strmPlayBaseSettingKey: "http://bm.local",
	})
	if actual, err := service.Root(ctx); err != nil || actual != filepath.Clean(root) {
		t.Fatalf("根目录被历史设置改变: %q err=%v", actual, err)
	}
	if _, err := service.CreateDirectory(ctx, "/", "movies"); err != nil {
		t.Fatal(err)
	}
	page, err := service.Directories(ctx, "/")
	if err != nil || len(page.Directories) != 1 || page.Directories[0].Name != "movies" {
		t.Fatalf("目录浏览异常: %+v err=%v", page, err)
	}
	result, err := service.Scan(ctx, "", domain.StrmGenerateFull)
	if err != nil || result.Created != 1 {
		t.Fatalf("生成异常: %+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "A.strm")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "movies")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("不得写入旧根目录")
	}
	service.settings = func(context.Context) (map[string]string, error) { return nil, errors.New("settings unavailable") }
	if actual, err := service.Root(ctx); err != nil || actual != filepath.Clean(root) {
		t.Fatalf("浏览根目录不应依赖设置读取: %q err=%v", actual, err)
	}
}

// TestCreateDirectoryRejectsNestedName 保证创建目录只产生一级目录，且上级必须存在。
func TestCreateDirectoryRejectsNestedName(t *testing.T) {
	root := t.TempDir()
	service := newStrmTestService(t, root, nil, nil, nil)
	if _, err := service.CreateDirectory(context.Background(), "/", "a/b"); err == nil {
		t.Fatal("含分隔符的目录名应被拒绝")
	}
	created, err := service.CreateDirectory(context.Background(), "/", "movies")
	if err != nil {
		t.Fatalf("创建一级目录失败: %v", err)
	}
	if created.Path != "/movies" {
		t.Fatalf("创建结果路径不符: %+v", created)
	}
	if _, err := service.CreateDirectory(context.Background(), "/", "movies"); err == nil {
		t.Fatal("重复创建同名目录应被拒绝")
	}
	if _, err := service.CreateDirectory(context.Background(), "/missing", "sub"); err == nil {
		t.Fatal("上级目录不存在时应被拒绝")
	}
	page, err := service.Directories(context.Background(), "/")
	if err != nil {
		t.Fatalf("读取根目录失败: %v", err)
	}
	if len(page.Directories) != 1 || page.Directories[0].Name != "movies" {
		t.Fatalf("根目录内容不符: %+v", page.Directories)
	}
}

// TestStrmRootUnavailableReportsDeploymentProblem 验证 strm 根目录无法创建或读取时返回可诊断的错误，
// 而不是让设置页只看到“服务内部错误”。
func TestStrmRootUnavailableReportsDeploymentProblem(t *testing.T) {
	ctx := context.Background()
	// 用普通文件占据根目录路径：MkdirAll 必然失败，等价于容器内 /strm 没有挂载可写目录。
	blocked := filepath.Join(t.TempDir(), "strm")
	if err := os.WriteFile(blocked, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := newStrmTestService(t, blocked, nil, nil, nil)
	if _, err := service.Directories(ctx, "/"); !errors.Is(err, ErrStrmRootUnavailable) {
		t.Fatalf("根目录不可用时浏览应返回 ErrStrmRootUnavailable，实际 %v", err)
	}
	if _, err := service.CreateDirectory(ctx, "/", "movies"); !errors.Is(err, ErrStrmRootUnavailable) {
		t.Fatalf("根目录不可用时创建目录应返回 ErrStrmRootUnavailable，实际 %v", err)
	}
}

// TestScanWritesPlayableStrmFiles 验证 115 与 CloudDrive2 映射都能生成内容正确的 strm：
// 全量清理旧内容后重建，增量保留本地文件只补齐缺失项，非媒体文件两种方式都不生成。
func TestScanWritesPlayableStrmFiles(t *testing.T) {
	root := t.TempDir()
	pan115 := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"100": {Files: []domain.Pan115File{
			{ID: "200", Name: "合集", IsDirectory: true},
			{ID: "f1", PickCode: "pc-1", Name: "A.mkv"},
			{ID: "f2", Name: "readme.txt"},
		}},
		"200": {Files: []domain.Pan115File{{ID: "f3", PickCode: "pc-3", Name: "C.mp4"}}},
	}}
	cloud := &strmCloudStub{configured: true, entries: map[string][]clouddrive.Entry{
		"/115/影片": {
			{Name: "S1", FullPath: "/115/影片/S1", Directory: true},
			{Name: "ignored.srt"},
		},
		"/115/影片/S1": {{Name: "B.mkv", FullPath: "/115/影片/S1/B.mkv"}},
	}}
	values := map[string]string{
		"STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{
			{Kind: "115", ID: "100", Path: "/影片", LocalPath: "/movies"},
			{Kind: "cd2", ID: "/115/影片", Path: "/115/影片", LocalPath: "/cloud"},
		}),
		"STRM_PLAY_BASE": "http://bm.local/",
	}
	service := newStrmTestService(t, root, pan115, cloud, values)
	if err := os.MkdirAll(filepath.Join(root, "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "movies", "A.strm"), []byte("http://bm.local/files/play/115/f1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := service.Scan(context.Background(), "", domain.StrmGenerateFull)
	if err != nil {
		t.Fatalf("生成 strm 失败: %v", err)
	}
	// 预置的 A.strm 内容已过期，全量生成必须先清理再按 pick_code 重建。
	if result.Files != 3 || result.Deleted != 1 || result.Created != 3 || result.Failed != 0 {
		t.Fatalf("生成统计不符: %+v", result)
	}
	if result.Emby.Attempted {
		t.Fatalf("未开启自动刷新时不应请求 Emby: %+v", result.Emby)
	}
	for target, want := range map[string]string{
		"movies/A.strm":    "http://bm.local/files/play/115/pc-1\n",
		"movies/合集/C.strm": "http://bm.local/files/play/115/pc-3\n",
		"cloud/S1/B.strm":  "http://bm.local/files/play/cd2/115/%E5%BD%B1%E7%89%87/S1/B.mkv\n",
	} {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", target, err)
		}
		if string(content) != want {
			t.Fatalf("%s 内容为 %q，期望 %q", target, string(content), want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "readme.txt.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("非媒体文件不应生成 strm")
	}

	second, err := service.Scan(context.Background(), "", domain.StrmGenerateIncremental)
	if err != nil {
		t.Fatalf("重复生成 strm 失败: %v", err)
	}
	if second.Deleted != 0 || second.Created != 0 || second.Mappings[0].Unchanged != 2 || second.Mappings[1].Unchanged != 1 {
		t.Fatalf("增量生成应跳过本地已有文件: %+v", second)
	}
}

// TestScanFullClearsStaleLocalStrm 验证全量生成清理网盘已删除文件对应的本地 strm：
// 只删除本功能生成的 .strm 文件，保留映射目录内的其他文件，并移除清理后变空的目录。
func TestScanFullClearsStaleLocalStrm(t *testing.T) {
	root := t.TempDir()
	pan115 := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"100": {Files: []domain.Pan115File{{ID: "f1", PickCode: "pc-1", Name: "A.mkv"}}},
	}}
	values := map[string]string{
		"STRM_PATHS":     strmTestMappings(t, []domain.StrmMapping{{Kind: domain.StrmKindPan115, ID: "100", Path: "/影片", LocalPath: "/movies"}}),
		"STRM_PLAY_BASE": "http://bm.local",
	}
	service := newStrmTestService(t, root, pan115, nil, values)
	for target, content := range map[string]string{
		"movies/A.strm":       "http://bm.local/files/play/115/legacy\n",
		"movies/removed.strm": "http://bm.local/files/play/115/gone\n",
		"movies/合集/old.strm":  "http://bm.local/files/play/115/gone\n",
		"movies/poster.jpg":   "cover",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(target))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := service.Scan(context.Background(), "", domain.StrmGenerateFull)
	if err != nil {
		t.Fatalf("全量生成失败: %v", err)
	}
	if result.Files != 1 || result.Deleted != 3 || result.Created != 1 || result.Failed != 0 {
		t.Fatalf("全量生成统计不符: %+v", result)
	}
	content, err := os.ReadFile(filepath.Join(root, "movies", "A.strm"))
	if err != nil || string(content) != "http://bm.local/files/play/115/pc-1\n" {
		t.Fatalf("本地 strm 未被清理重建: %q err=%v", string(content), err)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "removed.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("网盘已删除文件对应的本地 strm 应被清理")
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "合集")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("清理后为空的目录应被移除")
	}
	if content, err := os.ReadFile(filepath.Join(root, "movies", "poster.jpg")); err != nil || string(content) != "cover" {
		t.Fatalf("非 strm 文件不应被删除: %q err=%v", string(content), err)
	}
}

// TestScanIncrementalKeepsExistingLocalStrm 验证增量生成不改写本地已有 strm，只补齐缺失项。
func TestScanIncrementalKeepsExistingLocalStrm(t *testing.T) {
	root := t.TempDir()
	pan115 := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"100": {Files: []domain.Pan115File{
			{ID: "f1", PickCode: "pc-1", Name: "A.mkv"},
			{ID: "f2", PickCode: "pc-2", Name: "B.mkv"},
		}},
	}}
	values := map[string]string{
		"STRM_PATHS":     strmTestMappings(t, []domain.StrmMapping{{Kind: domain.StrmKindPan115, ID: "100", Path: "/影片", LocalPath: "/movies"}}),
		"STRM_PLAY_BASE": "http://bm.local",
	}
	service := newStrmTestService(t, root, pan115, nil, values)
	existing := filepath.Join(root, "movies", "A.strm")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("http://bm.local/files/play/115/legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := service.Scan(context.Background(), "", domain.StrmGenerateIncremental)
	if err != nil {
		t.Fatalf("增量生成失败: %v", err)
	}
	if result.Deleted != 0 || result.Created != 1 || result.Failed != 0 || result.Mappings[0].Unchanged != 1 {
		t.Fatalf("增量生成统计不符: %+v", result)
	}
	content, err := os.ReadFile(existing)
	if err != nil || string(content) != "http://bm.local/files/play/115/legacy\n" {
		t.Fatalf("增量生成不应改写本地已有文件: %q err=%v", string(content), err)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "B.strm")); err != nil {
		t.Fatalf("缺失的 strm 应被补齐: %v", err)
	}
}

// TestScanMissingPickCode 验证缺少提取码时报告失败，且保留文件 ID 供扫描入库。
func TestScanMissingPickCode(t *testing.T) {
	root := t.TempDir()
	pan115 := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"100": {Files: []domain.Pan115File{{ID: "f1", Name: "missing.mkv"}, {ID: "f2", PickCode: "pc-2", Name: "valid.mkv"}}},
	}}
	values := map[string]string{
		"STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "100", Path: "/影片", LocalPath: "/movies"}}),
	}
	service := newStrmTestService(t, root, pan115, nil, values)
	var files []strmSourceFile
	err := walkPan115Files(context.Background(), pan115, "100", strmFileFilter{formats: defaultStrmFormats}, func(file strmSourceFile) error {
		files = append(files, file)
		return nil
	})
	if err != nil || len(files) != 2 || files[0].ID != "f1" || files[1].ID != "f2" || files[1].PickCode != "pc-2" {
		t.Fatalf("扫描标识未保留: %+v err=%v", files, err)
	}
	ctx := context.WithValue(context.Background(), strmRetryKey{}, &strmRetryPolicy{wait: func(context.Context, string, error) error { return context.Canceled }})
	result, err := service.Scan(ctx, "http://bm.local", domain.StrmGenerateFull)
	if !errors.Is(err, context.Canceled) || result.Files != 2 || result.Failed != 1 || result.Created != 1 || !strings.Contains(result.Mappings[0].Message, "pick_code") {
		t.Fatalf("缺失提取码结果不符: %+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "missing.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("缺失提取码不得生成无效 STRM")
	}
}

// TestScanUsesMappingFormats 验证每条映射只生成自己选择的文件格式，且扩展名匹配不区分大小写。
func TestScanUsesMappingFormats(t *testing.T) {
	root := t.TempDir()
	pan115 := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"100": {Files: []domain.Pan115File{{ID: "f1", PickCode: "pc-1", Name: "movie.MKV"}, {ID: "f2", PickCode: "pc-2", Name: "unselected.mp4"}}},
	}}
	values := map[string]string{
		"STRM_PATHS":     strmTestMappings(t, []domain.StrmMapping{{Kind: domain.StrmKindPan115, ID: "100", Path: "/影片", LocalPath: "/movies", Formats: []string{"mkv"}}}),
		"STRM_PLAY_BASE": "http://bm.local",
	}
	service := newStrmTestService(t, root, pan115, nil, values)
	result, err := service.Scan(context.Background(), "", domain.StrmGenerateFull)
	if err != nil || result.Files != 1 || result.Created != 1 {
		t.Fatalf("格式过滤结果不符: %+v（err=%v）", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "movie.strm")); err != nil {
		t.Fatalf("选中格式未生成: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "unselected.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("未选格式不应生成 strm")
	}
}

// TestScanReportsUnconfiguredMapping 验证未配置网盘时给出可读原因，而不是中断整次扫描。
func TestScanReportsUnconfiguredMapping(t *testing.T) {
	root := t.TempDir()
	values := map[string]string{
		"STRM_PATHS":     strmTestMappings(t, []domain.StrmMapping{{Kind: "cd2", ID: "/115", Path: "/115", LocalPath: "/cloud"}}),
		"STRM_PLAY_BASE": "http://bm.local",
	}
	service := newStrmTestService(t, root, nil, &strmCloudStub{configured: false}, values)
	result, err := service.Scan(context.Background(), "", domain.StrmGenerateFull)
	if err != nil {
		t.Fatalf("扫描不应因单条映射失败而中断: %v", err)
	}
	if result.Mappings[0].Message != "CloudDrive2 尚未配置" || result.Files != 0 {
		t.Fatalf("未配置原因不符: %+v", result.Mappings[0])
	}
}

// TestStrmPlayURLRoundTrip 固定播放地址的编码与解析规则，两者必须互为逆运算。
func TestStrmPlayURLRoundTrip(t *testing.T) {
	for _, tc := range []struct{ kind, fileID string }{
		{"115", "f1"},
		{"cd2", "/115/影片/S1/B.mkv"},
		{"cd2", "/115/影片/A B#1.mkv"},
	} {
		address := strmPlayURL("http://bm.local", tc.kind, tc.fileID)
		if strings.Contains(address, "//115") {
			t.Fatalf("地址中不应出现多余空段: %s", address)
		}
		kind, fileID, err := ParseStrmPlayPath(strings.TrimPrefix(address, "http://bm.local/files/play/"))
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", address, err)
		}
		if kind != tc.kind || fileID != tc.fileID {
			t.Fatalf("往返结果 %s/%s，期望 %s/%s", kind, fileID, tc.kind, tc.fileID)
		}
	}
	if _, _, err := ParseStrmPlayPath("unknown/f1"); err == nil {
		t.Fatal("未知网盘类型应被拒绝")
	}
	if _, _, err := ParseStrmPlayPath("cd2"); err == nil {
		t.Fatal("缺少文件标识应被拒绝")
	}
}

// TestPlayURLUsesPlaybackUserAgent 验证 115 直链使用播放端 UA，CloudDrive2 需要转发时返回代理目标。
func TestPlayURLUsesPlaybackUserAgent(t *testing.T) {
	pan115 := &strmPan115Stub{playURL: "https://115.example/direct"}
	cloud := &strmCloudStub{configured: true, download: clouddrive.Download{
		URL:       "https://cd2.example/direct",
		UserAgent: "cd2-agent",
		Headers:   map[string]string{"X-Token": "abc"},
	}}
	service := newStrmTestService(t, t.TempDir(), pan115, cloud, map[string]string{})

	redirect, err := service.PlayURL(context.Background(), "115", "f1", "Emby/4.8")
	if err != nil {
		t.Fatalf("解析 115 播放地址失败: %v", err)
	}
	if redirect.Redirect != "https://115.example/direct" || redirect.Proxy != nil {
		t.Fatalf("115 应直接跳转: %+v", redirect)
	}
	if pan115.playUA != "Emby/4.8" {
		t.Fatalf("115 直链未使用播放端 UA: %q", pan115.playUA)
	}

	proxied, err := service.PlayURL(context.Background(), "cd2", "/115/影片/B.mkv", "Emby/4.8")
	if err != nil {
		t.Fatalf("解析 CloudDrive2 播放地址失败: %v", err)
	}
	if proxied.Proxy == nil || proxied.Proxy.URL != "https://cd2.example/direct" || proxied.Proxy.UserAgent != "cd2-agent" {
		t.Fatalf("CloudDrive2 直链需要服务端转发: %+v", proxied)
	}

	if _, err := service.PlayURL(context.Background(), "cd2", "/115/x", ""); err != nil {
		t.Fatalf("已配置的 CloudDrive2 不应报未配置: %v", err)
	}
}

// TestCloudDriveDirectories 只返回目录，并把根目录与子目录路径规范化。
func TestCloudDriveDirectories(t *testing.T) {
	cloud := &strmCloudStub{configured: true, entries: map[string][]clouddrive.Entry{
		"/": {
			{Name: "115", FullPath: "/115", Directory: true},
			{Name: "readme.txt"},
		},
		"/115": {{Name: "影片", FullPath: "/115/影片", Directory: true}},
	}}
	service := newStrmTestService(t, t.TempDir(), nil, cloud, map[string]string{})
	page, err := service.CloudDriveDirectories(context.Background(), "")
	if err != nil {
		t.Fatalf("读取 CloudDrive2 根目录失败: %v", err)
	}
	if page.Path != "/" || len(page.Directories) != 1 || page.Directories[0].Path != "/115" {
		t.Fatalf("CloudDrive2 根目录结果不符: %+v", page)
	}
	nested, err := service.CloudDriveDirectories(context.Background(), "/115/")
	if err != nil {
		t.Fatalf("读取 CloudDrive2 子目录失败: %v", err)
	}
	if nested.Path != "/115" || len(nested.Directories) != 1 || nested.Directories[0].Name != "影片" {
		t.Fatalf("CloudDrive2 子目录结果不符: %+v", nested)
	}
	unconfigured := newStrmTestService(t, t.TempDir(), nil, &strmCloudStub{}, map[string]string{})
	if _, err := unconfigured.CloudDriveDirectories(context.Background(), "/"); !errors.Is(err, ErrStrmNotConfigured) {
		t.Fatalf("未配置时应返回 ErrStrmNotConfigured，实际 %v", err)
	}
}

// TestWriteStrmFileIsIdempotent 保证内容一致时不触碰磁盘。
func TestWriteStrmFileIsIdempotent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nested", "A.strm")
	created, changed, err := writeStrmFile(target, "http://bm.local/files/play/115/f1\n")
	if err != nil || !created || !changed {
		t.Fatalf("首次写入结果不符: %v %v %v", created, changed, err)
	}
	created, changed, err = writeStrmFile(target, "http://bm.local/files/play/115/f1\n")
	if err != nil || created || changed {
		t.Fatalf("重复写入不应落盘: %v %v %v", created, changed, err)
	}
	created, changed, err = writeStrmFile(target, "http://bm.local/files/play/115/f2\n")
	if err != nil || created || !changed {
		t.Fatalf("内容变化时应覆盖: %v %v %v", created, changed, err)
	}
}

// TestParseStrmMappingsNormalizesFilters 验证排除规则统一匹配方式、按「方式 + 关键字」去重保存，
// 并拒绝负数的最小视频大小与不受支持的匹配方式。
func TestParseStrmMappingsNormalizesFilters(t *testing.T) {
	mappings, err := parseStrmMappings(`[{"kind":"115","id":"1","path":"/a","local_path":"/b","min_size_mb":300,` +
		`"exclude":[{"mode":" PREFIX ","value":" Sample "},{"mode":"contains","value":"trailer"},` +
		`{"mode":"contains","value":"TRAILER"},{"mode":"suffix","value":"  "}]}]`)
	if err != nil {
		t.Fatalf("带过滤条件的映射被拒绝: %v", err)
	}
	if got := mappings[0].MinSizeMB; got != 300 {
		t.Fatalf("最小视频大小解析为 %d，期望 300", got)
	}
	// 关键字保留用户输入的大小写用于回显，仅判重时忽略大小写；空关键字与重复规则被丢弃。
	want := []domain.StrmExcludeKeyword{
		{Mode: domain.StrmExcludeModePrefix, Value: "Sample"},
		{Mode: domain.StrmExcludeModeContains, Value: "trailer"},
	}
	if !reflect.DeepEqual(mappings[0].Exclude, want) {
		t.Fatalf("排除规则规范化为 %+v，期望 %+v", mappings[0].Exclude, want)
	}

	if _, err := parseStrmMappings(`[{"kind":"115","id":"1","path":"/a","local_path":"/b","min_size_mb":-1}]`); err == nil {
		t.Fatal("负数的最小视频大小应被拒绝")
	}
	if _, err := parseStrmMappings(`[{"kind":"115","id":"1","path":"/a","local_path":"/b","exclude":[{"mode":"regex","value":"x"}]}]`); err == nil {
		t.Fatal("不受支持的匹配方式应被拒绝")
	}
}

// TestStrmFileFilterMatchesKeywordsAndSize 约束四种匹配方式互不等价：等于只认完整名称，
// 前缀、后缀、包含各按对应位置匹配且不区分大小写；体积为 0 表示网盘未返回体积，不应被最小体积过滤。
func TestStrmFileFilterMatchesKeywordsAndSize(t *testing.T) {
	filter := strmFileFilter{
		formats:   []string{"mkv"},
		minSizeMB: 100,
		exclude: []domain.StrmExcludeKeyword{
			{Mode: domain.StrmExcludeModeEquals, Value: "Sample"},
			{Mode: domain.StrmExcludeModePrefix, Value: "cd2"},
			{Mode: domain.StrmExcludeModeSuffix, Value: "-Trailer"},
			{Mode: domain.StrmExcludeModeContains, Value: "rip"},
		},
	}
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"sample", true},               // 等于：完整名称，忽略大小写
		{"Sample.mkv", false},          // 等于不匹配更长的名称
		{"CD2-rip.mkv", true},          // 前缀：忽略大小写
		{"my-CD2.mkv", false},          // 前缀不匹配中间出现的关键字
		{"movie-Trailer", true},        // 后缀：忽略大小写
		{"movie-Trailer-2.mkv", false}, // 后缀不匹配结尾以外的位置
		{"my-rip.mkv", true},           // 包含
		{"movie.mkv", false},
	} {
		if got := filter.skipName(tc.name); got != tc.want {
			t.Fatalf("%s 的排除判定为 %v，期望 %v", tc.name, got, tc.want)
		}
	}

	const mb = 1024 * 1024
	if !filter.acceptFile("movie.mkv", 100*mb) {
		t.Fatal("达到最小体积的文件应保留")
	}
	if filter.acceptFile("movie.mkv", 100*mb-1) {
		t.Fatal("小于最小体积的文件应跳过")
	}
	if !filter.acceptFile("movie.mkv", 0) {
		t.Fatal("网盘未返回体积时不应按体积过滤")
	}
	if filter.acceptFile("movie.mp4", 100*mb) {
		t.Fatal("未选中的格式应跳过")
	}

	unlimited := newStrmFileFilter(domain.StrmMapping{Formats: []string{"mkv"}})
	if !unlimited.acceptFile("movie.mkv", 1) {
		t.Fatal("未设置最小体积时不应按体积过滤")
	}
	if unlimited.skipName("Sample.mkv") {
		t.Fatal("未设置排除关键字时不应跳过任何名称")
	}
}

// TestScanSkipsExcludedDirectoriesAndSmallFiles 验证生成 strm 时命中排除关键字的目录整棵跳过，
// 小文件不生成，而网盘未返回体积的文件仍然保留。
func TestScanSkipsExcludedDirectoriesAndSmallFiles(t *testing.T) {
	root := t.TempDir()
	const mb = 1024 * 1024
	pan115 := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"100": {Files: []domain.Pan115File{
			{ID: "200", Name: "Sample", IsDirectory: true},
			{ID: "f1", PickCode: "pc-1", Name: "A.mkv", Size: 200 * mb},
			{ID: "f2", Name: "B.trailer.mkv", Size: 200 * mb},
			{ID: "f3", Name: "C.mkv", Size: 10 * mb},
			{ID: "f4", PickCode: "pc-4", Name: "D.mkv"},
		}},
	}}
	values := map[string]string{
		"STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{{
			Kind: domain.StrmKindPan115, ID: "100", Path: "/影片", LocalPath: "/movies",
			MinSizeMB: 100, Exclude: []domain.StrmExcludeKeyword{
				{Mode: domain.StrmExcludeModeEquals, Value: "Sample"},
				{Mode: domain.StrmExcludeModeContains, Value: "trailer"},
			},
		}}),
		"STRM_PLAY_BASE": "http://bm.local",
	}
	service := newStrmTestService(t, root, pan115, nil, values)
	result, err := service.Scan(context.Background(), "", domain.StrmGenerateFull)
	if err != nil {
		t.Fatalf("生成 strm 失败: %v", err)
	}
	// stub 中不存在 "200"，命中排除关键字的目录若被进入会直接报错。
	if result.Files != 2 || result.Created != 2 || result.Failed != 0 {
		t.Fatalf("过滤结果不符: %+v", result)
	}
	for _, target := range []string{"movies/A.strm", "movies/D.strm"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(target))); err != nil {
			t.Fatalf("%s 应生成: %v", target, err)
		}
	}
	for _, target := range []string{"movies/Sample/A.strm", "movies/B.trailer.strm", "movies/C.strm"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(target))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s 不应生成", target)
		}
	}
}

// TestWalkCloudDriveAppliesFilter 验证 CloudDrive2 与 115 共用同一份排除与最小体积规则。
func TestWalkCloudDriveAppliesFilter(t *testing.T) {
	const mb = 1024 * 1024
	cloud := &strmCloudStub{configured: true, entries: map[string][]clouddrive.Entry{
		"/115/影片": {
			{Name: "Trailer", FullPath: "/115/影片/Trailer", Directory: true},
			{Name: "A.mkv", FullPath: "/115/影片/A.mkv", Size: 200 * mb},
			{Name: "B.mkv", FullPath: "/115/影片/B.mkv", Size: 1},
			{Name: "C.srt", FullPath: "/115/影片/C.srt", Size: 200 * mb},
		},
	}}
	service := newStrmTestService(t, t.TempDir(), nil, cloud, map[string]string{})
	var files []strmSourceFile
	err := service.walkCloudDrive(context.Background(), "/115/影片", strmFileFilter{
		formats:   []string{"mkv"},
		minSizeMB: 100,
		exclude:   []domain.StrmExcludeKeyword{{Mode: domain.StrmExcludeModePrefix, Value: "trailer"}},
	}, func(file strmSourceFile) error {
		files = append(files, file)
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 CloudDrive2 目录失败: %v", err)
	}
	if len(files) != 1 || files[0].Name != "A.mkv" || files[0].Directory != "" {
		t.Fatalf("过滤结果不符: %+v", files)
	}
}

// TestStrmNamesMatchSidecars 防止双后缀使本地海报、NFO 与 STRM 名称脱节。
func TestStrmNamesMatchSidecars(t *testing.T) {
	for _, name := range []string{"ABC-123.mp4", "ABC-123.MKV", "ABC-123.part1.mp4"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			service := newStrmTestService(t, root, nil, nil, nil)
			stem := "ABC-123"
			if strings.Contains(name, "part1") {
				stem = "ABC-123.part1"
			}
			for _, suffix := range []string{"-poster.jpg", ".nfo"} {
				if err := os.WriteFile(filepath.Join(root, stem+suffix), []byte("sidecar"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			source := strmSourceFile{ID: "1", PickCode: "pc", Name: name}
			for _, mode := range []domain.StrmGenerateMode{domain.StrmGenerateFull, domain.StrmGenerateIncremental} {
				if _, err := service.generateStrmFile(context.Background(), root, root, "115", "https://media.example.com", mode, source); err != nil {
					t.Fatal(err)
				}
			}
			content, err := os.ReadFile(filepath.Join(root, stem+".strm"))
			if err != nil || strings.TrimSpace(string(content)) != "https://media.example.com/files/play/115/pc" {
				t.Fatalf("content=%q err=%v", content, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 3 {
				t.Fatalf("entries=%v err=%v", entries, err)
			}
		})
	}
}

// TestScanRejectsSameStemVideos 防止不同格式的视频规范化后覆盖同一路径。
func TestScanRejectsSameStemVideos(t *testing.T) {
	root := t.TempDir()
	api := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{"1": {Files: []domain.Pan115File{
		{ID: "a", Name: "movie.mp4", PickCode: "a"}, {ID: "b", Name: "movie.mkv", PickCode: "b"},
	}}}}
	values := map[string]string{"STRM_PATHS": strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "1", Path: "/movies", LocalPath: "/movies"}})}
	svc := newStrmTestService(t, root, api, nil, values)
	result, err := svc.Scan(context.Background(), "https://media.example.com", domain.StrmGenerateFull)
	if err == nil || !strings.Contains(err.Error(), "文件名冲突") {
		t.Fatalf("expected collision, got %v", err)
	}
	if len(result.Mappings) != 1 || !strings.Contains(result.Mappings[0].Message, "文件名冲突") {
		t.Fatalf("result=%+v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, "movies", "movie.strm"))
	if !errors.Is(err, os.ErrNotExist) && (err != nil || strings.TrimSpace(string(data)) != "https://media.example.com/files/play/115/a") {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

// TestStrmIncrementalPreservesDuplicateVideos 验证已有标准名或旧名 STRM 在增量扫描中保留且不阻断后续文件。
func TestStrmIncrementalPreservesDuplicateVideos(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "legacy"}[legacy], func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "out")
			if err := os.MkdirAll(target, 0755); err != nil {
				t.Fatal(err)
			}
			names := []string{"movie.mp4", "movie.mp4"}
			outputs := []string{"movie.strm"}
			if legacy {
				names[1] = "movie.mkv"
				outputs = []string{"movie.mp4.strm", "movie.mkv.strm"}
			}
			for _, name := range outputs {
				if err := os.WriteFile(filepath.Join(target, name), []byte("preserved-"+name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			api := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{"root": {Files: []domain.Pan115File{
				{ID: "a", PickCode: "a", Name: names[0]}, {ID: "b", PickCode: "b", Name: names[1]}, {ID: "next", PickCode: "next", Name: "next.mp4"},
			}}}}
			service := newStrmTestService(t, root, api, nil, map[string]string{strmPathsSettingKey: strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "root", Path: "/root", LocalPath: "/out"}})})
			tree := newScanFileTree("output-names")
			ctx := context.WithValue(context.Background(), scanFileTreeKey{}, tree)
			result, err := service.Scan(ctx, "https://media.example.com", domain.StrmGenerateIncremental)
			if err != nil || result.Created != 1 || result.Unchanged != 2 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			for _, row := range tree.page(tree.page("", 1, 15, false).Items[0].ID, 1, 15, false).Items {
				if _, err := os.Stat(filepath.Join(target, row.Name)); err != nil {
					t.Fatalf("记录名称未对应实际文件：%s: %v", row.Name, err)
				}
			}
			for _, name := range outputs {
				body, err := os.ReadFile(filepath.Join(target, name))
				if err != nil || string(body) != "preserved-"+name {
					t.Fatalf("%s changed: %q %v", name, body, err)
				}
			}
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != len(outputs)+1 {
				t.Fatalf("unexpected outputs: %v %v", entries, err)
			}
		})
	}
}
