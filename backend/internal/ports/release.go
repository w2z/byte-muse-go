package ports

import (
	"context"
	"strconv"
	"strings"
)

// ReleaseInfo 是发布仓库 version.json 的权威字段子集。
// 只保留检查更新与展示需要的字段；版本回写提交产生的镜像、摘要、标签等元数据不进入业务层。
type ReleaseInfo struct {
	// Version 是发布版本号，形如 0.1.21；为空表示远端记录无效。
	Version string
	// Commit 是发布版本对应的完整提交号，用于展示与追溯。
	Commit string
	// BuiltAt 是构建时间，沿用 version.json 中的 UTC RFC 3339 文本。
	BuiltAt string
	// Source 是发布仓库地址，仅作为发布来源元数据。
	Source string
	// Changes 是发布脚本从构建提交生成的版本说明历史；旧版本文件可缺省。
	Changes []ReleaseChange
}

// ReleaseChange 将提交说明与其发布版本绑定，供服务按升级区间筛选。
type ReleaseChange struct {
	Version string `json:"version"`
	Message string `json:"message"`
}

// ReleaseSource 读取发布仓库当前记录的版本。
// 实现负责超时、限流和响应校验；错误信息必须已脱敏，不得包含凭据或上游正文。
type ReleaseSource interface {
	Latest(context.Context) (ReleaseInfo, error)
}

// UpgradeStatus 描述容器内升级任务；success 仅在新服务健康检查通过后写入。
type UpgradeStatus struct {
	// PhaseProgressPercent 为当前步骤的独立进度；切换步骤归零，通过校验才达到 100。
	PhaseProgressPercent int `json:"phase_progress_percent"`
	// ProgressPercent 为按四阶段等权折算的实际进度，成功前不达到 100。
	ProgressPercent int `json:"progress_percent"`
	// ProgressIndeterminate 表示当前阶段尚无可计算总量，页面显示处理中而非伪造百分比。
	ProgressIndeterminate bool `json:"progress_indeterminate"`
	// CompletedSteps 为已完成的下载、解压、安装、重启步骤数，失败保留当前值。
	CompletedSteps int    `json:"completed_steps"`
	Enabled        bool   `json:"enabled"`
	Phase          string `json:"phase"`
	Target         string `json:"target"`
	Error          string `json:"error"`
}

// UpgradeInstaller 下载校验并暂存完整升级包；提交后由常驻启动器切换服务。
type UpgradeInstaller interface {
	// Stage 在持久化阶段后同步报告状态；回调不得在返回后继续调用。
	Stage(context.Context, string, func(UpgradeStatus)) error
	Status() UpgradeStatus
}

// CompareVersions 按点分数字段比较版本，返回 -1/0/1。
// 任一侧包含非数字字段（如 dev 或自定义标识）时返回 0：无法判定升级方向时不提示更新。
func CompareVersions(current, latest string) int {
	left, okLeft := versionSegments(current)
	right, okRight := versionSegments(latest)
	if !okLeft || !okRight {
		return 0
	}
	for index := 0; index < len(left) || index < len(right); index++ {
		leftValue, rightValue := 0, 0
		if index < len(left) {
			leftValue = left[index]
		}
		if index < len(right) {
			rightValue = right[index]
		}
		if leftValue != rightValue {
			if leftValue < rightValue {
				return -1
			}
			return 1
		}
	}
	return 0
}

// versionSegments 解析点分数字版本；允许前缀 v，出现空段或非数字段即视为不可比较。
func versionSegments(value string) ([]int, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	if value == "" {
		return nil, false
	}
	parts := strings.Split(value, ".")
	segments := make([]int, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, false
		}
		segments = append(segments, number)
	}
	return segments, true
}
