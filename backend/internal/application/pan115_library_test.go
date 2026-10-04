package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// libraryScanCall 记录一次 Files 调用，用于断言每页读取 1150 条与翻页偏移。
type libraryScanCall struct {
	directoryID string
	offset      int
	limit       int
}

// libraryScanPan115Stub 按目录 ID 分页返回预设文件；未登记或显式失败的目录返回错误。
type libraryScanPan115Stub struct {
	files   map[string][]domain.Pan115File
	failure map[string]error
	calls   []libraryScanCall
}

func (s *libraryScanPan115Stub) Files(_ context.Context, directoryID string, offset, limit int) (domain.Pan115FilePage, error) {
	s.calls = append(s.calls, libraryScanCall{directoryID: directoryID, offset: offset, limit: limit})
	if err := s.failure[directoryID]; err != nil {
		return domain.Pan115FilePage{}, err
	}
	all, ok := s.files[directoryID]
	if !ok {
		return domain.Pan115FilePage{}, fmt.Errorf("未知的 115 目录 %s", directoryID)
	}
	if offset > len(all) {
		offset = len(all)
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return domain.Pan115FilePage{DirectoryID: directoryID, Files: all[offset:end], Total: len(all), HasMore: end < len(all)}, nil
}

// libraryScanWriterStub 记录登记批次，并默认把批大小当作本次新建行数。
type libraryScanWriterStub struct {
	batches [][]ports.LibraryMediaItem
	err     error
}

func (s *libraryScanWriterStub) MarkLibraryPresent(_ context.Context, items []ports.LibraryMediaItem) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	batch := make([]ports.LibraryMediaItem, len(items))
	copy(batch, items)
	s.batches = append(s.batches, batch)
	return len(items), nil
}

// libraryScanSettings 返回固定设置快照；调用方按需覆盖键值。
func libraryScanSettings(values map[string]string) func(context.Context) (map[string]string, error) {
	return func(context.Context) (map[string]string, error) { return values, nil }
}

// libraryScanVideoFiles 生成 count 个可识别番号的视频文件；编号用 5 位，避免与分辨率标记冲突。
func libraryScanVideoFiles(prefix string, count int) []domain.Pan115File {
	files := make([]domain.Pan115File, 0, count)
	for index := 1; index <= count; index++ {
		files = append(files, domain.Pan115File{ID: fmt.Sprintf("%s-%d", prefix, index), Name: fmt.Sprintf("%s-%05d.mp4", prefix, index)})
	}
	return files
}

func newLibraryScanService(t *testing.T, pan115 pan115FileAPI, writer ports.MediaLibraryWriter, values map[string]string) *Pan115LibraryService {
	t.Helper()
	service, err := NewPan115LibraryService(pan115, writer, libraryScanSettings(values))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// TestPan115LibraryScanRequiresConfiguredDirectories 覆盖未配置扫描目录时拒绝扫描，避免空跑。
func TestPan115LibraryScanRequiresConfiguredDirectories(t *testing.T) {
	service := newLibraryScanService(t, &libraryScanPan115Stub{}, &libraryScanWriterStub{}, map[string]string{})
	if _, err := service.Scan(context.Background()); !errors.Is(err, ErrPan115ScanNotConfigured) {
		t.Fatalf("未配置扫描目录应返回 ErrPan115ScanNotConfigured，实际 %v", err)
	}
}

// TestPan115LibraryScanRejectsInvalidSetting 覆盖配置非法时返回可映射为 400 的错误。
func TestPan115LibraryScanRejectsInvalidSetting(t *testing.T) {
	service := newLibraryScanService(t, &libraryScanPan115Stub{}, &libraryScanWriterStub{}, map[string]string{
		pan115ScanPathsSettingKey: `[{"id":"","path":"/影片"}]`,
	})
	if _, err := service.Scan(context.Background()); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("非法扫描目录应返回 ErrInvalidSetting，实际 %v", err)
	}
}

