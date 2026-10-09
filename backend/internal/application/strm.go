package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/clouddrive"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

var (
	// ErrStrmInvalidInput 表示调用方传入的 strm 路径或目录名不合法。
	ErrStrmInvalidInput = errors.New("strm 请求参数无效")
	// ErrStrmNotConfigured 表示 strm 映射或网盘集成尚未配置齐全。
	ErrStrmNotConfigured = errors.New("strm 尚未配置")
	// ErrStrmRootUnavailable 表示容器内 strm 根目录无法创建、读取或写入。
	// 这是部署问题而不是请求参数问题：容器必须把可写目录挂载到 /strm，且该挂载不能是只读。
	ErrStrmRootUnavailable = errors.New("strm 根目录不可用")
)

const (
	// strmListLimit 是递归读取 115 目录时每页的条目数；与扫描入库共用 115 单页上限，减少请求次数。
	strmListLimit = pan115FilePageLimit
	// strmRootDefault 是容器内 strm 根目录的默认挂载点，与 README、Compose 的 /strm 保持一致。
	strmRootDefault = "/strm"
	// strmRequestTimeout 是 strm 服务对外请求（Emby 媒体库刷新）的超时。
	strmRequestTimeout = 30 * time.Second
	// strmPlayPrefix 是 strm 内容使用的播放地址前缀；Emby 等播放器直接请求，不经过 /api 前缀。
	strmPlayPrefix = "/files/play/"
)

// defaultStrmFormats 是历史映射没有 formats 字段时使用的默认格式；顺序与设置页保持一致。
var defaultStrmFormats = []string{
	"mp4", "avi", "rmvb", "wmv", "mov", "mkv", "webm", "iso",
	"mpg", "m4v", "ts", "flv", "strm", "vob", "m2ts",
}

// strmPan115API 是 strm 生成与播放需要的 115 能力。
type strmPan115API interface {
	Files(ctx context.Context, directoryID string, offset, limit int) (domain.Pan115FilePage, error)
	PlayURL(ctx context.Context, pickCode, userAgent string) (string, error)
}

// strmCloudDriveAPI 是 strm 生成与播放需要的 CloudDrive2 能力。
type strmCloudDriveAPI interface {
	Configured(ctx context.Context) bool
	ListSubFiles(ctx context.Context, path string) ([]clouddrive.Entry, error)
	DownloadURL(ctx context.Context, path string, direct bool) (clouddrive.Download, error)
}

// strmSourceFile 是待写入 strm 的一个网盘文件。
// Directory 是从映射根目录到该文件所在目录的相对路径（以 / 分隔，根目录为空串）。
type strmSourceFile struct {
	ParentID  string
	Ancestors []string
	ID        string
	PickCode  string
	Name      string
	Directory string
}

// strmFileFilter 是递归扫描网盘目录时的过滤条件：媒体格式、最小体积与排除关键字。
// 生成 strm 用映射里的完整条件；扫描入库只关心格式，因此用同一份实现传入不同条件，
// 避免「什么算视频」与「跳过哪些名称」在两条链路上各写一套判定。
type strmFileFilter struct {
	formats         []string
	minSizeMB       int
	exclude         []domain.StrmExcludeKeyword
	downloadFormats []string
}

// newStrmFileFilter 由一条映射构造过滤器；formats 与 exclude 已由 parseStrmMappings 规范化。
func newStrmFileFilter(mapping domain.StrmMapping) strmFileFilter {
	return strmFileFilter{formats: mapping.Formats, minSizeMB: mapping.MinSizeMB, exclude: mapping.Exclude}
}

