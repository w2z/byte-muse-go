package pan115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var (
	// ErrFileNotFound 表示 115 中不存在该文件，或当前账号无权访问。
	ErrFileNotFound = errors.New("115 中不存在该文件")
	// ErrDownloadUnavailable 表示 115 未返回可用的下载直链，文件可能受限或已被清理。
	ErrDownloadUnavailable = errors.New("115 未返回可用的下载直链")
	// ErrInvalidFileID 表示 115 拒绝该文件标识，通常是标识格式非法或指向不可访问的对象。
	ErrInvalidFileID = errors.New("115 文件标识无效")
)

// pan115RootID 是 115 根目录的固定标识；文件信息接口不回传上级目录时用它兜底。
const pan115RootID = "0"

// invalidFileIDCode 是 115 对非法文件标识返回的业务错误码；115 只用通用的「参数错误」表达，
// 对调用方等价于文件标识无效，因此在这里归一化为 ErrInvalidFileID。
const invalidFileIDCode = 990002

// FileInfo 是单个 115 文件的完整信息；Path 是从根目录到该文件所在目录的完整路径。
type FileInfo struct {
	File
	Path []Directory
}

// Info 读取单个文件（夹）的信息。115 对同一个 file_id 既可能返回对象，
// 也可能返回单元素数组，这里统一归一化；data 为空表示文件不存在。
func (c *Client) Info(ctx context.Context, accessToken, fileID string) (FileInfo, error) {
	type fileInfoWire struct {
		ID       string      `json:"file_id"`
		Name     string      `json:"file_name"`
		Category string      `json:"file_category"`
		Size     json.Number `json:"size_byte"`
		PickCode string      `json:"pick_code"`
		Paths    []struct {
			ID   string `json:"file_id"`
			Name string `json:"file_name"`
		} `json:"paths"`
	}
	raw, err := c.apiCall(ctx, http.MethodGet, c.api+"/open/folder/get_info", accessToken,
		url.Values{"file_id": {fileID}}, "文件信息")
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == invalidFileIDCode {
			return FileInfo{}, ErrInvalidFileID
		}
		return FileInfo{}, err
	}
	data := raw
	if len(data) == 0 || string(data) == "null" {
		return FileInfo{}, ErrFileNotFound
	}
	if data[0] == '[' {
		var entries []json.RawMessage
		if err := json.Unmarshal(data, &entries); err != nil {
			return FileInfo{}, fmt.Errorf("解码 115 文件信息列表失败: %w", err)
		}
		switch len(entries) {
		case 0:
			return FileInfo{}, ErrFileNotFound
		case 1:
			data = entries[0]
		default:
			return FileInfo{}, fmt.Errorf("115 对单个文件返回了多条记录")
		}
	}
	var wire fileInfoWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return FileInfo{}, fmt.Errorf("解码 115 文件信息失败: %w", err)
	}
	if wire.ID == "" || wire.ID != fileID {
		return FileInfo{}, fmt.Errorf("115 返回的文件信息与请求不一致")
	}
	if wire.Category != "0" && wire.Category != "1" {
		return FileInfo{}, fmt.Errorf("115 返回未知的文件类型 %q", wire.Category)
	}
	var size int64
	if wire.Size != "" {
		if size, err = wire.Size.Int64(); err != nil {
			return FileInfo{}, fmt.Errorf("解码 115 文件大小失败: %w", err)
		}
	}
	info := FileInfo{
		File: File{
			ID:          wire.ID,
			ParentID:    pan115RootID,
			Name:        wire.Name,
			IsDirectory: wire.Category == "0",
			Size:        size,
			SizeKnown:   wire.Size != "",
			PickCode:    wire.PickCode,
		},
		Path: make([]Directory, len(wire.Paths)),
	}
	for index, item := range wire.Paths {
		info.Path[index] = Directory{ID: item.ID, Name: item.Name}
		info.ParentID = item.ID
	}
	return info, nil
}

type fileDownloadKey struct{}

// WithFileDownload 标记后台文件下载，使直链解析不占用播放专用的每秒一次配额。
// 全局请求间隔、并发上限、冷却和取消仍生效；该标记仅由内部下载入口设置。
func WithFileDownload(ctx context.Context) context.Context {
	return context.WithValue(ctx, fileDownloadKey{}, true)
}

// DownloadURL 用提取码换取带时效的下载直链。
// 115 会把直链绑定到换取直链时的 User-Agent，因此 userAgent 必须与最终播放端一致；
// 空字符串表示不携带 UA，与 115 官方客户端行为一致。
//
// 播放直链走单独的 1 请求/秒配额；WithFileDownload 标记的后台下载不使用该配额。
// 两种用途仍共享全局请求节流和限流冷却。
func (c *Client) DownloadURL(ctx context.Context, accessToken, pickCode, userAgent string) (string, error) {
	return callValue(c, ctx, http.MethodPost, func() (string, error) {
		if download, _ := ctx.Value(fileDownloadKey{}).(bool); !download {
			if err := c.admitPlay(ctx); err != nil {
				return "", err
			}
		}
		return c.downloadURLOnce(ctx, accessToken, pickCode, userAgent)
	})
}

// downloadURLOnce 是 DownloadURL 的单次实现；重试与直链配额由 DownloadURL 统一驱动。
func (c *Client) downloadURLOnce(ctx context.Context, accessToken, pickCode, userAgent string) (string, error) {
	// 115 的下载接口把结果直接放在 data 段里，以文件标识为键，不再嵌套 data 字段。
	type downloadEntry struct {
		URL struct {
			URL string `json:"url"`
		} `json:"url"`
	}
	ua := strings.TrimSpace(userAgent)
	raw, err := c.apiCallOnceWithUserAgent(ctx, http.MethodPost, c.api+"/open/ufile/downurl", accessToken,
		url.Values{"pick_code": {pickCode}}, &ua, "下载地址")
	if err != nil {
		return "", err
	}
	result, err := decodeData[map[string]downloadEntry](raw, "下载地址")
	if err != nil {
		return "", err
	}
	if len(result) == 0 {
		return "", ErrDownloadUnavailable
	}
	if len(result) != 1 {
		return "", fmt.Errorf("115 返回了多条下载地址")
	}
	var address string
	for _, item := range result {
		address = strings.TrimSpace(item.URL.URL)
	}
	if address == "" {
		return "", ErrDownloadUnavailable
	}
	return address, nil
}
