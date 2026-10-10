package downloadclient

import (
	"bytemuse/backend/internal/ports"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// thunderAPIPrefix 是迅雷网盘（pan-xunlei-com）网关的固定路径前缀，旧版实现同样写死该前缀。
const thunderAPIPrefix = "/webman/3rdparty/pan-xunlei-com/index.cgi"

// thunderMinFileSize 与旧版一致：只挑选大于 1GB 的文件，避免把样片、截图、字幕一并加入下载。
const thunderMinFileSize = 1000000000

// thunderPanAuthPattern 提取网关首页内联脚本里 uiauth() 返回的 pan-auth 值。
var thunderPanAuthPattern = regexp.MustCompile(`uiauth\(.*?\)\s*{\s*return\s*"([^"]+)"`)

// Thunder 通过迅雷网盘网关提交磁力任务。
// 网关只接受磁力链接，不接受私有种子文件，因此它只实现 BT 侧的 MagnetDownloader；
// PT 资源仍由 qBittorrent/Transmission 承担。提交前必须先回查，避免重复入队。
type Thunder struct {
	origin, fileID, authorization string
	client                        *http.Client
}

// NewThunder configures one client without performing network activity.
func NewThunder(origin, fileID, authorization string, client *http.Client) *Thunder {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	clone := *client
	if clone.Timeout == 0 {
		clone.Timeout = 30 * time.Second
	}
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return fmt.Errorf("download client redirect rejected")
	}
	return &Thunder{origin: strings.TrimRight(strings.TrimSpace(origin), "/"), fileID: strings.TrimSpace(fileID), authorization: strings.TrimSpace(authorization), client: &clone}
}

func (c *Thunder) configured() error {
	if c.origin == "" || c.fileID == "" || c.authorization == "" {
		return fmt.Errorf("迅雷未配置")
	}
	if !strings.HasPrefix(c.origin, "http://") && !strings.HasPrefix(c.origin, "https://") {
		return fmt.Errorf("迅雷地址无效")
	}
	return nil
}

// do 发送一次带鉴权头的请求；panAuth 为空时不附加 pan-auth 头（首页探测阶段）。
func (c *Thunder) do(ctx context.Context, method, path, panAuth string, payload any, limit int64) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", c.authorization)
	if panAuth != "" {
		req.Header.Set("pan-auth", panAuth)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// 只回传状态码，避免把网盘响应体（可能含账号信息）写入日志。
		return nil, fmt.Errorf("迅雷网关返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("迅雷响应过大")
	}
	return raw, nil
}

// panAuth 从网关首页提取 pan-auth；网关每次请求都要求该值，因此不缓存。
func (c *Thunder) panAuth(ctx context.Context) (string, error) {
	raw, err := c.do(ctx, http.MethodGet, thunderAPIPrefix+"/", "", nil, 512<<10)
	if err != nil {
		return "", err
	}
	match := thunderPanAuthPattern.FindSubmatch(raw)
	if len(match) < 2 {
		return "", fmt.Errorf("迅雷授权 code 解析失败")
	}
	return string(match[1]), nil
}

type thunderTask struct {
	Phase  string `json:"phase"`
	Params struct {
		Target string `json:"target"`
		URL    string `json:"url"`
	} `json:"params"`
	InfoHash string `json:"info_hash"`
	Name     string `json:"name"`
}