// TestPan115LibraryScanRequiresPan115Service 覆盖 115 未装配时返回未绑定错误。
func TestPan115LibraryScanRequiresPan115Service(t *testing.T) {
	service := newLibraryScanService(t, nil, &libraryScanWriterStub{}, map[string]string{
		pan115ScanPathsSettingKey: `[{"id":"1","path":"/影片"}]`,
	})
	if _, err := service.Scan(context.Background()); !errors.Is(err, ErrPan115NotLinked) {
		t.Fatalf("115 未装配应返回 ErrPan115NotLinked，实际 %v", err)
	}
}

// TestPan115LibraryScanRecursesWithPageLimit 覆盖「扫描目录 → 递归扫描 → 视频入库」全链路：
// 每页固定读取 1150 条并按 has_more 翻页，子目录递归，非视频文件被格式过滤跳过。
func TestPan115LibraryScanRecursesWithPageLimit(t *testing.T) {
	rootFiles := libraryScanVideoFiles("SSIS", 1155)
	rootFiles = append(rootFiles, domain.Pan115File{ID: "cover", Name: "cover.jpg"})
	rootFiles = append(rootFiles, domain.Pan115File{ID: "sub", Name: "子目录", IsDirectory: true})
	stub := &libraryScanPan115Stub{files: map[string][]domain.Pan115File{
		"1": rootFiles,
		"sub": {
			{ID: "a", Name: "ABP-00001.mkv"},
			{ID: "b", Name: "ABP-00002.mkv"},
			{ID: "c", Name: "note.txt"},
		},
	}}
	writer := &libraryScanWriterStub{}
	service := newLibraryScanService(t, stub, writer, map[string]string{
		pan115ScanPathsSettingKey: `[{"id":"1","path":"/影片"}]`,
	})

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 1156 条目录内容分两页，第二页从 1150 开始；子目录在第二页被递归。
	if len(stub.calls) != 3 {
		t.Fatalf("Files 调用次数 %d，期望 3（根目录两页 + 子目录一页）：%+v", len(stub.calls), stub.calls)
	}
	if stub.calls[0] != (libraryScanCall{directoryID: "1", offset: 0, limit: pan115FilePageLimit}) {
		t.Fatalf("首页调用 %+v，期望每页 %d 条", stub.calls[0], pan115FilePageLimit)
	}
	if stub.calls[1] != (libraryScanCall{directoryID: "1", offset: 1150, limit: pan115FilePageLimit}) {
		t.Fatalf("翻页调用 %+v，期望 offset=1150", stub.calls[1])
	}
	if stub.calls[2] != (libraryScanCall{directoryID: "sub", offset: 0, limit: pan115FilePageLimit}) {
		t.Fatalf("子目录调用 %+v", stub.calls[2])
	}
	if got, want := result.Files, 1157; got != want {
		t.Fatalf("视频文件数 %d，期望 %d", got, want)
	}
	if got, want := result.Matched, 1157; got != want {
		t.Fatalf("识别番号数 %d，期望 %d", got, want)
	}
	if result.Skipped != 0 {
		t.Fatalf("不应有跳过文件，实际 %d", result.Skipped)
	}
	if got, want := result.Created, 1157; got != want {
		t.Fatalf("新建影片数 %d，期望 %d", got, want)
	}
	// 边扫描边入库：1157 部影片按 libraryMarkBatchSize 分批提交，而不是遍历结束后一次写入。
	if got, want := len(writer.batches), (1157+libraryMarkBatchSize-1)/libraryMarkBatchSize; got != want {
		t.Fatalf("登记批次数 %d，期望 %d", got, want)
	}
	registered := 0
	for _, batch := range writer.batches {
		if len(batch) > libraryMarkBatchSize {
			t.Fatalf("单批 %d 条超过上限 %d", len(batch), libraryMarkBatchSize)
		}
		registered += len(batch)
	}
	if registered != 1157 {
		t.Fatalf("登记影片数 %d，期望 1157", registered)
	}
	if got := writer.batches[0][0]; got.Code != "SSIS-00001" || got.Title != "SSIS-00001" {
		t.Fatalf("登记项不符：%+v", got)
	}
}

