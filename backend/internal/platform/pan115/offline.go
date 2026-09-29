package pan115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// offlineExistsCode 是 115 表示「同一磁力已在离线列表」的错误码。
const offlineExistsCode = 10008

// ErrOfflineExists 表示该磁力已存在于 115 离线任务中。
var ErrOfflineExists = errors.New("115 已存在该离线任务")

// OfflineTask 是 115 离线任务的一次快照。
type OfflineTask struct {
	Hash        string
	Status      int
	Progress    int
	FileID      string
	DirectoryID string
}

// OfflinePage 是一页离线任务。
type OfflinePage struct {
	PageCount int
	Tasks     []OfflineTask
}

type offlineTaskWire struct {
	Hash        string      `json:"info_hash"`
	Status      int         `json:"status"`
	Progress    json.Number `json:"percentDone"`
	FileID      string      `json:"file_id"`
	DirectoryID string      `json:"wp_path_id"`
}

// task 归一化一个离线任务；115 可能返回小数或数字字符串形式的进度。
func (wire offlineTaskWire) task() (OfflineTask, error) {
	var progress float64
	if wire.Progress != "" {
		parsed, err := wire.Progress.Float64()
		if err != nil {
			return OfflineTask{}, fmt.Errorf("解码 115 离线进度失败: %w", err)
		}
		progress = parsed
	}
	return OfflineTask{
		Hash:        wire.Hash,
		Status:      wire.Status,
		Progress:    int(min(100, max(0, progress))),
		FileID:      wire.FileID,
		DirectoryID: wire.DirectoryID,
	}, nil
}

// AddOffline 提交一个磁力或 URL 离线任务到指定目录，返回 115 侧的信息哈希。
func (c *Client) AddOffline(ctx context.Context, accessToken, uri, directoryID string) (string, error) {
	type addItem struct {
		State   bool   `json:"state"`
		Code    int    `json:"code"`
		Message string `json:"message"`
		Hash    string `json:"info_hash"`
	}
	response, err := c.doMultipart(ctx, c.api+"/open/offline/add_task_urls", accessToken, url.Values{
		"urls":       {uri},
		"wp_path_id": {directoryID},
	})
	if err != nil {
		return "", err
	}
	body, err := readBody(response)
	if err != nil {
		return "", err
	}
	var envelope struct {
		State   bool      `json:"state"`
		Code    int       `json:"code"`
		Message string    `json:"message"`
		Data    []addItem `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("解码 115 离线提交响应失败: %w", err)
	}
	if !envelope.State || envelope.Code != 0 {
		return "", &APIError{Code: envelope.Code, Message: envelope.Message}
	}
	if len(envelope.Data) != 1 {
		return "", fmt.Errorf("115 对一个离线任务返回了 %d 条结果", len(envelope.Data))
	}
	item := envelope.Data[0]
	if !item.State || item.Code != 0 {
		if item.Code == offlineExistsCode {
			return "", ErrOfflineExists
		}
		return "", &APIError{Code: item.Code, Message: item.Message}
	}
	if item.Hash == "" {
		return "", fmt.Errorf("115 离线提交响应缺少信息哈希")
	}
	return item.Hash, nil
}

// OfflineTasks 读取一页离线任务。
func (c *Client) OfflineTasks(ctx context.Context, accessToken string, page int) (OfflinePage, error) {
	type tasksWire struct {
		PageCount int               `json:"page_count"`
		Tasks     []offlineTaskWire `json:"tasks"`
	}
	data, err := apiGet[*tasksWire](ctx, c, c.api+"/open/offline/get_task_list", accessToken, url.Values{
		"page": {strconv.Itoa(page)},
	}, "离线任务")
	if err != nil {
		return OfflinePage{}, err
	}
	if data == nil {
		return OfflinePage{}, fmt.Errorf("115 离线任务响应缺少数据段")
	}
	result := OfflinePage{PageCount: data.PageCount, Tasks: make([]OfflineTask, len(data.Tasks))}
	for index, wire := range data.Tasks {
		task, err := wire.task()
		if err != nil {
			return OfflinePage{}, err
		}
		result.Tasks[index] = task
	}
	return result, nil
}

// RemoveOffline 只删除离线任务记录，不删除已下载的源文件。
func (c *Client) RemoveOffline(ctx context.Context, accessToken, hash string) error {
	_, err := apiPost[struct{}](ctx, c, c.api+"/open/offline/del_task", accessToken, url.Values{
		"info_hash":       {hash},
		"del_source_file": {"0"},
	}, "离线任务删除")
	return err
}
