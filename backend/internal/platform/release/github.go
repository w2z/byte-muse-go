// Package release 从发布仓库读取版本记录，为「检查更新」提供远端版本来源。
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"bytemuse/backend/internal/ports"
)

const (
	// versionFilePath 是构建流程回写的版本记录路径，固定位于仓库根目录。
	versionFilePath = "version.json"
	// defaultEndpoint 是 GitHub 原始文件服务基址：直接返回文件正文，不占用 API 额度。
	defaultEndpoint = "https://raw.githubusercontent.com"
	// defaultBranchRef 使用 HEAD 跟随仓库默认分支，避免把分支名写死在代码里。
	defaultBranchRef = "HEAD"
	// maxResponseBytes 限制响应体积，避免异常响应占用内存。
	maxResponseBytes = 1 << 20
	// requestTimeout 限制单次检查更新的耗时，避免拖住调用方请求。
	requestTimeout = 10 * time.Second
)

// repoPattern 限定 owner/name 形式，防止把可配置文本拼进请求路径。
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// GitHubSource 通过 raw.githubusercontent.com 读取 version.json 的原始内容。
// 只读、匿名：公开仓库按未认证额度访问，服务端缓存负责压低请求量，不携带任何凭据。
type GitHubSource struct {
	repo     string
	endpoint string
	client   *http.Client
}

// NewGitHubSource 构建发布源；repo 为 owner/name，client 为空时使用默认客户端。
func NewGitHubSource(repo string, client *http.Client) *GitHubSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &GitHubSource{repo: strings.TrimSpace(repo), endpoint: defaultEndpoint, client: client}
}

// Latest 读取发布仓库当前的版本记录。返回的错误只描述本地判定结果，不回显上游正文或凭据。
func (s *GitHubSource) Latest(ctx context.Context) (ports.ReleaseInfo, error) {
	if !repoPattern.MatchString(s.repo) {
		return ports.ReleaseInfo{}, fmt.Errorf("发布仓库 %q 不是 owner/name 形式", s.repo)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/"+s.repo+"/"+defaultBranchRef+"/"+versionFilePath, nil)
	if err != nil {
		return ports.ReleaseInfo{}, errors.New("检查更新的请求构建失败")
	}
	// 原始文件服务按分支路径返回正文，不需要 Contents API 的 base64 包装。
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "bytemuse")
	// 不跟随重定向：版本记录只来自固定仓库路径，避免被重定向到其他主机。
	bounded := *s.client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := bounded.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ports.ReleaseInfo{}, errors.New("检查更新超时，请稍后重试")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return ports.ReleaseInfo{}, errors.New("检查更新已取消")
		}
		return ports.ReleaseInfo{}, errors.New("无法访问 GitHub 发布仓库，请检查服务所在网络的连通性")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusNotFound:
			return ports.ReleaseInfo{}, errors.New("发布仓库中未找到 version.json（仓库不可见或文件不存在）")
		case http.StatusForbidden, http.StatusTooManyRequests:
			return ports.ReleaseInfo{}, fmt.Errorf("GitHub 访问受限（HTTP %d），请稍后重试", response.StatusCode)
		default:
			return ports.ReleaseInfo{}, fmt.Errorf("GitHub 请求失败（HTTP %d）", response.StatusCode)
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return ports.ReleaseInfo{}, errors.New("读取发布版本记录失败")
	}
	var record struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		BuiltAt string `json:"built_at"`
		Source  string `json:"source"`
	}
	if json.Unmarshal(body, &record) != nil {
		return ports.ReleaseInfo{}, errors.New("发布版本记录格式无效")
	}
	record.Version = strings.TrimSpace(record.Version)
	if record.Version == "" {
		return ports.ReleaseInfo{}, errors.New("发布版本记录缺少版本号")
	}
	return ports.ReleaseInfo{
		Version: record.Version,
		Commit:  strings.TrimSpace(record.Commit),
		BuiltAt: strings.TrimSpace(record.BuiltAt),
		Source:  strings.TrimSpace(record.Source),
	}, nil
}
