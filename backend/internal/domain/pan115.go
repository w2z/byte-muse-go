package domain

import "time"

// Pan115LoginState 是一次扫码登录对外可见的状态；取值与 115 协议状态一一对应。
type Pan115LoginState string

const (
	Pan115LoginWaiting    Pan115LoginState = "waiting"
	Pan115LoginScanned    Pan115LoginState = "scanned"
	Pan115LoginAuthorized Pan115LoginState = "authorized"
	Pan115LoginExpired    Pan115LoginState = "expired"
	Pan115LoginCanceled   Pan115LoginState = "canceled"
)

// Pan115LoginSession 是一次扫码登录的启动结果；QRCode 为可直接渲染的 PNG data URL。
type Pan115LoginSession struct {
	SessionID string    `json:"session_id"`
	QRCode    string    `json:"qr_code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Pan115LoginResult 是一次扫码状态查询结果；仅授权状态携带账号快照。
type Pan115LoginResult struct {
	Status  Pan115LoginState `json:"status"`
	Account *Pan115Account   `json:"account"`
}

// Pan115Account 是 115 账号的对外快照，只包含展示字段，不含任何令牌。
type Pan115Account struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Avatar string      `json:"avatar"`
	Level  string      `json:"level"`
	Space  Pan115Space `json:"space"`
}

// Pan115Space 是容量快照；Formatted 直接使用 115 提供的展示文本。
type Pan115Space struct {
	Total     Pan115SpaceAmount `json:"total"`
	Used      Pan115SpaceAmount `json:"used"`
	Remaining Pan115SpaceAmount `json:"remaining"`
}

// Pan115SpaceAmount 是单个容量数值；Size 为字节数，零值表示 115 未返回该字段。
type Pan115SpaceAmount struct {
	Size      int64  `json:"size"`
	Formatted string `json:"formatted"`
}

// Pan115Directory 是 115 目录路径上的一级。
type Pan115Directory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Pan115File 是 115 目录里的一个条目；PickCode 供后续播放直链使用，本身不含凭据。
type Pan115File struct {
	ID          string `json:"id"`
	ParentID    string `json:"parent_id"`
	Name        string `json:"name"`
	IsDirectory bool   `json:"is_directory"`
	Size        int64  `json:"size"`
	PickCode    string `json:"pick_code"`
}

// Pan115FilePage 是一页目录内容；Path 为从根目录到当前目录的完整路径。
type Pan115FilePage struct {
	DirectoryID string            `json:"directory_id"`
	Path        []Pan115Directory `json:"path"`
	Files       []Pan115File      `json:"files"`
	Total       int               `json:"total"`
	HasMore     bool              `json:"has_more"`
}

// Pan115OfflineTask 是 115 离线任务的一次快照；Progress 已归一化为 0-100。
type Pan115OfflineTask struct {
	Hash        string `json:"hash"`
	Status      int    `json:"status"`
	Progress    int    `json:"progress"`
	FileID      string `json:"file_id"`
	DirectoryID string `json:"directory_id"`
}

// Pan115OfflinePage 是一页离线任务。
type Pan115OfflinePage struct {
	Page      int                 `json:"page"`
	PageCount int                 `json:"page_count"`
	Tasks     []Pan115OfflineTask `json:"tasks"`
}