// TestPan115LibraryScanDeduplicatesCodeWithinDirectory 覆盖同番号多文件只登记一次，无法识别番号的文件计入跳过。
func TestPan115LibraryScanDeduplicatesCodeWithinDirectory(t *testing.T) {
	stub := &libraryScanPan115Stub{files: map[string][]domain.Pan115File{
		"1": {
			{ID: "a", Name: "SSIS-00001.mp4"},
			{ID: "b", Name: "SSIS-00001.part2.mkv"},
			{ID: "c", Name: "sample_video.mp4"},
		},
	}}
	writer := &libraryScanWriterStub{}
	service := newLibraryScanService(t, stub, writer, map[string]string{
		pan115ScanPathsSettingKey: `[{"id":"1","path":"/影片"}]`,
	})

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Files, 3; got != want {
		t.Fatalf("视频文件数 %d，期望 %d", got, want)
	}
	if got, want := result.Matched, 1; got != want {
		t.Fatalf("同番号应只登记一次，识别数 %d，期望 %d", got, want)
	}
	if got, want := result.Skipped, 1; got != want {
		t.Fatalf("无法识别番号的文件应计入跳过，实际 %d，期望 %d", got, want)
	}
	if len(writer.batches[0]) != 1 || writer.batches[0][0].Code != "SSIS-00001" {
		t.Fatalf("登记批次不符：%+v", writer.batches)
	}
}

// TestPan115LibraryScanReportsWriterFailure 覆盖写库失败时按目录返回原因，不静默丢弃。
func TestPan115LibraryScanReportsWriterFailure(t *testing.T) {
	stub := &libraryScanPan115Stub{files: map[string][]domain.Pan115File{
		"1": {{ID: "a", Name: "SSIS-00001.mp4"}},
	}}
	writer := &libraryScanWriterStub{err: errors.New("数据库不可用")}
	service := newLibraryScanService(t, stub, writer, map[string]string{
		pan115ScanPathsSettingKey: `[{"id":"1","path":"/影片"}]`,
	})

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 0 || result.Matched != 1 {
		t.Fatalf("写库失败时不应计新建：%+v", result)
	}
	if got, want := result.Directories[0].Message, "写入媒体库失败：数据库不可用"; got != want {
		t.Fatalf("失败原因 %q，期望 %q", got, want)
	}
}

// TestPan115LibraryScanIsolatesDirectoryFailure 覆盖单目录失败不影响其他目录，失败原因按目录返回。
func TestPan115LibraryScanIsolatesDirectoryFailure(t *testing.T) {
	stub := &libraryScanPan115Stub{
		files: map[string][]domain.Pan115File{
			"2": {{ID: "b", Name: "ABP-00001.mkv"}},
		},
		failure: map[string]error{"1": errors.New("115 目录读取失败")},
	}
	writer := &libraryScanWriterStub{}
	service := newLibraryScanService(t, stub, writer, map[string]string{
		pan115ScanPathsSettingKey: `[{"id":"1","path":"/影片"},{"id":"2","path":"/剧集"}]`,
	})

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Directories) != 2 {
		t.Fatalf("两个目录都应返回结果，实际 %d", len(result.Directories))
	}
	if got, want := result.Directories[0].Message, "115 目录读取失败"; got != want {
		t.Fatalf("失败目录原因 %q，期望 %q", got, want)
	}
	if got, want := result.Directories[1].Created, 1; got != want {
		t.Fatalf("失败目录不应影响其他目录，实际新建 %d，期望 %d", got, want)
	}
	if result.Created != 1 || result.Files != 1 || result.Matched != 1 {
		t.Fatalf("汇总结果不符：%+v", result)
	}
}
