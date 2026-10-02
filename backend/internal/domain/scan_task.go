package domain

import "encoding/json"

// ScanProgress 保存动态发现的文件总数；已处理包含成功、跳过和失败项。
type ScanProgress struct {
	Phase     string `json:"phase"`
	Processed int    `json:"processed"`
	Total     int    `json:"total"`
	Percent   int    `json:"percent"`
	Current   string `json:"current"`
}

// ScanTask 是独立扫描或生成任务的持久化快照，不包含网盘凭据。
// 服务重启将活动状态改为 interrupted；取消和中断不回滚已经完成的业务写入。
type ScanTask struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	State     string          `json:"state"`
	Mode      string          `json:"mode"`
	Progress  ScanProgress    `json:"progress"`
	Result    json.RawMessage `json:"result"`
	Error     string          `json:"error"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// Active 表示任务仍占用对应操作入口，包括暂停与停止请求尚未完成的状态。
func (t ScanTask) Active() bool {
	switch t.State {
	case "running", "pausing", "paused", "canceling":
		return true
	}
	return false
}
