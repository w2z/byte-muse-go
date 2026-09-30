// Package covercache 把影片封面按番号持久化到本地目录，页面与渠道复用同一份图片。
package covercache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrInvalidCode 表示番号不能安全地作为文件名，调用方应回退到源站地址。
var ErrInvalidCode = errors.New("invalid_cover_code")

// ErrSource 表示封面下载或落盘失败，调用方同样回退到源站地址。
var ErrSource = errors.New("cover_source_unavailable")

const (
	// maxCoverBytes 限制单张封面大小，异常响应不得写满磁盘。
	maxCoverBytes = 12 << 20
	// maxCoverNameLength 限制文件名主干长度，避免超出文件系统上限。
	maxCoverNameLength = 120
	// userAgent 与采集、PT 搜索保持同一口径，避免默认 Go UA 被图床拒绝。
	userAgent = "Mozilla/5.0 ByteMuse"
)

// coverExtensions 是允许落盘的图片格式，也是查找已有缓存时的顺序。
var coverExtensions = []string{"jpg", "png", "webp", "gif", "avif"}

// File 是命中或新写入的封面文件。
type File struct {
	Path        string
	ContentType string
}

// Cache 按番号把封面写入本地目录：一个番号只保留一份文件，扩展名取图片真实格式。
// 目录默认是容器内的 /data/cover，由部署时挂载；目录不可写只影响封面持久化，不阻断页面。
type Cache struct {
	dir   string
	http  *http.Client
	proxy func(context.Context) string
}

// New 绑定缓存目录与代理读取器；proxy 为 nil 表示直连。
// 代理在每次请求时重新读取，管理员改设置不需要重启进程。
func New(dir string, proxy func(context.Context) string) *Cache {
	cache := &Cache{dir: strings.TrimSpace(dir), proxy: proxy}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = cache.transportProxy
	cache.http = &http.Client{Transport: transport, Timeout: 20 * time.Second}
	return cache
}

// Dir 返回缓存目录，供启动日志与排障使用。
func (c *Cache) Dir() string {
	if c == nil {
		return ""
	}
	return c.dir
}

// AllowedSource 判断源地址是否允许下载：只接受不带凭据的 http/https 绝对地址。
// 下载与「失败时回退到源站」的重定向共用它，避免把任意协议交给浏览器。
func AllowedSource(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return parsed.Host != "" && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// Ensure 返回番号对应的本地封面：命中缓存直接复用，未命中则下载后落盘。
// 同一个番号先写临时文件再原子改名，并发请求最多重复下载一次，不会留下半张图片。
func (c *Cache) Ensure(ctx context.Context, code, source string) (File, error) {
	if c == nil {
		return File{}, fmt.Errorf("%w: 封面缓存未初始化", ErrSource)
	}
	name, err := coverName(code)
	if err != nil {
		return File{}, err
	}
	if cached, ok := c.lookup(name); ok {
		return cached, nil
	}
	if c.dir == "" {
		return File{}, fmt.Errorf("%w: 未配置封面目录", ErrSource)
	}
	body, contentType, err := c.download(ctx, source)
	if err != nil {
		return File{}, err
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return File{}, fmt.Errorf("%w: 创建封面目录失败: %v", ErrSource, err)
	}
	target := filepath.Join(c.dir, name+"."+coverExtension(contentType))
	if err := writeFile(target, body); err != nil {
		return File{}, fmt.Errorf("%w: 写入封面失败: %v", ErrSource, err)
	}
	return File{Path: target, ContentType: contentType}, nil
}

// lookup 查找该番号已落盘的封面；命中多个扩展名时按 coverExtensions 顺序取第一个。
func (c *Cache) lookup(name string) (File, bool) {
	if c.dir == "" {
		return File{}, false
	}
	for _, extension := range coverExtensions {
		path := filepath.Join(c.dir, name+"."+extension)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		return File{Path: path, ContentType: imageContentType(extension)}, true
	}
	return File{}, false
}

// transportProxy 每次请求重新读取代理设置；空值或非法值按直连处理，日志与错误都不回显凭据。
func (c *Cache) transportProxy(request *http.Request) (*url.URL, error) {
	if c.proxy == nil || request == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(c.proxy(request.Context()))
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, nil
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
		return parsed, nil
	default:
		return nil, nil
	}
}

// download 拉取封面并确认响应确实是图片。
// 不发送 Referer：图床按来源站点校验时错误的 Referer 会被拒绝，而空 Referer 通常放行。
func (c *Cache) download(ctx context.Context, source string) ([]byte, string, error) {
	if !AllowedSource(source) {
		return nil, "", fmt.Errorf("%w: 源地址无效", ErrSource)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(source), nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrSource, err)
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrSource, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: 源站返回 %d", ErrSource, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCoverBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrSource, err)
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("%w: 源站返回空内容", ErrSource)
	}
	if len(body) > maxCoverBytes {
		return nil, "", fmt.Errorf("%w: 封面超过 %d 字节", ErrSource, maxCoverBytes)
	}
	contentType := imageContentType(response.Header.Get("Content-Type"))
	if contentType == "" {
		// 部分图床把 Content-Type 写成 application/octet-stream，按内容嗅探兜底。
		contentType = imageContentType(http.DetectContentType(body))
	}
	if contentType == "" {
		return nil, "", fmt.Errorf("%w: 响应不是图片", ErrSource)
	}
	return body, contentType, nil
}

// coverName 把番号规范成文件名主干：只保留字母、数字、连字符与下划线并转小写，
// 例如 SONS-1223 -> sons-1223；结果为空或过长时返回 ErrInvalidCode，由调用方回退源站。
func coverName(code string) (string, error) {
	var builder strings.Builder
	for _, symbol := range strings.ToLower(strings.TrimSpace(code)) {
		switch {
		case symbol >= 'a' && symbol <= 'z', symbol >= '0' && symbol <= '9', symbol == '-', symbol == '_':
			builder.WriteRune(symbol)
		}
	}
	name := strings.Trim(builder.String(), "-_")
	if name == "" || len(name) > maxCoverNameLength {
		return "", fmt.Errorf("%w: %q", ErrInvalidCode, code)
	}
	return name, nil
}

// imageContentType 把媒体类型或扩展名规范成允许的图片类型，不支持的格式返回空串。
func imageContentType(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if index := strings.Index(value, ";"); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	switch value {
	case "image/jpeg", "jpg", "jpeg":
		return "image/jpeg"
	case "image/png", "png":
		return "image/png"
	case "image/webp", "webp":
		return "image/webp"
	case "image/gif", "gif":
		return "image/gif"
	case "image/avif", "avif":
		return "image/avif"
	default:
		return ""
	}
}

// extensionFor 把规范图片类型映射成落盘扩展名。
func extensionFor(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/avif":
		return "avif"
	default:
		return ""
	}
}

// coverExtension 把下载时确认的图片类型映射成落盘扩展名；类型不可识别时按 jpg 兜底。
// 扩展名取图片真实格式，源地址写成 .jpg 而实际返回 webp 的图床也会按真实格式保存。
func coverExtension(contentType string) string {
	if extension := extensionFor(contentType); extension != "" {
		return extension
	}
	return "jpg"
}

// writeFile 先写同目录临时文件再改名，中断时不会留下半张封面被后续请求当成缓存命中。
func writeFile(target string, body []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(target), ".cover-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// 缓存目录会被用户直接浏览，权限跟随常规文件，而不是临时文件的 0600。
	if err := os.Chmod(tempName, 0o644); err != nil {
		return err
	}
	return os.Rename(tempName, target)
}