// skipName 判断名称是否命中排除规则。四条规则共用这一份实现，匹配一律不区分大小写；
// 文件夹名命中时调用方会整棵子树跳过。
func (f strmFileFilter) skipName(name string) bool {
	if len(f.exclude) == 0 {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	for _, rule := range f.exclude {
		value := strings.ToLower(rule.Value)
		switch rule.Mode {
		case domain.StrmExcludeModeEquals:
			if lower == value {
				return true
			}
		case domain.StrmExcludeModePrefix:
			if strings.HasPrefix(lower, value) {
				return true
			}
		case domain.StrmExcludeModeSuffix:
			if strings.HasSuffix(lower, value) {
				return true
			}
		default:
			if strings.Contains(lower, value) {
				return true
			}
		}
	}
	return false
}

// acceptFile 判断一个文件是否应生成 strm：格式匹配、未命中排除关键字、体积不小于下限。
// sizeBytes 为 0 表示网盘未返回体积，此时不按体积过滤，避免把整个目录误判成小文件而全部跳过。
func (f strmFileFilter) acceptFile(name string, sizeBytes int64) bool {
	if f.skipName(name) {
		return false
	}
	if isStrmMedia(name, f.downloadFormats) {
		return true
	}
	if !isStrmMedia(name, f.formats) {
		return false
	}
	if f.minSizeMB <= 0 || sizeBytes <= 0 {
		return true
	}
	return sizeBytes >= int64(f.minSizeMB)*1024*1024
}

// StrmService 把网盘目录镜像成本地 strm 文件，并为播放请求解析真实地址。
// 目录浏览始终以固定 /strm 根目录为界，调用方无法访问根目录以外的路径。
type StrmService struct {
	// scanMu 串行化手动与事件生成，防止并发写入同一映射。
	scanMu sync.Mutex
	// root 是生产环境固定的 /strm；测试可在同包内替换为临时目录隔离文件。
	root             string
	pan115           strmPan115API
	cloud            strmCloudDriveAPI
	settings         func(context.Context) (map[string]string, error)
	http             *http.Client
	embyMediaEnqueue func(context.Context)
	managedFiles     ports.StrmFileRepository
	managedAccount   func(context.Context) (string, error)
}

// NewStrmService 组装固定使用 /strm 根目录的 strm 服务。
// cloud 未配置时只支持 115，pan115 为 nil 时只支持 CloudDrive2。
func NewStrmService(pan115 strmPan115API, cloud strmCloudDriveAPI, settings func(context.Context) (map[string]string, error)) (*StrmService, error) {
	if settings == nil {
		return nil, fmt.Errorf("strm settings loader is required")
	}
	// 具体类型为 nil 时接口本身不为 nil，这里显式归一化，避免运行期空指针。
	if service, ok := pan115.(*Pan115Service); ok && service == nil {
		pan115 = nil
	}
	return &StrmService{
		root:     strmRootDefault,
		pan115:   pan115,
		cloud:    cloud,
		settings: settings,
		http:     &http.Client{Timeout: strmRequestTimeout},
	}, nil
}

// SetEmbyMediaEnqueue 注入媒体信息预热队列；生成流程只负责提交，不等待探测完成。
func (s *StrmService) SetEmbyMediaEnqueue(enqueue func(context.Context)) {
	s.embyMediaEnqueue = enqueue
}

// Root 返回固定的本地 strm 根目录，不读取数据库设置。
func (s *StrmService) Root(_ context.Context) (string, error) {
	return s.root, nil
}

// Directories 列出 strm 根目录下某个目录的直接子目录。
// 每次调用都实时读盘，因此外部新建的目录只要重新请求就能看到，不需要额外的缓存失效逻辑。
func (s *StrmService) Directories(ctx context.Context, relative string) (domain.StrmDirectoryPage, error) {
	root, err := s.Root(ctx)
	if err != nil {
		return domain.StrmDirectoryPage{}, err
	}
	absolute, normalized, err := resolveStrmPath(root, relative)
	if err != nil {
		return domain.StrmDirectoryPage{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return domain.StrmDirectoryPage{}, strmRootFailure("创建", root, err)
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return domain.StrmDirectoryPage{}, fmt.Errorf("%w: strm 目录不存在", ErrStrmInvalidInput)
		}
		return domain.StrmDirectoryPage{}, strmRootFailure("读取", absolute, err)
	}
	page := domain.StrmDirectoryPage{Path: normalized, Directories: make([]domain.StrmDirectory, 0, len(entries))}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		page.Directories = append(page.Directories, domain.StrmDirectory{
			Name: entry.Name(),
			Path: joinStrmPath(normalized, entry.Name()),
		})
	}
	sort.Slice(page.Directories, func(i, j int) bool { return page.Directories[i].Name < page.Directories[j].Name })
	return page, nil
}

