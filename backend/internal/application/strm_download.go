package application

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
)

const strmDownloadWorkers = 5
const strmDownloadUserAgent = "ByteMuse/STRM"
const defaultStrmDownloadExtensions = `["srt","ssa","ass","nfo","jpg","png"]`

// parseStrmDownloadExtensions 是设置校验与下载过滤共用的后缀规则；空串恢复默认，[] 禁用全部后缀。
// STRM 本身禁止下载，避免覆盖生成的播放入口；后缀只允许字母和数字。
func parseStrmDownloadExtensions(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = defaultStrmDownloadExtensions
	}
	var values []string
	if !strings.HasPrefix(strings.TrimSpace(raw), "[") || json.Unmarshal([]byte(raw), &values) != nil {
		return nil, errors.New("需要是文件后缀字符串数组")
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		ext := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
		if ext == "" || len(ext) > 16 || ext == "strm" || strings.Trim(ext, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
			return nil, errors.New("后缀须为 1 到 16 位字母或数字，不能为 strm")
		}
		if !seen[ext] {
			result = append(result, ext)
			seen[ext] = true
		}
	}
	return result, nil
}

// strmDownloadFormats 只在显式开启时扩展目录扫描；关闭时不解析、不下载。
func strmDownloadFormats(values map[string]string) ([]string, error) {
	if values[strmDownloadEnableSettingKey] != "true" {
		return nil, nil
	}
	return parseStrmDownloadExtensions(values[strmDownloadExtensionsSettingKey])
}

type strmDownloadOutcome struct {
	skipped bool
	err     error
}

// strmDownloadReader 在收到数据时续期空闲超时，不限制正常传输的总时长。
type strmDownloadReader struct {
	reader io.Reader
	idle   *time.Timer
}

func (r strmDownloadReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.idle.Reset(strmRequestTimeout)
	}
	return n, err
}

// strmDownloadPool 以固定大小队列提供背压；所有结果由扫描线程归并，避免进度与计数竞争。
type strmDownloadPool struct {
	jobs    chan strmSourceFile
	results chan strmDownloadOutcome
	pending int
	apply   func(strmDownloadOutcome)
}

func (s *StrmService) newDownloadPool(ctx context.Context, root, target, kind string, mode domain.StrmGenerateMode, apply func(strmDownloadOutcome)) *strmDownloadPool {
	// 进度回调只允许扫描线程调用；下载仍沿用 115 客户端的全局冷却和限速。
	ctx = pan115.WithCooldownReporter(ctx, func(time.Duration) {})
	p := &strmDownloadPool{jobs: make(chan strmSourceFile), results: make(chan strmDownloadOutcome, strmDownloadWorkers), apply: apply}
	var workers sync.WaitGroup
	for i := 0; i < strmDownloadWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for file := range p.jobs {
				skipped, err := s.downloadStrmMedia(ctx, root, target, kind, file, mode)
				p.results <- strmDownloadOutcome{skipped: skipped, err: err}
			}
		}()
	}
	go func() { workers.Wait(); close(p.results) }()
	return p
}

// enqueue 在五项尚未完成时等待结果，使扫描不会无界积压下载或占用内存。
func (p *strmDownloadPool) enqueue(ctx context.Context, file strmSourceFile) error {
	for p.pending >= strmDownloadWorkers {
		select {
		case result := <-p.results:
			p.pending--
			p.apply(result)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case p.jobs <- file:
		p.pending++
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// finish 等待所有已提交下载结束，确保函数返回和 Emby 通知前文件已落盘或临时文件已清理。
func (p *strmDownloadPool) finish() {
	close(p.jobs)
	for result := range p.results {
		p.pending--
		p.apply(result)
	}
}

// downloadStrmMedia 复用网盘直链解析，保持 UA/请求头一致，通过受限根目录原子落盘。
// 增量和事件同步保留已有文件；全量用完整新文件替换，失败始终保留旧文件。
func (s *StrmService) downloadStrmMedia(ctx context.Context, root, target, kind string, file strmSourceFile, mode domain.StrmGenerateMode) (bool, error) {
	if err := scanCheckpoint(ctx); err != nil {
		return false, err
	}
	relative := filepath.Join(filepath.FromSlash(file.Directory), file.Name)
	if file.Name == "" || strings.ContainsAny(file.Name, `/\`) || !filepath.IsLocal(relative) {
		return false, errors.New("下载文件路径无效")
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return false, errors.New("无法打开 STRM 根目录")
	}
	defer base.Close()
	mappingPath, err := filepath.Rel(root, target)
	if err != nil || !filepath.IsLocal(mappingPath) {
		return false, errors.New("下载目录超出 STRM 根目录")
	}
	dir, err := base.OpenRoot(mappingPath)
	if err != nil {
		return false, errors.New("无法打开下载目录")
	}
	defer dir.Close()
	info, err := dir.Lstat(relative)
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, errors.New("下载目标不是普通文件")
		}
		if mode != domain.StrmGenerateFull {
			return true, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, errors.New("无法检查下载目标")
	}
	identifier := file.ID
	if kind == domain.StrmKindPan115 {
		identifier = strings.TrimSpace(file.PickCode)
	}
	if identifier == "" {
		return false, errors.New("下载文件缺少网盘标识")
	}
	targetURL, err := s.PlayURL(ctx, kind, identifier, strmDownloadUserAgent)
	if err != nil {
		return false, errors.New("获取媒体下载地址失败，请检查网盘连接或限流状态")
	}
	address := targetURL.Redirect
	if targetURL.Proxy != nil {
		address = targetURL.Proxy.URL
	}
	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// 首次响应与后续读取都限制空闲时间；任务取消仍会立即终止网络请求。
	idle := time.AfterFunc(strmRequestTimeout, cancel)
	defer idle.Stop()
	request, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, address, nil)
	if err != nil {
		return false, errors.New("媒体下载地址无效")
	}
	request.Header.Set("User-Agent", strmDownloadUserAgent)
	if targetURL.Proxy != nil {
		for key, value := range targetURL.Proxy.Headers {
			request.Header.Set(key, value)
		}
		if targetURL.Proxy.UserAgent != "" {
			request.Header.Set("User-Agent", targetURL.Proxy.UserAgent)
		}
	}
	client := *s.http
	client.Timeout = 0
	response, err := client.Do(request)
	if err != nil {
		return false, errors.New("媒体下载请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("媒体下载返回状态码 %d", response.StatusCode)
	}
	if err := dir.MkdirAll(filepath.Dir(relative), 0o755); err != nil {
		return false, errors.New("创建媒体下载目录失败")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false, err
	}
	temporary := filepath.Join(filepath.Dir(relative), fmt.Sprintf(".bytemuse-download-%x.part", random))
	output, err := dir.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false, errors.New("创建媒体临时文件失败")
	}
	defer dir.Remove(temporary)
	_, copyErr := io.Copy(output, strmDownloadReader{reader: response.Body, idle: idle})
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		return false, errors.New("媒体下载未完整写入")
	}
	if err := scanCheckpoint(ctx); err != nil {
		return false, err
	}
	if err := dir.Rename(temporary, relative); err != nil {
		return false, errors.New("保存媒体下载文件失败")
	}
	return false, nil
}
