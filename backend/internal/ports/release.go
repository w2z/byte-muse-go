package ports

import "context"

// ReleaseInfo 是发布仓库 version.json 的权威字段子集。
// 只保留检查更新与展示需要的字段；版本回写提交产生的镜像、摘要、标签等元数据不进入业务层。
type ReleaseInfo struct {
	// Version 是发布版本号，形如 0.1.21；为空表示远端记录无效。
	Version string
	// Commit 是发布版本对应的完整提交号，用于展示与追溯。
	Commit string
	// BuiltAt 是构建时间，沿用 version.json 中的 UTC RFC 3339 文本。
	BuiltAt string
	// Source 是发布仓库地址，用于在前端跳转查看发布记录。
	Source string
}

// ReleaseSource 读取发布仓库当前记录的版本。
// 实现负责超时、限流和响应校验；错误信息必须已脱敏，不得包含凭据或上游正文。
type ReleaseSource interface {
	Latest(context.Context) (ReleaseInfo, error)
}
