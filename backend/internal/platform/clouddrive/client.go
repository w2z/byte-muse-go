// Package clouddrive 提供 CloudDrive2 的最小 gRPC-web 客户端。
//
// CloudDrive2 只暴露 gRPC-web，仓库不引入 protobuf 工具链，
// 因此请求与响应按官方 clouddrive.proto v1.0.14 的字段号手工编解码（见 proto.go）。
// 帧格式固定为 [1 字节标志][4 字节大端长度][protobuf]，
// 与旧版 Python 集成（未加密代码/app/modules/cloudnas/cloudnas.py）保持一致。
package clouddrive

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// servicePath 是 CloudDriveFileSrv 的服务路径，所有 RPC 都挂在它下面。
	servicePath = "/clouddrive.CloudDriveFileSrv"
	// grpcWebContentType 是 gRPC-web 二进制帧的 Content-Type。
	grpcWebContentType = "application/grpc-web"
	// frameHeaderBytes 是帧头长度：1 字节标志位加 4 字节大端长度。
	frameHeaderBytes = 5
	// maxResponseBytes 限制单次响应体积，避免异常服务端拖垮进程。
	maxResponseBytes = 32 << 20
	// tokenLifetime 是本地缓存令牌的寿命；CD2 的 JWT 有效期更长，这里取保守值。
	tokenLifetime = 30 * time.Minute
)

var (
	// ErrNotConfigured 表示 CloudDrive2 地址或账号密码未配置。
	ErrNotConfigured = errors.New("CloudDrive2 尚未配置")
	// ErrUnauthorized 表示 CloudDrive2 拒绝了账号密码或令牌。
	ErrUnauthorized = errors.New("CloudDrive2 认证失败")
)

// Entry 是 CloudDrive2 的一个目录条目。
type Entry struct {
	SHA1      string
	ID        string
	Name      string
	FullPath  string
	Size      int64
	Directory bool
}

// Download 是一次 GetDownloadUrlPath 的结果。
// URL 已是可直接访问的地址：优先云盘直链，缺失时回退 CloudDrive2 中转地址。
type Download struct {
	URL       string
	UserAgent string
	Headers   map[string]string
}

// Config 是客户端连接参数；BaseURL 允许省略协议，默认补 http://。
type Config struct {
	BaseURL  string
	Username string
	Password string
	HTTP     *http.Client
}

// Client 是并发安全的 CloudDrive2 客户端，内部缓存登录令牌。
type Client struct {
	base     string
	username string
	password string
	http     *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// New 组装一个 CloudDrive2 客户端；地址为空时所有调用返回 ErrNotConfigured。
func New(cfg Config) *Client {
	client := cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{
		base:     normalizeBase(cfg.BaseURL),
		username: strings.TrimSpace(cfg.Username),
		password: cfg.Password,
		http:     client,
	}
}

// Configured 报告客户端是否具备发起请求所需的全部参数。
func (c *Client) Configured() bool {
	return c.base != "" && c.username != "" && c.password != ""
}

// normalizeBase 统一地址写法：补协议、去掉末尾斜杠。
func normalizeBase(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	return strings.TrimRight(value, "/")
}

// Token 返回可用令牌；缓存未过期时直接复用，过期后重新登录。
func (c *Client) Token(ctx context.Context) (string, error) {
	if !c.Configured() {
		return "", ErrNotConfigured
	}
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.expiry) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	frames, err := c.call(ctx, "GetToken", encodeGetTokenRequest(c.username, c.password), "")
	if err != nil {
		return "", err
	}
	if len(frames) == 0 {
		return "", fmt.Errorf("%w: CloudDrive2 未返回令牌", ErrUnauthorized)
	}
	success, message, token, err := decodeToken(frames[0])
	if err != nil {
		return "", err
	}
	if !success || strings.TrimSpace(token) == "" {
		if strings.TrimSpace(message) == "" {
			message = "账号或密码不正确"
		}
		return "", fmt.Errorf("%w: %s", ErrUnauthorized, message)
	}
	c.mu.Lock()
	c.token = token
	c.expiry = time.Now().Add(tokenLifetime)
	c.mu.Unlock()
	return token, nil
}

// invalidate 丢弃缓存令牌，用于服务端提前让令牌失效的场景。
func (c *Client) invalidate() {
	c.mu.Lock()
	c.token = ""
	c.expiry = time.Time{}
	c.mu.Unlock()
}

// withToken 先用当前令牌执行一次 RPC；令牌失效时重新登录并重试一次。
func (c *Client) withToken(ctx context.Context, action func(token string) error) error {
	token, err := c.Token(ctx)
	if err != nil {
		return err
	}
	err = action(token)
	if !errors.Is(err, ErrUnauthorized) {
		return err
	}
	c.invalidate()
	token, err = c.Token(ctx)
	if err != nil {
		return err
	}
	return action(token)
}