func (c *Thunder) listTasks(ctx context.Context, taskType string) ([]thunderTask, error) {
	panAuth, err := c.panAuth(ctx)
	if err != nil {
		return nil, err
	}
	query := url.Values{"type": {taskType}, "device_space": {""}}
	raw, err := c.do(ctx, http.MethodGet, thunderAPIPrefix+"/drive/v1/tasks?"+query.Encode(), panAuth, nil, 4<<20)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Error         string        `json:"error"`
		Tasks         []thunderTask `json:"tasks"`
		NextPageToken string        `json:"next_page_token"`
		HasMore       bool          `json:"has_more"`
		Total         int           `json:"total"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("迅雷任务列表解析失败: %w", err)
	}
	if payload.Error != "" {
		return nil, fmt.Errorf("迅雷任务列表返回错误")
	}
	if payload.Tasks == nil || payload.NextPageToken != "" || payload.HasMore || payload.Total > len(payload.Tasks) {
		return nil, fmt.Errorf("迅雷任务列表不完整，无法确认任务缺失")
	}
	return payload.Tasks, nil
}

// deviceID 取当前网盘运行设备，任务必须落到该设备上才会真正开始下载。
func (c *Thunder) deviceID(ctx context.Context) (string, error) {
	tasks, err := c.listTasks(ctx, "user#runner")
	if err != nil {
		return "", err
	}
	for _, task := range tasks {
		if target := strings.TrimSpace(task.Params.Target); target != "" {
			return target, nil
		}
	}
	return "", fmt.Errorf("迅雷设备 ID 不可用")
}

type thunderResource struct {
	Name     string `json:"name"`
	FileSize int64  `json:"file_size"`
}

type thunderResourceList struct {
	List struct {
		Resources []struct {
			Name string `json:"name"`
			Dir  struct {
				Resources []thunderResource `json:"resources"`
			} `json:"dir"`
		} `json:"resources"`
	} `json:"list"`
}

// resolve 解析磁力链接并返回根名称与全部文件；调用方负责按大小过滤。
func (c *Thunder) resolve(ctx context.Context, magnet string) (string, []thunderResource, error) {
	panAuth, err := c.panAuth(ctx)
	if err != nil {
		return "", nil, err
	}
	raw, err := c.do(ctx, http.MethodPost, thunderAPIPrefix+"/drive/v1/resource/list", panAuth, map[string]any{"page_size": 1000, "urls": magnet}, 4<<20)
	if err != nil {
		return "", nil, err
	}
	var payload thunderResourceList
	if err = json.Unmarshal(raw, &payload); err != nil {
		return "", nil, fmt.Errorf("迅雷磁力解析结果无效: %w", err)
	}
	if len(payload.List.Resources) == 0 {
		return "", nil, fmt.Errorf("迅雷未解析到磁力内容")
	}
	root := payload.List.Resources[0]
	return root.Name, root.Dir.Resources, nil
}

// HasHash 通过任务列表按 info hash 回查，避免把同一磁力重复提交到网盘。
func (c *Thunder) HasHash(ctx context.Context, hash string) (bool, error) {
	state, err := c.Observe(ctx, hash)
	return state != nil, err
}

// Observe 保留离线任务完成事实；未知状态仍视为存在，列表不完整返回错误。
func (c *Thunder) Observe(ctx context.Context, hash string) (*ports.TransferState, error) {
	if err := c.configured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(hash) == "" {
		return nil, fmt.Errorf("无效的任务 hash")
	}
	tasks, err := c.listTasks(ctx, "user#download-url")
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if strings.EqualFold(strings.TrimSpace(task.InfoHash), hash) || strings.Contains(strings.ToLower(task.Params.URL), strings.ToLower(hash)) {
			status := "downloading"
			if task.Phase == "PHASE_TYPE_COMPLETE" {
				status = "completed"
			}
			return &ports.TransferState{Hash: hash, Status: status}, nil
		}
	}
	return nil, nil
}

// Submit 解析磁力、挑选大于 1GB 的文件并创建一次下载任务；结果由调用方回查确认。
func (c *Thunder) Submit(ctx context.Context, magnet string) error {
	if err := c.configured(); err != nil {
		return err
	}
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:") {
		return fmt.Errorf("invalid magnet")
	}
	deviceID, err := c.deviceID(ctx)
	if err != nil {
		return err
	}
	name, files, err := c.resolve(ctx, magnet)
	if err != nil {
		return err
	}
	indexes := make([]string, 0, len(files))
	var total int64
	for index, file := range files {
		if file.FileSize <= thunderMinFileSize {
			continue
		}
		indexes = append(indexes, fmt.Sprintf("%d", index))
		total += file.FileSize
	}
	if len(indexes) == 0 {
		return fmt.Errorf("迅雷磁力中没有大于 1GB 的文件")
	}
	panAuth, err := c.panAuth(ctx)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"params": map[string]string{
			"parent_folder_id": c.fileID,
			"url":              magnet,
			"target":           deviceID,
			"total_file_count": fmt.Sprintf("%d", len(files)),
			"sub_file_index":   strings.Join(indexes, ","),
		},
		"file_name": name,
		"file_size": fmt.Sprintf("%d", total),
		"name":      name,
		"type":      "user#download-url",
		"space":     deviceID,
	}
	if _, err = c.do(ctx, http.MethodPost, thunderAPIPrefix+"/drive/v1/task", panAuth, payload, 1<<20); err != nil {
		return err
	}
	return nil
}
