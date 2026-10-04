package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

// ErrPan115ScanNotConfigured 表示设置页尚未配置任何 115 扫描目录。
var ErrPan115ScanNotConfigured = errors.New("尚未配置 115 扫描目录")

// Pan115LibraryService 把 PAN115_SCAN_PATHS 里的 115 目录递归扫描后登记到媒体库。
//
// 它只依赖 115 目录读取与媒体库登记两个端口：扫描不触发采集、订阅或下载，
// 因此「已在网盘里」的影片只被标记为媒体库存在，不会被当成订阅目标重新下载。
type Pan115LibraryService struct {
	pan115   pan115FileAPI
	library  ports.MediaLibraryWriter
	settings func(context.Context) (map[string]string, error)
}

// NewPan115LibraryService 组装 115 扫描入库服务；pan115 为 nil 时扫描会直接报告未绑定。
func NewPan115LibraryService(pan115 pan115FileAPI, library ports.MediaLibraryWriter, settings func(context.Context) (map[string]string, error)) (*Pan115LibraryService, error) {
	if library == nil {
		return nil, fmt.Errorf("media library writer is required")
	}
	if settings == nil {
		return nil, fmt.Errorf("settings loader is required")
	}
	// 具体类型为 nil 时接口本身不为 nil，这里显式归一化，避免运行期空指针。
	if service, ok := pan115.(*Pan115Service); ok && service == nil {
		pan115 = nil
	}
	return &Pan115LibraryService{pan115: pan115, library: library, settings: settings}, nil
}

// Scan 递归扫描每个已配置的 115 目录，把识别出番号的视频登记为「已在媒体库」。
//
// 视频格式沿用生成 strm 的同一份默认格式表，避免两条链路对「什么算视频」判断不一致。
// 单个目录失败只记录在该目录的 Message 里，不影响其他目录，与生成 strm 的失败隔离方式一致。
func (s *Pan115LibraryService) Scan(ctx context.Context) (domain.Pan115LibraryScanResult, error) {
	values, err := s.settings(ctx)
	if err != nil {
		return domain.Pan115LibraryScanResult{}, fmt.Errorf("读取扫描目录配置失败: %w", err)
	}
	directories, err := parsePan115ScanPaths(values[pan115ScanPathsSettingKey])
	if err != nil {
		return domain.Pan115LibraryScanResult{}, fmt.Errorf("%w: %s %s", ErrInvalidSetting, pan115ScanPathsSettingKey, err)
	}
	if len(directories) == 0 {
		return domain.Pan115LibraryScanResult{}, ErrPan115ScanNotConfigured
	}
	if s.pan115 == nil {
		return domain.Pan115LibraryScanResult{}, fmt.Errorf("%w: 115 网盘服务尚未就绪", ErrPan115NotLinked)
	}
	result := domain.Pan115LibraryScanResult{Directories: make([]domain.Pan115LibraryDirectoryResult, 0, len(directories))}
	total, processed := 0, 0
	for _, directory := range directories {
		if err := scanCheckpoint(ctx); err != nil {
			return result, err
		}
		reportScanProgress(ctx, "discovering", processed, total, directory.Path)
		discoveryCtx := withScanDiscovery(ctx, func() {
			total++
			reportScanProgress(ctx, "discovering", processed, total, directory.Path)
		})
		// 115 限流时把冷却反馈到任务进度：冷却期间不再产生新请求，任务仍在运行。
		discoveryCtx = pan115.WithCooldownReporter(discoveryCtx, func(wait time.Duration) {
			reportScanProgress(ctx, "cooling", processed, total, pan115CooldownNotice(directory.Path, wait))
		})
		// 扫描入库仍沿用先收集后入库：整目录文件集是识别影片的输入，缺一个文件就会漏片。
		var files []strmSourceFile
		scanErr := walkPan115Files(discoveryCtx, s.pan115, directory.ID, strmFileFilter{formats: defaultStrmFormats}, func(file strmSourceFile) error {
			files = append(files, file)
			return nil
		})
		if err := scanCheckpoint(ctx); err != nil {
			return result, err
		}
		reportScanProgress(ctx, "processing", processed, total, directory.Path)
		entry := s.scanDirectory(ctx, directory, files, scanErr, func() {
			processed++
			reportScanProgress(ctx, "processing", processed, total, directory.Path)
		})
		result.Directories = append(result.Directories, entry)
		result.Files += entry.Files
		result.Matched += entry.Matched
		result.Created += entry.Created
		result.Skipped += entry.Skipped
	}
	if err := scanCheckpoint(ctx); err != nil {
		return result, err
	}
	reportScanProgress(ctx, "completed", processed, total, "")
	return result, nil
}

// scanDirectory 递归扫描一个 115 目录并把识别出的影片登记入库。
// 同番号在一个目录里只登记一次：分卷、多格式文件属于同一部影片，媒体库按番号唯一。
func (s *Pan115LibraryService) scanDirectory(ctx context.Context, directory pan115ScanPath, files []strmSourceFile, err error, advance func()) domain.Pan115LibraryDirectoryResult {
	entry := domain.Pan115LibraryDirectoryResult{ID: directory.ID, Path: directory.Path}
	if err != nil {
		entry.Message = err.Error()
		for range files {
			advance()
		}
		return entry
	}
	entry.Files = len(files)
	items := make([]ports.LibraryMediaItem, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		if err := scanCheckpoint(ctx); err != nil {
			entry.Message = err.Error()
			return entry
		}
		code := domain.ExtractCode(file.Name)
		if code == "" {
			entry.Skipped++
			advance()
			continue
		}
		if seen[code] {
			advance()
			continue
		}
		seen[code] = true
		// 标题兜底用去掉扩展名的文件名；真正的标题由采集链路补齐，这里不覆盖已有值。
		title := strings.TrimSpace(strings.TrimSuffix(file.Name, filepath.Ext(file.Name)))
		items = append(items, ports.LibraryMediaItem{
			Code:      code,
			Title:     title,
			VideoType: domain.ClassifyVideoType(code, title, nil),
		})
		advance()
	}
	entry.Matched = len(items)
	if entry.Matched == 0 {
		return entry
	}
	if err := scanCheckpoint(ctx); err != nil {
		entry.Message = err.Error()
		return entry
	}
	created, err := s.library.MarkLibraryPresent(ctx, items)
	if err != nil {
		entry.Message = "写入媒体库失败：" + err.Error()
		return entry
	}
	entry.Created = created
	return entry
}
