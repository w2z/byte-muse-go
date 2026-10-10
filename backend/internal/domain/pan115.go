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

// Pan115CookieLoginSession 是一次 Cookie 扫码的启动结果；QRCode 为可直接渲染的 PNG data URL。
// ClientType 回显归一化后的渠道，前端据此提示用户用哪个客户端扫码。
type Pan115CookieLoginSession struct {
	SessionID  string    `json:"session_id"`
	QRCode     string    `json:"qr_code"`
	ClientType string    `json:"client_type"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Pan115CookieLoginResult 是一次 Cookie 扫码状态查询结果；仅授权状态携带 Cookie 明文。
// Cookie 只在本次响应中返回，由设置页写入 PAN115_COOKIE 后统一加密保存，服务端不落库。
type Pan115CookieLoginResult struct {
	Status Pan115LoginState `json:"status"`
	Cookie string           `json:"cookie"`
}

// Pan115Account 是 115 账号的对外快照，只包含展示字段，不含任何令牌。
type Pan115Account struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Avatar string      `json:"avatar"`
	Level  string      `json:"level"`
	Space  Pan115Space `json:"space"`
	// Quota 是云下载配额；115 未提供该数据时为 null，调用方据此只隐藏配额展示。
	Quota *Pan115Quota `json:"quota"`
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

// Pan115Quota 是云下载配额快照；115 以任务个数计量，三个字段都是任务数而不是字节。
type Pan115Quota struct {
	Total     int `json:"total"`
	Used      int `json:"used"`
	Remaining int `json:"remaining"`
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
	// SizeKnown 表示网盘是否回传了体积；为 false 时 Size 为 0 只代表未知，不能判定文件为空。
	SizeKnown bool   `json:"size_known"`
	PickCode  string `json:"pick_code"`
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

// Pan115LibraryDirectoryResult 是单个扫描目录的入库结果。
// Files 是递归扫描到的视频文件数，Matched 是识别出番号的影片数，Skipped 是无法识别番号而跳过的文件数。
// Message 为空表示该目录没有可报告的问题；单个目录失败不影响其他目录。
type Pan115LibraryDirectoryResult struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Files   int    `json:"files"`
	Matched int    `json:"matched"`
	Created int    `json:"created"`
	Skipped int    `json:"skipped"`
	Message string `json:"message"`
}

// Pan115LibraryScanResult 是一次「115 扫描目录 → 递归扫描 → 视频入库」的总结果。
// Created 只统计本次新建的影片；已存在的影片只把媒体库状态置为 present，不覆盖标题、订阅状态等已有字段。
type Pan115LibraryScanResult struct {
	Directories []Pan115LibraryDirectoryResult `json:"directories"`
	Files       int                            `json:"files"`
	Matched     int                            `json:"matched"`
	Created     int                            `json:"created"`
	Skipped     int                            `json:"skipped"`
}
