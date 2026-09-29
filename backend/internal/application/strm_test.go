package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/clouddrive"
)

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
	service, err := NewStrmService(root, pan115, cloud, strmTestSettings(values))
	if err != nil {
		t.Fatal(err)
	}
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
	for _, tc := range []struct{ name, raw string }{
		{"空值表示未配置", "  "},
		{"非数组", `{"kind":"115"}`},
		{"未知网盘类型", `[{"kind":"pan","id":"1","path":"/a","local_path":"/b"}]`},
		{"缺少网盘目录", `[{"kind":"115","id":"","path":"/a","local_path":"/b"}]`},
		{"本地路径非绝对", `[{"kind":"115","id":"1","path":"/a","local_path":"b"}]`},
		{"重复映射", `[{"kind":"115","id":"1","path":"/a","local_path":"/b"},{"kind":"115","id":"1","path":"/a","local_path":"/b"}]`},
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
	service := newStrmTestService(t, root, nil, nil, nil)
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
		absolute, normalized, err := service.resolveStrmPath(tc.relative)
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

// TestScanWritesPlayableStrmFiles 验证 115 与 CloudDrive2 映射都能生成内容正确的 strm，
// 且非媒体文件与重复扫描都不产生多余写入。
func TestScanWritesPlayableStrmFiles(t *testing.T) {
	root := t.TempDir()
	pan115 := &strmPan115Stub{pages: map[string]domain.Pan115FilePage{
		"100": {Files: []domain.Pan115File{
			{ID: "200", Name: "合集", IsDirectory: true},
			{ID: "f1", Name: "A.mkv"},
			{ID: "f2", Name: "readme.txt"},
		}},
		"200": {Files: []domain.Pan115File{{ID: "f3", Name: "C.mp4"}}},
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

	result, err := service.Scan(context.Background(), "")
	if err != nil {
		t.Fatalf("生成 strm 失败: %v", err)
	}
	if result.Files != 3 || result.Created != 3 || result.Failed != 0 {
		t.Fatalf("生成统计不符: %+v", result)
	}
	if result.Emby.Attempted {
		t.Fatalf("未开启自动刷新时不应请求 Emby: %+v", result.Emby)
	}
	for target, want := range map[string]string{
		"movies/A.mkv.strm":      "http://bm.local/files/play/115/f1\n",
		"movies/合集/C.mp4.strm":  "http://bm.local/files/play/115/f3\n",
		"cloud/S1/B.mkv.strm":    "http://bm.local/files/play/cd2/115/%E5%BD%B1%E7%89%87/S1/B.mkv\n",
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

	second, err := service.Scan(context.Background(), "")
	if err != nil {
		t.Fatalf("重复生成 strm 失败: %v", err)
	}
	if second.Created != 0 || second.Mappings[0].Unchanged != 2 || second.Mappings[1].Unchanged != 1 {
		t.Fatalf("重复扫描不是幂等的: %+v", second)
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
	result, err := service.Scan(context.Background(), "")
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
	target := filepath.Join(t.TempDir(), "nested", "A.mkv.strm")
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