// ListSubFiles 读取一个目录的全部直接子项；path 是 CloudDrive2 中的绝对路径。
// GetSubFiles 是服务端流式 RPC，可能返回多帧，这里合并为一个列表。
func (c *Client) ListSubFiles(ctx context.Context, path string) ([]Entry, error) {
	target := strings.TrimSpace(path)
	if target == "" {
		return nil, fmt.Errorf("CloudDrive2 目录路径不能为空")
	}
	var entries []Entry
	err := c.withToken(ctx, func(token string) error {
		frames, err := c.call(ctx, "GetSubFiles", encodeListSubFileRequest(target), token)
		if err != nil {
			return err
		}
		collected := make([]Entry, 0, len(frames))
		for _, frame := range frames {
			files, err := decodeSubFiles(frame)
			if err != nil {
				return err
			}
			for _, file := range files {
				collected = append(collected, Entry{
					SHA1:      file.SHA1,
					ID:        file.ID,
					Name:      file.Name,
					FullPath:  file.FullPathName,
					Size:      file.Size,
					Directory: file.isDir(),
				})
			}
		}
		entries = collected
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// CreateFolder 在 parentPath 下新建 folderName；已存在或创建失败时返回 CD2 的错误说明。
func (c *Client) CreateFolder(ctx context.Context, parentPath, folderName string) error {
	parent := strings.TrimSpace(parentPath)
	name := strings.TrimSpace(folderName)
	if parent == "" || name == "" {
		return fmt.Errorf("CloudDrive2 目录名与父目录都不能为空")
	}
	return c.withToken(ctx, func(token string) error {
		frames, err := c.call(ctx, "CreateFolder", encodeCreateFolderRequest(parent, name), token)
		if err != nil {
			return err
		}
		if len(frames) == 0 {
			return fmt.Errorf("CloudDrive2 未返回目录创建结果")
		}
		created, message, err := decodeCreateFolderResult(frames[0])
		if err != nil {
			return err
		}
		if !created {
			if strings.TrimSpace(message) == "" {
				message = "CloudDrive2 未说明创建失败原因"
			}
			return errors.New(message)
		}
		return nil
	})
}

// DownloadURL 把一个文件路径换成可直接播放的地址。
// direct 为真时要求 CloudDrive2 返回云盘直链，避免流量绕经 CD2 服务端。
func (c *Client) DownloadURL(ctx context.Context, path string, direct bool) (Download, error) {
	target := strings.TrimSpace(path)
	if target == "" {
		return Download{}, fmt.Errorf("CloudDrive2 文件路径不能为空")
	}
	var result Download
	err := c.withToken(ctx, func(token string) error {
		frames, err := c.call(ctx, "GetDownloadUrlPath", encodeDownloadURLRequest(target, direct), token)
		if err != nil {
			return err
		}
		if len(frames) == 0 {
			return fmt.Errorf("CloudDrive2 未返回下载地址")
		}
		info, err := decodeDownloadURL(frames[0])
		if err != nil {
			return err
		}
		address := strings.TrimSpace(info.Direct)
		if address == "" {
			address = c.absoluteURL(info.Path)
		}
		if address == "" {
			return fmt.Errorf("CloudDrive2 未返回可用的下载地址")
		}
		result = Download{URL: address, UserAgent: strings.TrimSpace(info.UserAgent), Headers: info.Headers}
		return nil
	})
	if err != nil {
		return Download{}, err
	}
	return result, nil
}

// absoluteURL 把 CD2 返回的相对下载路径补成完整地址。
func (c *Client) absoluteURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		return value
	}
	return c.base + "/" + strings.TrimLeft(value, "/")
}

// call 发起一次 gRPC-web 调用并返回全部数据帧。
func (c *Client) call(ctx context.Context, method string, payload []byte, token string) ([][]byte, error) {
	if c.base == "" {
		return nil, ErrNotConfigured
	}
	frame := make([]byte, frameHeaderBytes+len(payload))
	binary.BigEndian.PutUint32(frame[1:frameHeaderBytes], uint32(len(payload)))
	copy(frame[frameHeaderBytes:], payload)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+servicePath+"/"+method, bytes.NewReader(frame))
	if err != nil {
		return nil, fmt.Errorf("构造 CloudDrive2 请求失败: %w", err)
	}
	request.Header.Set("Content-Type", grpcWebContentType)
	request.Header.Set("Accept", grpcWebContentType)
	request.Header.Set("X-Grpc-Web", "1")
	request.Header.Set("X-User-Agent", "grpc-go/1.0")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求 CloudDrive2 失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("读取 CloudDrive2 响应失败: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, ErrUnauthorized
	}
	// HTTP/1.1 下 gRPC-web 用响应头回传错误；HTTP/2 下会落到 trailer 帧，两者都解析。
	if message := strings.TrimSpace(response.Header.Get("grpc-message")); message != "" {
		return nil, fmt.Errorf("CloudDrive2 返回错误: %s", decodeGrpcMessage(message))
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CloudDrive2 返回 HTTP %d", response.StatusCode)
	}
	return grpcWebFrames(body)
}

// grpcWebFrames 解析 gRPC-web 响应体并返回数据帧；trailer 中的非零 grpc-status 视为错误。
func grpcWebFrames(body []byte) ([][]byte, error) {
	var frames [][]byte
	var status, message string
	for offset := 0; offset < len(body); {
		if offset+frameHeaderBytes > len(body) {
			return nil, errors.New("CloudDrive2 响应帧不完整")
		}
		flag := body[offset]
		length := int(binary.BigEndian.Uint32(body[offset+1 : offset+frameHeaderBytes]))
		offset += frameHeaderBytes
		if length < 0 || offset+length > len(body) {
			return nil, errors.New("CloudDrive2 响应帧长度非法")
		}
		payload := body[offset : offset+length]
		offset += length
		if flag&0x80 == 0 {
			frames = append(frames, payload)
			continue
		}
		for _, line := range strings.Split(string(payload), "\r\n") {
			name, value, found := strings.Cut(line, ":")
			if !found {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "grpc-status":
				status = strings.TrimSpace(value)
			case "grpc-message":
				message = strings.TrimSpace(value)
			}
		}
	}
	if status != "" && status != "0" {
		return nil, fmt.Errorf("CloudDrive2 返回错误（%s）：%s", status, decodeGrpcMessage(message))
	}
	return frames, nil
}

// decodeGrpcMessage 还原 gRPC 错误消息的百分号编码，失败时保留原文。
func decodeGrpcMessage(message string) string {
	decoded, err := url.PathUnescape(message)
	if err != nil {
		return message
	}
	return decoded
}
