package pan115

import (
	"context"
	"encoding/json"
	"fmt"
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

// List 读取一个目录的分页内容。排序固定为文件名升序，
// 保证同一目录多次读取顺序稳定；返回的 cid 与路径末级必须等于请求的目录。
func (c *Client) List(ctx context.Context, accessToken, directoryID string, offset, limit int) (FilePage, error) {
	type fileListWire struct {
		CID   json.Number `json:"cid"`
		Count json.Number `json:"count"`
		Data  []struct {
			ID       string      `json:"fid"`
			ParentID string      `json:"pid"`
			Name     string      `json:"fn"`
			Category string      `json:"fc"`
			Size     json.Number `json:"fs"`
			PickCode string      `json:"pc"`
		} `json:"data"`
		Path []struct {
			ID   json.Number `json:"cid"`
			Name string      `json:"name"`
		} `json:"path"`
	}
	data, err := apiGet[fileListWire](ctx, c, c.api+"/open/ufile/files", accessToken, url.Values{
		"cid":      {directoryID},
		"offset":   {strconv.Itoa(offset)},
		"limit":    {strconv.Itoa(limit)},
		"show_dir": {"1"},
		"stdir":    {"1"},
		"cur":      {"1"},
		"o":        {"file_name"},
		"asc":      {"1"},
	}, "文件列表")
	if err != nil {
		return FilePage{}, err
	}
	if data.CID.String() != directoryID || len(data.Path) == 0 || data.Path[len(data.Path)-1].ID.String() != directoryID {
		return FilePage{}, fmt.Errorf("115 返回的目录与请求不一致")
	}
	total, err := data.Count.Int64()
	if err != nil && data.Count != "" {
		return FilePage{}, fmt.Errorf("解码 115 文件数量失败: %w", err)
	}
	page := FilePage{
		Files:   make([]File, len(data.Data)),
		Path:    make([]Directory, len(data.Path)),
		Total:   int(total),
		HasMore: int64(offset+len(data.Data)) < total,
	}
	for index, item := range data.Data {
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
	for index, item := range data.Path {
		page.Path[index] = Directory{ID: item.ID.String(), Name: item.Name}
	}
	return page, nil
}
