package application

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/ports"
)

// ErrVersionUnavailable 表示版本检查未装配发布源，HTTP 层据此返回 503。
var ErrVersionUnavailable = errors.New("版本检查未就绪")

const (
	// releaseCacheTTL 是成功结果的服务端缓存时长：同一小时内刷新页面不会重复访问 GitHub。
	releaseCacheTTL = time.Hour
	// releaseFailureTTL 是失败结果的缓存时长：上游故障时避免每次打开页面都重试。
	releaseFailureTTL = time.Minute
)

// VersionStatus 是检查更新的权威结果。
// 远端检查失败不视为接口失败：CheckError 非空时 Latest 为空、HasUpdate 为 false，页面仍显示当前版本。
type VersionStatus struct {
	// Current 是当前运行版本，未注入构建版本时为 dev。
	Current string `json:"current"`
	// Latest 是发布仓库当前记录的版本；检查失败时为空。
	Latest string `json:"latest"`
	// HasUpdate 表示 Latest 高于 Current，可据此提示升级。
	HasUpdate bool `json:"has_update"`
	// ReleaseURL 是发布仓库地址，取自 version.json 的 source。
	ReleaseURL string `json:"release_url"`
	// CheckedAt 是本次结论的产生时间，UTC RFC 3339。
	CheckedAt string `json:"checked_at"`
	// CheckError 是远端检查的脱敏失败原因，已确认可用时为空。
	CheckError string `json:"check_error"`
}

// VersionService 比较当前运行版本与发布仓库记录的版本。
// 远端结果按固定时长缓存在进程内：同一实例的多次页面加载只产生一次上游请求，未认证额度不会被刷新耗尽。
type VersionService struct {
	current string
	source  ports.ReleaseSource
	now     func() time.Time

	// mu 在读取远端期间保持持有：检查更新是低频冷路径，串行化可确保每个缓存窗口最多一次上游请求。
	mu        sync.Mutex
	cached    ports.ReleaseInfo
	cachedErr error
	cachedAt  time.Time
}

// NewVersionService 构建版本检查服务；source 为 nil 时 Status 返回 ErrVersionUnavailable。
func NewVersionService(current string, source ports.ReleaseSource) *VersionService {
	return &VersionService{current: strings.TrimSpace(current), source: source, now: time.Now}
}

// Current 返回当前运行版本；未注入构建版本时回退 dev，与 Agent 运行环境提示保持一致。
func (s *VersionService) Current() string {
	if s == nil || s.current == "" {
		return "dev"
	}
	return s.current
}

// Status 返回当前版本与远端版本的比较结果。
// 只有发布源未装配才返回错误；远端失败写入 CheckError，保证页面始终能显示当前版本。
func (s *VersionService) Status(ctx context.Context) (VersionStatus, error) {
	if s == nil || s.source == nil {
		return VersionStatus{}, ErrVersionUnavailable
	}
	status := VersionStatus{Current: s.Current(), CheckedAt: s.now().UTC().Format(time.RFC3339)}
	info, err := s.latest(ctx)
	if err != nil {
		status.CheckError = err.Error()
		return status, nil
	}
	status.Latest = info.Version
	status.ReleaseURL = info.Source
	status.HasUpdate = compareVersions(status.Current, info.Version) < 0
	return status, nil
}

// latest 读取远端版本，命中缓存时直接返回；成功与失败使用不同缓存时长。
func (s *VersionService) latest(ctx context.Context) (ports.ReleaseInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cachedAt.IsZero() {
		ttl := releaseCacheTTL
		if s.cachedErr != nil {
			ttl = releaseFailureTTL
		}
		if s.now().Sub(s.cachedAt) < ttl {
			return s.cached, s.cachedErr
		}
	}
	info, err := s.source.Latest(ctx)
	s.cached, s.cachedErr, s.cachedAt = info, err, s.now()
	return info, err
}

// compareVersions 按点分数字段比较版本，返回 -1/0/1。
// 任一侧包含非数字字段（如 dev 或自定义标识）时返回 0：无法判定升级方向时不提示更新。
func compareVersions(current, latest string) int {
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
