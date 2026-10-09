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

// strmDownloadReader 在收到数据时续期空闲超时，不限制正常传输的总时长。
type strmDownloadReader struct {
	reader io.Reader
	idle   *time.Timer
	report func(int)
}

func (r strmDownloadReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.idle.Reset(strmRequestTimeout)
		if r.report != nil {
			r.report(n)
		}
	}
	return n, err
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
		return false, fmt.Errorf("无法打开 STRM 根目录：%s", scanFileError(err))
	}
	defer base.Close()
	mappingPath, err := filepath.Rel(root, target)
	if err != nil || !filepath.IsLocal(mappingPath) {
		return false, errors.New("下载目录超出 STRM 根目录")
	}
	dir, err := base.OpenRoot(mappingPath)
	if err != nil {
		return false, fmt.Errorf("无法打开下载目录：%s", scanFileError(err))
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
		return false, fmt.Errorf("无法检查下载目标：%s", scanFileError(err))
	}
	identifier := file.ID
	if kind == domain.StrmKindPan115 {
		identifier = strings.TrimSpace(file.PickCode)
	}
	if identifier == "" {
		return false, errors.New("下载文件缺少网盘标识")
	}
	targetURL, err := s.PlayURL(pan115.WithFileDownload(ctx), kind, identifier, strmDownloadUserAgent)
	if err != nil {
		return false, fmt.Errorf("获取媒体下载地址失败：%s", scanFileError(err))
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
		return false, fmt.Errorf("媒体下载请求失败：%s", scanFileError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, strmDownloadResponseError(response)
	}
	if err := dir.MkdirAll(filepath.Dir(relative), 0o755); err != nil {
		return false, fmt.Errorf("创建媒体下载目录失败：%s", scanFileError(err))
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false, err
	}
	temporary := filepath.Join(filepath.Dir(relative), fmt.Sprintf(".bytemuse-download-%x.part", random))
	output, err := dir.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("创建媒体临时文件失败：%s", scanFileError(err))
	}
	defer dir.Remove(temporary)
	var transferred int64
	reportScanFileBytes(ctx, file, 0, response.ContentLength)
	_, copyErr := io.Copy(output, strmDownloadReader{reader: response.Body, idle: idle, report: func(count int) {
		transferred += int64(count)
		reportScanFileBytes(ctx, file, transferred, response.ContentLength)
	}})
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		return false, fmt.Errorf("媒体下载未完整写入：%s", scanFileError(errors.Join(copyErr, closeErr)))
	}
	if err := scanCheckpoint(ctx); err != nil {
		return false, err
	}
	if err := s.recordManagedFileAt(ctx, file, filepath.Join(target, temporary), filepath.Join(target, relative)); err != nil {
		return false, err
	}
	if err := dir.Rename(temporary, relative); err != nil {
		return false, fmt.Errorf("保存媒体下载文件失败：%s", scanFileError(err))
	}
	return false, nil
}

// strmDownloadResponseError keeps bounded JSON error messages, never raw HTML or arbitrary response data.
func strmDownloadResponseError(response *http.Response) error {
	message := fmt.Sprintf("媒体下载返回状态码 %d", response.StatusCode)
	body, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(body) > 8192 {
		return errors.New(message)
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) == nil {
		for _, key := range []string{"message", "msg", "error", "error_description"} {
			var detail string
			if json.Unmarshal(payload[key], &detail) == nil && strings.TrimSpace(detail) != "" {
				return fmt.Errorf("%s：%s", message, scanFileError(errors.New(detail)))
			}
		}
	}
	return errors.New(message)
}
