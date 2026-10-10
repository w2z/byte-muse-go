package pan115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Directory 是 115 目录路径上的一级。
type Directory struct {
	ID   string
	Name string
}

// File 是 115 目录里的一个条目。
type File struct {
	SHA1        string
	ID          string
	ParentID    string
	Name        string
	IsDirectory bool
	Size        int64
	// SizeKnown 表示 115 是否回传了体积（fs 字段）。体积为 0 且已知才代表空文件；
	// 字段缺失时体积按未知处理，不能据此判定文件为空。
	SizeKnown bool
	PickCode  string
}

// FilePage 是一页目录内容。
type FilePage struct {
	Files   []File
	Path    []Directory
	Total   int
	HasMore bool
}

// fileEntryWire 是 115 文件列表里的一条记录。
type fileEntryWire struct {
	SHA1     string      `json:"sha1"`
	ID       string      `json:"fid"`
	ParentID string      `json:"pid"`
	Name     string      `json:"fn"`
	Category string      `json:"fc"`
	Size     json.Number `json:"fs"`
	PickCode string      `json:"pc"`
}

// directoryWire 是 115 回传的父目录树节点；115 会用数字或字符串表示同一个目录 ID。
type directoryWire struct {
	ID   json.Number `json:"cid"`
	Name string      `json:"name"`
}

// fileListWire 是 115 文件列表接口的完整响应：
// 条目在 data，当前目录的总数与父目录树（末级为当前目录）在 data 同级的 count、path。
// 只解析 data 段会丢掉路径与总数，因此这里读取完整响应体。
type fileListWire struct {
	Count json.Number     `json:"count"`
	Data  []fileEntryWire `json:"data"`
	Path  []directoryWire `json:"path"`
}

// List 读取一个目录的分页内容。排序固定为文件名升序，
// 保证同一目录多次读取顺序稳定；回传的父目录树末级必须等于请求的目录。
//
// 115 的文件列表接口同时返回条目、当前目录总数与父目录树，
// 一次请求即可拿到面包屑与分页总数，不需要再调用目录信息接口补齐路径。
// 仅当 115 未回传父目录树时才返回空路径，由上层用目录信息接口兜底。
func (c *Client) List(ctx context.Context, accessToken, directoryID string, offset, limit int) (FilePage, error) {
	query := url.Values{
		"cid":      {directoryID},
		"offset":   {strconv.Itoa(offset)},
		"limit":    {strconv.Itoa(limit)},
		"show_dir": {"1"},
		"stdir":    {"1"},
		"cur":      {"1"},
		"o":        {"file_name"},
		"asc":      {"1"},
	}
	var wire fileListWire
	if err := c.apiCallInto(ctx, http.MethodGet, appendQuery(c.api+"/open/ufile/files", query), accessToken, nil, "文件列表", &wire); err != nil {
		return FilePage{}, err
	}
	path := make([]Directory, len(wire.Path))
	for index, item := range wire.Path {
		path[index] = Directory{ID: item.ID.String(), Name: item.Name}
	}
	if len(path) > 0 && path[len(path)-1].ID != directoryID {
		return FilePage{}, fmt.Errorf("115 返回的目录与请求不一致")
	}
	total := -1
	if wire.Count != "" {
		value, err := wire.Count.Int64()
		if err != nil {
			return FilePage{}, fmt.Errorf("解码 115 文件数量失败: %w", err)
		}
		total = int(value)
	}
	return buildFilePage(wire.Data, path, total, offset, limit)
}

// buildFilePage 把 115 的目录条目转换成 FilePage；total 为 -1 表示 115 未回传总数，
// 此时按本页条目数推断是否还有下一页。
func buildFilePage(entries []fileEntryWire, path []Directory, total, offset, limit int) (FilePage, error) {
	page := FilePage{
		Files: make([]File, len(entries)),
		Path:  path,
	}
	for index, item := range entries {
		if item.Category != "0" && item.Category != "1" {
			return FilePage{}, fmt.Errorf("115 返回未知的文件类型 %q", item.Category)
		}
		size, err := item.Size.Int64()
		if err != nil && item.Size != "" {
			return FilePage{}, fmt.Errorf("解码 115 文件大小失败: %w", err)
		}
		page.Files[index] = File{
			SHA1:        item.SHA1,
			ID:          item.ID,
			ParentID:    item.ParentID,
			Name:        item.Name,
			IsDirectory: item.Category == "0",
			Size:        size,
			SizeKnown:   item.Size != "",
			PickCode:    item.PickCode,
		}
	}
	if total < 0 {
		page.Total = offset + len(entries)
		page.HasMore = limit > 0 && len(entries) >= limit
		return page, nil
	}
	page.Total = total
	page.HasMore = offset+len(entries) < total
	return page, nil
}
