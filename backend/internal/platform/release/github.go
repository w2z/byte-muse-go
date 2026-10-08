// Package release 从发布仓库读取版本记录，为「检查更新」提供远端版本来源。
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"bytemuse/backend/internal/ports"
)

const (
	// versionFilePath 是提交钩子维护的版本记录路径，固定位于仓库根目录。
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

// NewProxyClient 仅在直连失败后读取设置页 PROXY；未配置返回 nil，跳过代理回退。
// 每次回退读取最新配置，不把代理地址或凭据写入错误。
func NewProxyClient(settings func(context.Context) (map[string]string, error)) func(context.Context) (*http.Client, error) {
	return func(ctx context.Context) (*http.Client, error) {
		if settings == nil {
			return nil, nil
		}
		values, err := settings(ctx)
		if err != nil {
			return nil, errors.New("无法读取更新代理设置")
		}
		raw := strings.TrimSpace(values["PROXY"])
		if raw == "" {
			return nil, nil
		}
		proxy, err := url.Parse(raw)
		if err != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
			return nil, errors.New("更新代理配置无效")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: requestTimeout, KeepAlive: 30 * time.Second}).DialContext
		transport.ResponseHeaderTimeout = requestTimeout
		transport.Proxy = http.ProxyURL(proxy)
		return &http.Client{Transport: transport}, nil
	}
}

// directClient 显式禁用环境代理，保证首轮和 CDN 回退均为直连。
func directClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: requestTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = requestTimeout
	transport.Proxy = nil
	return &http.Client{Transport: transport}
}

// GitHubSource 通过 raw.githubusercontent.com 读取 version.json 的原始内容。
// 只读、匿名：公开仓库按未认证额度访问，服务端缓存负责压低请求量，不携带任何凭据。
type GitHubSource struct {
	repo        string
	endpoint    string
	client      *http.Client
	cdnEndpoint string
	// ProxyClient 仅在 raw 和 CDN 均失败后调用；nil 表示未提供代理。
	ProxyClient func(context.Context) (*http.Client, error)
}

// NewGitHubSource 构建发布源；repo 为 owner/name，client 为空时使用直连客户端。
func NewGitHubSource(repo string, client *http.Client) *GitHubSource {
	if client == nil {
		client = directClient()
	}
	return &GitHubSource{repo: strings.TrimSpace(repo), endpoint: defaultEndpoint, cdnEndpoint: "https://cdn.jsdelivr.net/gh", client: client}
}

// Latest 按 raw 直连、jsDelivr 直连、已配置代理访问 raw 的顺序读取版本。
// 每个来源独立限时并验证正文，成功即停止；调用方取消后不继续回退。
func (s *GitHubSource) Latest(ctx context.Context) (ports.ReleaseInfo, error) {
	if !repoPattern.MatchString(s.repo) {
		return ports.ReleaseInfo{}, fmt.Errorf("发布仓库 %q 不是 owner/name 形式", s.repo)
	}
	rawURL := s.endpoint + "/" + s.repo + "/" + defaultBranchRef + "/" + versionFilePath
	info, err := s.read(ctx, s.client, rawURL)
	if err == nil || ctx.Err() != nil {
		return info, err
	}
	if s.cdnEndpoint != "" {
		info, err = s.read(ctx, s.client, s.cdnEndpoint+"/"+s.repo+"@"+defaultBranchRef+"/"+versionFilePath)
		if err == nil || ctx.Err() != nil {
			return info, err
		}
	}
	if s.ProxyClient != nil {
		proxy, proxyErr := s.ProxyClient(ctx)
		if proxyErr != nil {
			return ports.ReleaseInfo{}, proxyErr
		}
		if proxy != nil {
			defer proxy.CloseIdleConnections()
			return s.read(ctx, proxy, rawURL)
		}
	}
	return ports.ReleaseInfo{}, err
}

// read 为每个来源设置独立超时，只返回通过大小和格式检查的版本记录。
func (s *GitHubSource) read(ctx context.Context, client *http.Client, endpoint string) (ports.ReleaseInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ports.ReleaseInfo{}, errors.New("检查更新的请求构建失败")
	}
	// 原始文件服务按分支路径返回正文，不需要 Contents API 的 base64 包装。
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "bytemuse")
	// 不跟随重定向：版本记录只来自固定仓库路径，避免被重定向到其他主机。
	bounded := *client
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