// CreateDirectory 在 strm 根目录内的 parent 下新建一级目录。
// 只创建一级：上级目录必须已经存在，避免一次请求产生难以排查的隐式目录树。
func (s *StrmService) CreateDirectory(ctx context.Context, parent, name string) (domain.StrmDirectory, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." ||
		strings.ContainsAny(trimmed, `/\`) || strings.ContainsRune(trimmed, 0) {
		return domain.StrmDirectory{}, fmt.Errorf("%w: 目录名不能为空，且不能包含路径分隔符", ErrStrmInvalidInput)
	}
	root, err := s.Root(ctx)
	if err != nil {
		return domain.StrmDirectory{}, err
	}
	// 根目录缺失或不可写时先报明确的部署问题，而不是把错误说成“上级目录不存在”。
	if err := os.MkdirAll(root, 0o755); err != nil {
		return domain.StrmDirectory{}, strmRootFailure("创建", root, err)
	}
	absoluteParent, normalized, err := resolveStrmPath(root, parent)
	if err != nil {
		return domain.StrmDirectory{}, err
	}
	target := filepath.Join(absoluteParent, trimmed)
	if !withinStrmRoot(root, target) {
		return domain.StrmDirectory{}, fmt.Errorf("%w: 路径超出 strm 根目录", ErrStrmInvalidInput)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		switch {
		case errors.Is(err, fs.ErrExist):
			return domain.StrmDirectory{}, fmt.Errorf("%w: 目录已存在", ErrStrmInvalidInput)
		case errors.Is(err, fs.ErrNotExist):
			return domain.StrmDirectory{}, fmt.Errorf("%w: 上级目录不存在", ErrStrmInvalidInput)
		}
		return domain.StrmDirectory{}, strmRootFailure("创建", target, err)
	}
	return domain.StrmDirectory{Name: trimmed, Path: joinStrmPath(normalized, trimmed)}, nil
}

// CloudDriveDirectories 列出 CloudDrive2 中某个目录的直接子目录，供设置页选择网盘路径。
// 数据来自网盘，因此不受本地 strm 根目录限制；只返回目录，文件不参与路径选择。
func (s *StrmService) CloudDriveDirectories(ctx context.Context, directory string) (domain.StrmDirectoryPage, error) {
	if s.cloud == nil || !s.cloud.Configured(ctx) {
		return domain.StrmDirectoryPage{}, fmt.Errorf("%w: CloudDrive2 尚未配置", ErrStrmNotConfigured)
	}
	current := normalizeCloudPath(directory)
	entries, err := s.cloud.ListSubFiles(ctx, current)
	if err != nil {
		return domain.StrmDirectoryPage{}, err
	}
	page := domain.StrmDirectoryPage{Path: current, Directories: make([]domain.StrmDirectory, 0, len(entries))}
	for _, item := range entries {
		if !item.Directory {
			continue
		}
		full := strings.TrimSpace(item.FullPath)
		if full == "" {
			full = joinCloudPath(current, item.Name)
		}
		page.Directories = append(page.Directories, domain.StrmDirectory{Name: item.Name, Path: full})
	}
	sort.Slice(page.Directories, func(i, j int) bool { return page.Directories[i].Name < page.Directories[j].Name })
	return page, nil
}

// Scan 按 STRM_PATHS 配置把网盘目录镜像成本地 strm 文件，并按开关触发 Emby 刷新。
// playBase 是调用方观测到的 ByteMuse 对外基址，仅在未配置 STRM_PLAY_BASE 时兜底。
// mode 决定本地已有文件的处理方式：全量先清理映射本地目录下的 strm 内容再重建，
// 增量保留本地文件、只补齐缺失项；两种方式共用同一套映射、过滤与写入规则。
func (s *StrmService) Scan(ctx context.Context, playBase string, mode domain.StrmGenerateMode) (domain.StrmScanResult, error) {
	logging.Info(logging.CategoryStrmGenerate, "生成 STRM 开始扫描", "mode", string(mode))
	reportScanProgress(ctx, "waiting", 0, 0, "")
	// 等待事件生成释放互斥时仍响应任务暂停和取消。
	for !s.scanMu.TryLock() {
		if err := scanCheckpoint(ctx); err != nil {
			return domain.StrmScanResult{}, err
		}
		select {
		case <-ctx.Done():
			return domain.StrmScanResult{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer s.scanMu.Unlock()
	if err := scanCheckpoint(ctx); err != nil {
		return domain.StrmScanResult{}, err
	}
	values, err := s.settings(ctx)
	if err != nil {
		return domain.StrmScanResult{}, fmt.Errorf("读取 strm 配置失败: %w", err)
	}
	inputs := map[string]string{}
	for _, key := range []string{strmPathsSettingKey, strmDownloadEnableSettingKey, strmDownloadExtensionsSettingKey, strmPlayBaseSettingKey} {
		inputs[key] = values[key]
	}
	if _, err := loadTaskCheckpoint(ctx, journalKey("input", "strm-settings"), &inputs); err != nil {
		return domain.StrmScanResult{}, err
	}
	frozen := make(map[string]string, len(values))
	for key, value := range values {
		frozen[key] = value
	}
	for key, value := range inputs {
		frozen[key] = value
	}
	values = frozen
	mappings, err := parseStrmMappings(values[strmPathsSettingKey])
	if err != nil {
		return domain.StrmScanResult{}, fmt.Errorf("%w: %s %s", ErrInvalidSetting, strmPathsSettingKey, err)
	}
	if len(mappings) == 0 {
		return domain.StrmScanResult{}, fmt.Errorf("%w: 尚未配置网盘映射", ErrStrmNotConfigured)
	}
	downloadFormats, err := strmDownloadFormats(values)
	if err != nil {
		return domain.StrmScanResult{}, fmt.Errorf("%w: %s", ErrInvalidSetting, err)
	}
	root := s.root
	base := strings.TrimRight(strings.TrimSpace(values[strmPlayBaseSettingKey]), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(playBase), "/")
	}
	if _, err := loadTaskCheckpoint(ctx, journalKey("input", "strm-base"), &base); err != nil {
		return domain.StrmScanResult{}, err
	}
	if base == "" {
		return domain.StrmScanResult{}, fmt.Errorf("%w: 缺少 ByteMuse 访问地址，无法生成 strm 内容", ErrStrmNotConfigured)
	}
	if err := taskInput(ctx, "strm-settings", &inputs); err != nil {
		return domain.StrmScanResult{}, err
	}
	if err := taskInput(ctx, "strm-base", &base); err != nil {
		return domain.StrmScanResult{}, err
	}
	result := domain.StrmScanResult{Mappings: make([]domain.StrmScanMapping, 0, len(mappings))}
	total, processed := 0, 0
	for _, mapping := range mappings {
		if err := scanCheckpoint(ctx); err != nil {
			return result, err
		}
		// 边扫描边写：先确认网盘来源可用（不发起请求），再清理本地目录，
		// 然后在遍历过程中逐个写入，使限流或中断只影响尚未扫描到的部分。
		walk, walkErr := s.mappingWalk(ctx, mapping, downloadFormats)
		if walkErr != nil {
			result.Mappings = append(result.Mappings, strmMappingFailure(mapping, walkErr))
			continue
		}
		reportScanProgress(ctx, "processing", processed, total, mapping.Path)
		walkCtx := withScanDiscovery(ctx, func() {
			total++
			reportScanProgress(ctx, "processing", processed, total, mapping.Path)
		})
		// 115 限流时把冷却反馈到任务进度：冷却期间不再产生新请求，任务仍在运行。
		walkCtx = pan115.WithCooldownReporter(walkCtx, func(wait time.Duration) {
			reportScanProgress(ctx, "cooling", processed, total, pan115CooldownNotice(mapping.Path, wait))
		})
		entry := s.scanMapping(walkCtx, root, mapping, base, mode, walk, func() {
			processed++
			reportScanProgress(ctx, "processing", processed, total, mapping.Path)
		}, downloadFormats)
		if err := scanCheckpoint(ctx); err != nil {
			return result, err
		}
		result.Mappings = append(result.Mappings, entry)
		result.Files += entry.Files
		result.Deleted += entry.Deleted
		result.Created += entry.Created
		result.Unchanged += entry.Unchanged
		result.Failed += entry.Failed
		result.Downloaded += entry.Downloaded
		result.DownloadSkipped += entry.DownloadSkipped
		result.DownloadFailed += entry.DownloadFailed
	}
	reportScanProgress(ctx, "finalizing", processed, total, "")
	if err := scanCheckpoint(ctx); err != nil {
		logging.Error(logging.CategoryStrmGenerate, "生成 STRM 已取消", "mode", string(mode), "error", err.Error())
		return result, err
	}
	result.Emby = s.refreshEmby(ctx, values)
	if err := scanCheckpoint(ctx); err != nil {
		return result, err
	}
	reportScanProgress(ctx, "completed", processed, total, "")
	logging.Info(logging.CategoryStrmGenerate, "生成 STRM 完成", "mode", string(mode), "files", result.Files, "created", result.Created, "failed", result.Failed, "download_failed", result.DownloadFailed)
	return result, nil
}

// PlayURL 把播放地址里的网盘标识解析成真实地址。
// 115 直链绑定换取时的 User-Agent，因此必须使用调用方（播放器）的 UA，返回 302 目标；
// CloudDrive2 直链可能要求固定请求头，302 无法携带，此时返回需要服务端转发的目标。
func (s *StrmService) PlayURL(ctx context.Context, kind, fileID, userAgent string) (domain.StrmPlayTarget, error) {
	identifier := strings.TrimSpace(fileID)
	if identifier == "" {
		return domain.StrmPlayTarget{}, fmt.Errorf("%w: 缺少网盘文件标识", ErrStrmInvalidInput)
	}
	switch strings.TrimSpace(kind) {
	case domain.StrmKindPan115:
		if s.pan115 == nil {
			return domain.StrmPlayTarget{}, fmt.Errorf("%w: 115 网盘服务尚未就绪", ErrStrmNotConfigured)
		}
		address, err := s.pan115.PlayURL(ctx, identifier, userAgent)
		if err != nil {
			return domain.StrmPlayTarget{}, err
		}
		return domain.StrmPlayTarget{Redirect: address}, nil
	case domain.StrmKindCloudDrive2:
		if s.cloud == nil || !s.cloud.Configured(ctx) {
			return domain.StrmPlayTarget{}, fmt.Errorf("%w: CloudDrive2 尚未配置", ErrStrmNotConfigured)
		}
		download, err := s.cloud.DownloadURL(ctx, identifier, true)
		if err != nil {
			return domain.StrmPlayTarget{}, err
		}
		if strings.TrimSpace(download.UserAgent) == "" && len(download.Headers) == 0 {
			return domain.StrmPlayTarget{Redirect: download.URL}, nil
		}
		return domain.StrmPlayTarget{Proxy: &domain.StrmProxyTarget{
			URL:       download.URL,
			UserAgent: download.UserAgent,
			Headers:   download.Headers,
		}}, nil
	default:
		return domain.StrmPlayTarget{}, fmt.Errorf("%w: 不支持的网盘类型 %q", ErrStrmInvalidInput, kind)
	}
}

// strmFileVisit 接收一个符合过滤条件的媒体文件；返回错误表示调用方要求停止遍历。
type strmFileVisit func(strmSourceFile) error

// strmWalk 是一条映射的网盘遍历入口：对每个符合过滤条件的媒体文件调用 visit。
// 生成 strm 与 115 事件同步共用它，使过滤、分页与中断规则只有一处实现。
type strmWalk func(ctx context.Context, visit strmFileVisit) error

// mappingWalk 校验一条映射的网盘来源并返回遍历入口。
// 校验发生在清理本地目录之前：来源不可用时只报告原因，不清空本地已有的 strm 文件。
func (s *StrmService) mappingWalk(ctx context.Context, mapping domain.StrmMapping, downloadFormats []string) (strmWalk, error) {
	filter := newStrmFileFilter(mapping)
	filter.downloadFormats = downloadFormats
	switch mapping.Kind {
	case domain.StrmKindPan115:
		if s.pan115 == nil {
			return nil, errors.New("115 网盘服务尚未就绪")
		}
		api := s.pan115
		return func(walkCtx context.Context, visit strmFileVisit) error {
			return walkPan115Files(walkCtx, api, mapping.ID, filter, visit)
		}, nil
	case domain.StrmKindCloudDrive2:
		if s.cloud == nil || !s.cloud.Configured(ctx) {
			return nil, errors.New("CloudDrive2 尚未配置")
		}
		return func(walkCtx context.Context, visit strmFileVisit) error {
			return s.walkCloudDrive(walkCtx, mapping.ID, filter, visit)
		}, nil
	default:
		return nil, fmt.Errorf("不支持的网盘类型 %q", mapping.Kind)
	}
}

// strmMappingFailure 构造一条尚未开始遍历就失败的映射结果：只带原因，不涉及本地文件。
func strmMappingFailure(mapping domain.StrmMapping, err error) domain.StrmScanMapping {
	return domain.StrmScanMapping{Kind: mapping.Kind, Path: mapping.Path, LocalPath: mapping.LocalPath, Message: err.Error()}
}

// scanMapping 把一条映射的网盘文件写入本地 strm 目录，遍历到符合过滤条件的文件就立即写入。
// 全量先清理映射本地目录下的 strm 内容，增量跳过本地已存在的文件，内容比对方式只在内容变化时改写，
// 三种方式共用同一套过滤、播放地址与失败计数规则；视频的成功、跳过和失败均计入已处理数，附件下载不计入。
// 遍历中断（限流、网络失败或取消）只影响尚未扫描到的文件，已写入的 strm 保留，中断原因写入 message。
func (s *StrmService) scanMapping(ctx context.Context, root string, mapping domain.StrmMapping, base string, mode domain.StrmGenerateMode, walk strmWalk, advance func(), downloadFormats []string) domain.StrmScanMapping {
	ctx = withScanFileMapping(ctx, mapping)
	finishMapping := scanFileDirectory(ctx, "")
	discovered := false
	defer func() { finishMapping(discovered) }()
	entry := domain.StrmScanMapping{Kind: mapping.Kind, Path: mapping.Path, LocalPath: mapping.LocalPath}
	ctx, trackingErr := s.managedContext(ctx, mapping, downloadFormats)
	if trackingErr != nil {
		entry.Message = trackingErr.Error()
		return entry
	}
	target, _, err := resolveStrmPath(root, mapping.LocalPath)
	cleanupKey := journalKey("cleanup", mapping.Kind, mapping.ID, mapping.LocalPath)
	var cleaned bool
	if err == nil {
		_, err = loadTaskCheckpoint(ctx, cleanupKey, &cleaned)
	}
	if err == nil && mode == domain.StrmGenerateFull && !cleaned {
		entry.Deleted, err = clearStrmContentContext(ctx, root, target)
		if err == nil {
			err = saveTaskCheckpoint(ctx, cleanupKey, true)
		}
	}
	if err == nil {
		if mkdirErr := os.MkdirAll(target, 0o755); mkdirErr != nil {
			err = strmRootFailure("创建", target, mkdirErr)
		}
	}
	if err != nil {
		entry.Message = "准备本地 strm 目录失败：" + err.Error()
		return entry
	}
	var downloads *strmDownloadPool
	if len(downloadFormats) > 0 {
		downloads = s.newDownloadPool(ctx, root, target, mapping.Kind, mode, func(outcome strmDownloadOutcome) {
			switch {
			case outcome.err != nil:
				entry.DownloadFailed++
				entry.Message = "下载媒体失败：" + outcome.err.Error()
			case outcome.skipped:
				entry.DownloadSkipped++
			default:
				entry.Downloaded++
			}
		})
	}
	walkErr := walk(ctx, func(file strmSourceFile) (fileErr error) {
		if err := scanCheckpoint(ctx); err != nil {
			return err
		}
		if downloads != nil && isStrmMedia(file.Name, downloadFormats) {
			return downloads.enqueue(ctx, file)
		}
		finishFile := startScanFile(ctx, file, "generate")
		var itemErr error
		failed, unchanged := entry.Failed, entry.Unchanged
		defer func() {
			state := "completed"
			if fileErr != nil || entry.Failed > failed {
				state = "failed"
				if ctx.Err() != nil {
					state = "interrupted"
				}
			} else if entry.Unchanged > unchanged {
				state = "skipped"
			}
			if fileErr != nil {
				itemErr = fileErr
			}
			finishFile(state, itemErr)
		}()
		done, err := taskUnitDone(ctx, "strm-file", target, file.Directory, file.ID)
		if err != nil {
			return err
		}
		if done {
			entry.Files++
			entry.Unchanged++
			advance()
			return nil
		}
		entry.Files++
		if strings.ContainsAny(file.Name, `/\`) {
			itemErr = errors.New("STRM 文件名包含路径分隔符")
			entry.Failed++
			advance()
			return nil
		}
		absolute := filepath.Join(target, filepath.FromSlash(file.Directory), file.Name+".strm")
		if !withinStrmRoot(root, absolute) {
			itemErr = errors.New("STRM 文件路径超出根目录")
			entry.Failed++
			advance()
			return nil
		}
		identifier := file.ID
		if mapping.Kind == domain.StrmKindPan115 {
			identifier = strings.TrimSpace(file.PickCode)
			if identifier == "" {
				entry.Failed++
				entry.Message = "115 文件缺少 pick_code，无法生成播放链接"
				itemErr = errors.New(entry.Message)
				advance()
				return nil
			}
		}
		if mode == domain.StrmGenerateIncremental {
			exists, statErr := strmFileExists(absolute)
			if statErr != nil {
				entry.Failed++
				entry.Message = "读取本地 strm 文件失败：" + statErr.Error()
				itemErr = errors.New(entry.Message)
				advance()
				return nil
			}
			if exists {
				entry.Unchanged++
				advance()
				return completeTaskUnit(ctx, "strm-file", target, file.Directory, file.ID)
			}
		}
		created, changed, writeErr := s.writeManagedStrm(ctx, file, absolute, strmPlayURL(base, mapping.Kind, identifier)+"\n")
		switch {
		case writeErr != nil:
			entry.Failed++
			entry.Message = "写入 strm 文件失败：" + writeErr.Error()
			itemErr = errors.New(entry.Message)
		case created:
			entry.Created++
		case !changed:
			entry.Unchanged++
		}
		advance()
		if writeErr == nil {
			return completeTaskUnit(ctx, "strm-file", target, file.Directory, file.ID)
		}
		return nil
	})
	if downloads != nil {
		downloads.finish()
	}
	discovered = walkErr == nil
	if walkErr != nil {
		// 中断原因放在最前：它解释了本次结果为何不完整，单文件提示保留在后。
		if entry.Message == "" {
			entry.Message = walkErr.Error()
		} else {
			entry.Message = walkErr.Error() + "；" + entry.Message
		}
	}
	return entry
}

// pan115FileAPI 是递归遍历 115 目录所需的最小能力；生成 strm 与扫描入库都只依赖它。
type pan115FileAPI interface {
	Files(ctx context.Context, directoryID string, offset, limit int) (domain.Pan115FilePage, error)
}

// walkPan115Files 递归遍历 115 媒体文件；保留 ID 供扫描入库，pick_code 供生成播放链接。
// 每页固定读取 pan115FilePageLimit 条并按 HasMore 翻页，传给 visit 的 Directory 是相对扫描根目录的路径。
// 生成 strm 与扫描入库共用这一份递归实现，避免两条链路的分页与格式过滤规则漂移。
func walkPan115Files(ctx context.Context, api pan115FileAPI, rootID string, filter strmFileFilter, visit strmFileVisit) error {
	var ancestors []string
	var walk func(directoryID, relative string) error
	walk = func(directoryID, relative string) (walkErr error) {
		finishDirectory := scanFileDirectory(ctx, relative)
		defer func() { finishDirectory(walkErr == nil) }()
		ancestors = append(ancestors, directoryID)
		defer func() { ancestors = ancestors[:len(ancestors)-1] }()
		for offset := 0; ; {
			if err := scanCheckpoint(ctx); err != nil {
				return err
			}
			var page domain.Pan115FilePage
			pageKey := journalKey("115-page", directoryID, fmt.Sprint(offset))
			found, err := loadTaskCheckpoint(ctx, pageKey, &page)
			if err != nil {
				return err
			}
			if !found {
				page, err = api.Files(ctx, directoryID, offset, strmListLimit)
				if err != nil {
					return err
				}
				if err = saveTaskCheckpoint(ctx, pageKey, page); err != nil {
					return err
				}
			}
			for _, file := range page.Files {
				if err := scanCheckpoint(ctx); err != nil {
					return err
				}
				if filter.skipName(file.Name) {
					continue
				}
				if file.IsDirectory {
					if err := walk(file.ID, joinStrmRelative(relative, file.Name)); err != nil {
						return err
					}
					continue
				}
				if !filter.acceptFile(file.Name, file.Size) {
					continue
				}
				// 先记发现再交给调用方处理，保证进度里总数不少于已处理数。
				if !isStrmMedia(file.Name, filter.downloadFormats) {
					reportScanDiscovery(ctx)
				}
				if err := visit(strmSourceFile{ID: file.ID, PickCode: file.PickCode, Name: file.Name, Directory: relative, ParentID: directoryID, Ancestors: append([]string(nil), ancestors...)}); err != nil {
					return err
				}
			}
			if !page.HasMore || len(page.Files) == 0 {
				return nil
			}
			offset += len(page.Files)
		}
	}
	return walk(strings.TrimSpace(rootID), "")
}

// walkCloudDrive 递归遍历 CloudDrive2 目录下的媒体文件。
// CD2 的条目 ID 不保证可直接播放，这里统一用绝对路径作为播放标识。
func (s *StrmService) walkCloudDrive(ctx context.Context, rootPath string, filter strmFileFilter, visit strmFileVisit) error {
	var walk func(directory, relative string) error
	walk = func(directory, relative string) (walkErr error) {
		finishDirectory := scanFileDirectory(ctx, relative)
		defer func() { finishDirectory(walkErr == nil) }()
		if err := scanCheckpoint(ctx); err != nil {
			return err
		}
		var entries []clouddrive.Entry
		pageKey := journalKey("cd2-page", directory)
		found, err := loadTaskCheckpoint(ctx, pageKey, &entries)
		if err != nil {
			return err
		}
		if !found {
			entries, err = s.cloud.ListSubFiles(ctx, directory)
			if err != nil {
				return err
			}
			if err = saveTaskCheckpoint(ctx, pageKey, entries); err != nil {
				return err
			}
		}
		for _, item := range entries {
			if err := scanCheckpoint(ctx); err != nil {
				return err
			}
			full := strings.TrimSpace(item.FullPath)
			if full == "" {
				full = joinCloudPath(directory, item.Name)
			}
			if filter.skipName(item.Name) {
				continue
			}
			if item.Directory {
				if err := walk(full, joinStrmRelative(relative, item.Name)); err != nil {
					return err
				}
				continue
			}
			if !filter.acceptFile(item.Name, item.Size) {
				continue
			}
			if !isStrmMedia(item.Name, filter.downloadFormats) {
				reportScanDiscovery(ctx)
			}
			if err := visit(strmSourceFile{ID: full, Name: item.Name, Directory: relative}); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(strings.TrimSpace(rootPath), "")
}

// refreshEmby 按设置触发一次 Emby 媒体库刷新；未开启或未配置时不做任何外部请求。
func (s *StrmService) refreshEmby(ctx context.Context, values map[string]string) domain.StrmEmbyResult {
	if strings.TrimSpace(values[strmEmbyRefreshSettingKey]) != "true" {
		return domain.StrmEmbyResult{}
	}
	base := strings.TrimRight(strings.TrimSpace(values["EMBY_URL"]), "/")
	key := strings.TrimSpace(values["EMBY_API_KEY"])
	if base == "" || key == "" {
		return domain.StrmEmbyResult{Message: "未配置 Emby 地址或密钥，已跳过媒体库刷新"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/emby/Library/Refresh?api_key="+url.QueryEscape(key), nil)
	if err != nil {
		return domain.StrmEmbyResult{Attempted: true, Message: "构造 Emby 刷新请求失败，请检查 Emby 地址"}
	}
	response, err := s.http.Do(request)
	if err != nil {
		// URL 错误可能带 api_key；任务快照和页面只保留脱敏原因。
		return domain.StrmEmbyResult{Attempted: true, Message: "请求 Emby 刷新失败，请检查地址、密钥和连接"}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return domain.StrmEmbyResult{Attempted: true, Message: fmt.Sprintf("Emby 返回状态码 %d", response.StatusCode)}
	}
	if strings.TrimSpace(values[strmEmbyMediaAfterRefreshKey]) == "true" && s.embyMediaEnqueue != nil {
		go func() {
			timer := time.NewTimer(15 * time.Second)
			defer timer.Stop()
			<-timer.C
			s.embyMediaEnqueue(context.Background())
		}()
	}
	return domain.StrmEmbyResult{Attempted: true, Refreshed: true}
}

// parseStrmMappings 解析 STRM_PATHS 设置；空值表示没有配置任何映射。
// 设置保存校验与运行期扫描共用这一份实现，避免两套规则漂移。
// 返回的错误只说明原因，由调用方补充设置键等上下文。
func parseStrmMappings(raw string) ([]domain.StrmMapping, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		return nil, errors.New("需要是 JSON 数组")
	}
	var mappings []domain.StrmMapping
	if err := json.Unmarshal([]byte(trimmed), &mappings); err != nil {
		return nil, errors.New("需要是 JSON 数组，元素为网盘类型、网盘目录与本地路径")
	}
	seen := make(map[string]struct{}, len(mappings))
	seenLocalPaths := make(map[string]struct{}, len(mappings))
	for index := range mappings {
		mapping := &mappings[index]
		mapping.Kind = strings.TrimSpace(mapping.Kind)
		mapping.ID = strings.TrimSpace(mapping.ID)
		mapping.Path = strings.TrimSpace(mapping.Path)
		mapping.LocalPath = strings.TrimSpace(mapping.LocalPath)
		if mapping.Kind != domain.StrmKindPan115 && mapping.Kind != domain.StrmKindCloudDrive2 {
			return nil, fmt.Errorf("第 %d 项的网盘类型必须是 %s 或 %s", index+1, domain.StrmKindPan115, domain.StrmKindCloudDrive2)
		}
		if mapping.ID == "" || mapping.Path == "" {
			return nil, fmt.Errorf("第 %d 项缺少网盘目录", index+1)
		}
		if !strings.HasPrefix(mapping.LocalPath, "/") {
			return nil, fmt.Errorf("第 %d 项的本地 strm 路径必须以 / 开头", index+1)
		}
		mapping.LocalPath = path.Clean(mapping.LocalPath)
		if mapping.Formats == nil {
			mapping.Formats = append([]string(nil), defaultStrmFormats...)
		} else {
			mapping.Formats = normalizeStrmFormats(mapping.Formats)
		}
		if len(mapping.Formats) == 0 {
			return nil, fmt.Errorf("第 %d 项至少需要选择一种生成格式", index+1)
		}
		if mapping.MinSizeMB < 0 {
			return nil, fmt.Errorf("第 %d 项的最小视频大小不能为负数", index+1)
		}
		excludes, err := normalizeStrmExcludes(mapping.Exclude)
		if err != nil {
			return nil, fmt.Errorf("第 %d 项%w", index+1, err)
		}
		mapping.Exclude = excludes
		key := mapping.Kind + "\x00" + mapping.ID + "\x00" + mapping.LocalPath
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("第 %d 项与前面的映射重复", index+1)
		}
		seen[key] = struct{}{}
		if _, ok := seenLocalPaths[mapping.LocalPath]; ok {
			return nil, fmt.Errorf("第 %d 项的本地 strm 路径与前面的映射重复", index+1)
		}
		seenLocalPaths[mapping.LocalPath] = struct{}{}
	}
	return mappings, nil
}

// normalizeStrmExcludes 规范化排除规则：统一匹配方式、去空白、丢弃空关键字，并按「方式 + 小写关键字」去重。
// 关键字保留用户输入的大小写用于回显，匹配时才转小写，因此这里的小写只用于判重。
// 匹配方式不受支持时返回错误，避免保存出运行期无法解释的规则。
func normalizeStrmExcludes(excludes []domain.StrmExcludeKeyword) ([]domain.StrmExcludeKeyword, error) {
	seen := make(map[string]struct{}, len(excludes))
	result := make([]domain.StrmExcludeKeyword, 0, len(excludes))
	for _, exclude := range excludes {
		mode := strings.ToLower(strings.TrimSpace(exclude.Mode))
		switch mode {
		case domain.StrmExcludeModeEquals, domain.StrmExcludeModePrefix,
			domain.StrmExcludeModeSuffix, domain.StrmExcludeModeContains:
		default:
			return nil, fmt.Errorf("的排除规则匹配方式 %q 不受支持", exclude.Mode)
		}
		value := strings.TrimSpace(exclude.Value)
		if value == "" {
			continue
		}
		key := mode + "\x00" + strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, domain.StrmExcludeKeyword{Mode: mode, Value: value})
	}
	return result, nil
}

// normalizeStrmFormats 规范化格式输入，保证扩展名不受大小写、空格和前导点影响。
func normalizeStrmFormats(formats []string) []string {
	seen := make(map[string]struct{}, len(formats))
	result := make([]string, 0, len(formats))
	for _, format := range formats {
		format = strings.ToLower(strings.TrimSpace(format))
		format = strings.TrimLeft(format, ".")
		if format == "" {
			continue
		}
		if _, ok := seen[format]; ok {
			continue
		}
		seen[format] = struct{}{}
		result = append(result, format)
	}
	return result
}

// strmRootFailure 包装 strm 根目录下的文件系统错误，附带动作与具体路径。
// 这类错误的共同原因是容器内 /strm 没有挂载可写目录，HTTP 层据此返回可操作的部署提示，
// 而不是把挂载或权限问题降级成“服务内部错误”。
func strmRootFailure(action, target string, err error) error {
	return fmt.Errorf("%w: %s %s 失败（%v）", ErrStrmRootUnavailable, action, target, err)
}

// resolveStrmPath 把浏览器传入的相对路径解析为 root 目录下的绝对路径。
// 返回值是绝对路径与规范化后的相对路径（始终以 / 开头，根目录为 /）。
// 只允许 root 以下的路径：绝对路径、上跳与越界都会被拒绝。
func resolveStrmPath(root, relative string) (string, string, error) {
	trimmed := strings.TrimSpace(relative)
	if trimmed == "" {
		trimmed = "/"
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", "", fmt.Errorf("%w: 路径包含非法字符", ErrStrmInvalidInput)
	}
	normalized := path.Clean("/" + strings.TrimPrefix(strings.ReplaceAll(trimmed, `\`, "/"), "/"))
	absolute := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(normalized, "/")))
	if !withinStrmRoot(root, absolute) {
		return "", "", fmt.Errorf("%w: 路径超出 strm 根目录", ErrStrmInvalidInput)
	}
	return absolute, normalized, nil
}

// withinStrmRoot 判断 target 是否位于 root 之内（含 root 本身）。
func withinStrmRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

// writeStrmFile 写入 strm 文件；内容一致时不触碰磁盘，使重复扫描保持幂等。
// 返回的 created 表示文件此前不存在，changed 表示本次真的写入了磁盘。
func writeStrmFile(target, content string) (created bool, changed bool, err error) {
	existing, readErr := os.ReadFile(target)
	missing := errors.Is(readErr, fs.ErrNotExist)
	if readErr != nil && !missing {
		return false, false, readErr
	}
	if !missing && string(existing) == content {
		return false, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, false, strmRootFailure("创建", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return false, false, strmRootFailure("写入", target, err)
	}
	return missing, true, nil
}

// strmFileExists 报告目标位置是否已经存在文件，供增量生成跳过本地已有文件。
// 目录不算已有文件：生成目标是文件，同名目录会由写入逻辑报错，而不是被静默跳过。
func strmFileExists(target string) (bool, error) {
	info, err := os.Stat(target)
	if err == nil {
		return !info.IsDir(), nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// clearStrmContent 在全量生成前清理映射本地目录下的 strm 内容，返回删除的 strm 文件数。
// 只删除本功能生成的 .strm 文件：映射目录内可能同时存在字幕、封面等外部文件，不能一并删除。
// 清理后变为空的子目录一并移除，避免网盘已删除的目录在本地长期残留；映射根目录本身始终保留。
func clearStrmContent(root, target string) (int, error) {
	return clearStrmContentContext(context.Background(), root, target)
}

// clearStrmContentContext 在每次清理文件前响应暂停和取消。
func clearStrmContentContext(ctx context.Context, root, target string) (int, error) {
	if !withinStrmRoot(root, target) {
		return 0, fmt.Errorf("%w: 路径超出 strm 根目录", ErrStrmInvalidInput)
	}
	deleted := 0
	directories := make([]string, 0, 8)
	err := filepath.WalkDir(target, func(current string, entry fs.DirEntry, walkErr error) error {
		if err := scanCheckpoint(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			if current != target {
				directories = append(directories, current)
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".strm") {
			return nil
		}
		if err := os.Remove(current); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		deleted++
		return nil
	})
	if err != nil {
		return deleted, err
	}
	// WalkDir 按前序访问目录，倒序即子目录先于父目录，逐个删除已经空掉的目录。
	for index := len(directories) - 1; index >= 0; index-- {
		if err := scanCheckpoint(ctx); err != nil {
			return deleted, err
		}
		entries, readErr := os.ReadDir(directories[index])
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			return deleted, readErr
		}
		if len(entries) > 0 {
			continue
		}
		if err := os.Remove(directories[index]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return deleted, err
		}
	}
	return deleted, nil
}

// strmPlayURL 生成 strm 文件里的播放地址：{基址}/files/play/{网盘类型}/{文件标识}。
// 标识按路径段转义，使 CloudDrive2 的目录路径可以原样放进 URL。
func strmPlayURL(base, kind, fileID string) string {
	return base + strmPlayPrefix + kind + "/" + strings.Join(strmIDSegments(kind, fileID), "/")
}

// ParseStrmPlayPath 把播放地址中 /files/play/ 之后的部分还原成网盘类型与文件标识。
// 与 strmPlayURL 互为逆运算：编码与解码规则集中在这里，地址格式只有一处权威实现。
func ParseStrmPlayPath(escaped string) (string, string, error) {
	segments := strings.Split(escaped, "/")
	for index, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return "", "", fmt.Errorf("%w: 播放地址编码非法", ErrStrmInvalidInput)
		}
		segments[index] = decoded
	}
	kind := strings.TrimSpace(segments[0])
	if kind != domain.StrmKindPan115 && kind != domain.StrmKindCloudDrive2 {
		return "", "", fmt.Errorf("%w: 不支持的网盘类型 %q", ErrStrmInvalidInput, kind)
	}
	fileID := strings.TrimSpace(strings.Join(segments[1:], "/"))
	if fileID == "" {
		return "", "", fmt.Errorf("%w: 播放地址缺少网盘文件标识", ErrStrmInvalidInput)
	}
	if strmAbsoluteID(kind) {
		fileID = "/" + fileID
	}
	return kind, fileID, nil
}

// strmIDSegments 把网盘标识拆成 URL 路径段并逐段转义。
// CloudDrive2 的标识是网盘绝对路径，首个空段被省略：地址里出现 // 会被部分反向代理合并成 /，
// 省略后解析时再按网盘类型补回前导斜杠，保证 strm 内容在不同网关下都能解析。
func strmIDSegments(kind, fileID string) []string {
	trimmed := strings.TrimSpace(fileID)
	if strmAbsoluteID(kind) {
		trimmed = strings.TrimPrefix(trimmed, "/")
	}
	segments := strings.Split(trimmed, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return segments
}

// strmAbsoluteID 报告该网盘类型的播放标识是否为网盘绝对路径。
func strmAbsoluteID(kind string) bool { return kind == domain.StrmKindCloudDrive2 }

// isStrmMedia 判断文件名是否符合当前映射配置的格式。
func isStrmMedia(name string, formats []string) bool {
	extension := strings.TrimLeft(strings.ToLower(filepath.Ext(name)), ".")
	for _, format := range formats {
		if extension == format {
			return true
		}
	}
	return false
}

// joinStrmRelative 拼接扫描结果里的相对目录路径，根目录为空串。
func joinStrmRelative(base, name string) string {
	if base == "" {
		return name
	}
	return base + "/" + name
}

// joinStrmPath 拼接浏览器路径，根目录固定为 /。
func joinStrmPath(base, name string) string {
	if base == "/" {
		return "/" + name
	}
	return base + "/" + name
}

// joinCloudPath 在 CloudDrive2 未回传完整路径时按父目录拼接。
func joinCloudPath(directory, name string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(directory), "/")
	return trimmed + "/" + name
}

// normalizeCloudPath 把浏览器传入的 CloudDrive2 路径规范成以 / 开头的绝对路径；空值表示根目录。
func normalizeCloudPath(directory string) string {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" {
		return "/"
	}
	return path.Clean("/" + strings.TrimPrefix(strings.ReplaceAll(trimmed, `\`, "/"), "/"))
}
