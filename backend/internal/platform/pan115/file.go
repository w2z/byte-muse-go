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
	ID          string
	ParentID    string
	Name        string
	IsDirectory bool
	Size        int64
	PickCode    string
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
	ID       string      `json:"fid"`
	ParentID string      `json:"pid"`
	Name     string      `json:"fn"`
	Category string      `json:"fc"`
	Size     json.Number `json:"fs"`
	PickCode string      `json:"pc"`
}

// fileListWire 是 115 对普通目录返回的对象形式；
// 根目录（cid=0）只返回条目数组，由 List 单独处理。
type fileListWire struct {
	CID   json.Number     `json:"cid"`
	Count json.Number     `json:"count"`
	Data  []fileEntryWire `json:"data"`
	Path  []struct {
		ID   json.Number `json:"cid"`
		Name string      `json:"name"`
	} `json:"path"`
}

// List 读取一个目录的分页内容。排序固定为文件名升序，
// 保证同一目录多次读取顺序稳定；返回的 cid 与路径末级必须等于请求的目录。
//
// 115 对普通目录返回 {cid,count,data,path} 对象，对根目录只返回条目数组，
// 两种形式在这里归一化为同一个 FilePage；数组形式不带路径与总数，
// 路径由上层用目录信息接口补齐，总数按本页条目数推断分页。
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
	raw, err := c.apiCall(ctx, http.MethodGet, appendQuery(c.api+"/open/ufile/files", query), accessToken, nil, "文件列表")
	if err != nil {
		return FilePage{}, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return FilePage{}, fmt.Errorf("115 未返回文件列表")
	}
	if raw[0] == '[' {
		var entries []fileEntryWire
		if err := json.Unmarshal(raw, &entries); err != nil {
			return FilePage{}, fmt.Errorf("解码 115 文件列表失败: %w", err)
		}
		return buildFilePage(entries, nil, -1, offset, limit)
	}
	var data fileListWire
	if err := json.Unmarshal(raw, &data); err != nil {
		return FilePage{}, fmt.Errorf("解码 115 文件列表失败: %w", err)
	}
	if data.CID.String() != directoryID || len(data.Path) == 0 || data.Path[len(data.Path)-1].ID.String() != directoryID {
		return FilePage{}, fmt.Errorf("115 返回的目录与请求不一致")
	}
	total, err := data.Count.Int64()
	if err != nil && data.Count != "" {
		return FilePage{}, fmt.Errorf("解码 115 文件数量失败: %w", err)
	}
	path := make([]Directory, len(data.Path))
	for index, item := range data.Path {
		path[index] = Directory{ID: item.ID.String(), Name: item.Name}
	}
	return buildFilePage(data.Data, path, int(total), offset, limit)
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
			ID:          item.ID,
			ParentID:    item.ParentID,
			Name:        item.Name,
			IsDirectory: item.Category == "0",
			Size:        size,
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
