package domain

// strm 映射的网盘类型。取值同时用作播放地址 /files/play/{kind}/ 的路径段，
// 使设置项、strm 内容与 HTTP 路由共用同一套命名，避免两处漂移。
const (
	StrmKindPan115      = "115"
	StrmKindCloudDrive2 = "cd2"
)

// 排除关键字的匹配方式。四种写法共用同一份匹配实现，取值进入设置项 STRM_PATHS，
// 由前端渲染成「等于:xxx / 前缀:xxx* / 后缀:*xxx / 包含:*xxx*」。
const (
	StrmExcludeModeEquals   = "equals"
	StrmExcludeModePrefix   = "prefix"
	StrmExcludeModeSuffix   = "suffix"
	StrmExcludeModeContains = "contains"
)

// StrmExcludeKeyword 是一条排除规则：Mode 决定 Value 的匹配方式，匹配一律不区分大小写。
// Value 为空串的规则在保存时被丢弃，因此运行期不必再判空。
type StrmExcludeKeyword struct {
	Mode  string `json:"mode"`
	Value string `json:"value"`
}

// StrmMapping 是一条「网盘目录 → 本地 strm 目录」映射，来自设置项 STRM_PATHS。
// ID 是网盘目录标识（115 为目录 ID，CloudDrive2 为目录绝对路径），是扫描与播放时的权威依据；
// Path 是网盘目录展示路径，只用于界面展示。
type StrmMapping struct {
	Kind      string   `json:"kind"`
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	LocalPath string   `json:"local_path"`
	Formats   []string `json:"formats"`
	// MinSizeMB 是生成 strm 的最小视频体积（MB）；0 表示不限制，用于跳过样本、预告片等小文件。
	MinSizeMB int `json:"min_size_mb"`
	// Exclude 是排除规则：文件或文件夹名命中任一规则时跳过，不区分大小写。
	// 命中文件夹时整棵子树跳过，命中文件时该文件不生成 strm。
	Exclude []StrmExcludeKeyword `json:"exclude"`
}

// StrmProxyTarget 描述一次需要服务端转发的播放请求。
// CloudDrive2 的直链可能要求特定 User-Agent 与附加请求头，302 无法携带，只能由服务端转发。
type StrmProxyTarget struct {
	URL       string            `json:"url"`
	UserAgent string            `json:"user_agent"`
	Headers   map[string]string `json:"headers"`
}

// StrmPlayTarget 是一次播放解析结果；Redirect 与 Proxy 恰好只有一个非空。
type StrmPlayTarget struct {
	Redirect string           `json:"redirect"`
	Proxy    *StrmProxyTarget `json:"proxy"`
}

// StrmDirectory 是本地 strm 目录下的一个子目录。
// Path 是相对 strm 根目录的绝对形式路径（以 / 开头），浏览器只能浏览该根目录以下的内容。
type StrmDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// StrmDirectoryPage 是一次本地目录浏览结果；Path 为当前目录相对 strm 根目录的路径。
type StrmDirectoryPage struct {
	Path        string          `json:"path"`
	Directories []StrmDirectory `json:"directories"`
}

// StrmScanMapping 是单条映射的生成结果；Message 为空表示该映射没有可报告的问题。
type StrmScanMapping struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	LocalPath string `json:"local_path"`
	Files     int    `json:"files"`
	Created   int    `json:"created"`
	Unchanged int    `json:"unchanged"`
	Failed    int    `json:"failed"`
	Message   string `json:"message"`
}

// StrmEmbyResult 描述生成 strm 之后的 Emby 媒体库刷新结果。
// Attempted 为 false 表示未配置 Emby 或未开启自动刷新，此时 Refreshed 与 Message 均为空。
type StrmEmbyResult struct {
	Attempted bool   `json:"attempted"`
	Refreshed bool   `json:"refreshed"`
	Message   string `json:"message"`
}

// StrmScanResult 是一次 strm 生成的总结果。
type StrmScanResult struct {
	Mappings []StrmScanMapping `json:"mappings"`
	Files    int               `json:"files"`
	Created  int               `json:"created"`
	Failed   int               `json:"failed"`
	Emby     StrmEmbyResult    `json:"emby"`
}
