package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"bytemuse/backend/internal/ports"
)

var packageVersion = regexp.MustCompile(`^[0-9]+[.][0-9]+[.][0-9]+$`)

const maxPackageBytes int64 = 256 << 20

// progressReader 按实际读取的字节数报告进度；未知总量以非正数传递，不推算百分比。
type progressReader struct {
	reader      io.Reader
	done, total int64
	report      func(int64, int64)
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.done += int64(n)
	if n > 0 {
		r.report(r.done, r.total)
	}
	return n, err
}

// progressCallback 允许不关注进度的内部校验调用省略回调。
func progressCallback(reports []func(int64, int64)) func(int64, int64) {
	if len(reports) > 0 && reports[0] != nil {
		return reports[0]
	}
	return func(int64, int64) {}
}

// PackageInstaller 将升级包保存在 /data 的专用目录，不覆盖运行文件或用户配置。
type PackageInstaller struct {
	Root, Repo string
	// Client 可注入直连测试客户端；为空时显式直连。
	Client *http.Client
	// ProxyClient 在直连下载失败后返回已配置的代理；nil 表示跳过代理回退。
	ProxyClient func(context.Context) (*http.Client, error)
}

// Status 读取启动器写入的升级状态，异常状态文件按失败报告。
func (p *PackageInstaller) Status() ports.UpgradeStatus {
	state := ports.UpgradeStatus{Enabled: true, Phase: "idle"}
	body, err := os.ReadFile(filepath.Join(p.Root, "status.json"))
	if err == nil {
		err = json.Unmarshal(body, &state)
	}
	if err != nil && !os.IsNotExist(err) {
		state.Phase = "failed"
		state.Error = "无法读取升级状态"
	}
	// 常驻的旧版启动器不写步骤数，依据其终态补齐协议字段。
	if state.Phase == "restarting" {
		state.CompletedSteps = 3
		state.ProgressIndeterminate = true
	}
	if state.Phase == "success" {
		state.CompletedSteps = 4
		state.ProgressIndeterminate = false
	}
	if state.ProgressPercent < state.CompletedSteps*25 {
		state.ProgressPercent = state.CompletedSteps * 25
	}
	return state
}

// Stage 从固定仓库的版本 Release 下载与本机架构匹配的包，校验 SHA256 后原子提交重启请求。
func (p *PackageInstaller) Stage(ctx context.Context, version string, report func(ports.UpgradeStatus)) (resultErr error) {
	if runtime.GOOS != "linux" || !packageVersion.MatchString(version) || !repoPattern.MatchString(p.Repo) {
		return errors.New("当前平台或升级版本无效")
	}
	if err := os.MkdirAll(p.Root, 0700); err != nil {
		return errors.New("升级目录不可写")
	}
	state := ports.UpgradeStatus{Enabled: true, Phase: "downloading", Target: version}
	advance := func(phase string, completed int) error {
		state.Phase, state.CompletedSteps = phase, completed
		state.ProgressPercent, state.ProgressIndeterminate = completed*25, false
		if err := writeRecord(p.Root, "status.json", state); err != nil {
			return errors.New("无法保存升级状态")
		}
		if report != nil {
			report(state)
		}
		return nil
	}
	// 每个整数百分比最多报告一次；过程进度只更新内存，阶段边界才持久化，避免频繁写盘。
	progress := func(done, total int64) {
		percent := state.CompletedSteps * 25
		if total > 0 {
			percent += int(min(int64(24), done*25/total))
		}
		unknown := total <= 0
		if percent != state.ProgressPercent || unknown != state.ProgressIndeterminate {
			state.ProgressPercent, state.ProgressIndeterminate = percent, unknown
			if report != nil {
				report(state)
			}
		}
	}
	if err := advance("downloading", 0); err != nil {
		return errors.New("无法保存升级状态")
	}
	defer func() {
		if resultErr != nil {
			state.Phase, state.Error = "failed", resultErr.Error()
			state.ProgressIndeterminate = false
			_ = writeRecord(p.Root, "status.json", state)
		}
	}()
	temp, err := os.MkdirTemp(p.Root, "staging-")
	if err != nil {
		return errors.New("无法创建升级暂存目录")
	}
	defer os.RemoveAll(temp)
	name := "bytemuse-linux-" + runtime.GOARCH + ".tar.gz"
	base := "https://github.com/" + p.Repo + "/releases/download/v" + version + "/" + name
	archive := filepath.Join(temp, "package.tgz")
	if err := p.download(ctx, base, archive, progress); err != nil {
		return err
	}
	candidate := filepath.Join(temp, "release")
	if err := advance("extracting", 1); err != nil {
		return err
	}
	if err := extractPackage(archive, candidate, version, progress); err != nil {
		return err
	}
	binary, err := elf.Open(filepath.Join(candidate, "bytemuse"))
	if err != nil {
		return errors.New("升级程序不是有效的 Linux 可执行文件")
	}
	machine := binary.Machine
	_ = binary.Close()
	if (runtime.GOARCH == "amd64" && machine != elf.EM_X86_64) || (runtime.GOARCH == "arm64" && machine != elf.EM_AARCH64) {
		return errors.New("升级程序架构不匹配")
	}
	releases := filepath.Join(p.Root, "releases")
	if err := advance("installing", 2); err != nil {
		return err
	}
	if err := os.MkdirAll(releases, 0700); err != nil {
		return errors.New("无法创建版本目录")
	}
	progress(1, 3)
	// 目录使用随机名称，重试不会覆盖当前或历史运行版本。
	dest := filepath.Join(releases, filepath.Base(temp))
	if err := os.Rename(candidate, dest); err != nil {
		return errors.New("无法保存升级包")
	}
	progress(2, 3)
	request := Activation{Directory: filepath.Base(dest), Version: version}
	if err := writeRecord(p.Root, "pending.json", request); err != nil {
		_ = os.RemoveAll(dest)
		return errors.New("无法提交服务重启请求")
	}
	return nil
}

// download 先直连 Release，失败后仅通过已配置代理重新下载摘要与完整包。
// 不使用 jsDelivr（不支持 Release 附件），每次重新校验 SHA256，不复用失败的部分文件。
func (p *PackageInstaller) download(ctx context.Context, base, archive string, reports ...func(int64, int64)) error {
	report := progressCallback(reports)
	client := p.Client
	if client == nil {
		client = directClient()
		defer client.CloseIdleConnections()
	}
	attempt := func(client *http.Client) error {
		report(0, 0)
		attemptCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		defer cancel()
		checksumCtx, cancelChecksum := context.WithTimeout(attemptCtx, requestTimeout)
		checksum, err := downloadBytes(checksumCtx, client, base+".sha256", 1024)
		cancelChecksum()
		if err != nil {
			return err
		}
		fields := strings.Fields(string(checksum))
		if len(fields) == 0 || len(fields[0]) != 64 {
			return errors.New("升级包摘要无效")
		}
		return downloadFile(attemptCtx, client, base, archive, fields[0], report)
	}
	err := attempt(client)
	if err == nil || ctx.Err() != nil || p.ProxyClient == nil {
		return err
	}
	proxy, proxyErr := p.ProxyClient(ctx)
	if proxyErr != nil {
		return proxyErr
	}
	if proxy == nil {
		return err
	}
	defer proxy.CloseIdleConnections()
	if removeErr := os.Remove(archive); removeErr != nil && !os.IsNotExist(removeErr) {
		return errors.New("无法清理未完成的升级包")
	}
	return attempt(proxy)
}

// packageClient 只允许 GitHub 的 HTTPS 制品重定向，不携带本地凭据。
func packageClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	bounded := *client
	// 两轮完整下载均包含在后台任务的十分钟期限内，给校验和安装留出时间。
	bounded.Timeout = 4 * time.Minute
	bounded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		host := req.URL.Hostname()
		if len(via) >= 5 || req.URL.Scheme != "https" || (host != "github.com" && !strings.HasSuffix(host, ".githubusercontent.com")) {
			return errors.New("升级下载地址无效")
		}
		return nil
	}
	return &bounded
}

func packageResponse(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.New("升级下载请求无效")
	}
	resp, err := packageClient(client).Do(req)
	if err != nil {
		return nil, errors.New("下载升级包失败，请检查网络后重试")
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, errors.New("升级包尚未发布或暂时无法下载，请稍后重试")
	}
	return resp, nil
}

func downloadBytes(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	resp, err := packageResponse(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, errors.New("升级包摘要读取失败")
	}
	return body, nil
}

func downloadFile(ctx context.Context, client *http.Client, url, destination, expected string, reports ...func(int64, int64)) error {
	resp, err := packageResponse(ctx, client, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("升级包写入失败")
	}
	digest := sha256.New()
	report := progressCallback(reports)
	report(0, resp.ContentLength)
	counter := &progressReader{reader: io.LimitReader(resp.Body, maxPackageBytes+1), total: resp.ContentLength, report: report}
	size, copyErr := io.Copy(io.MultiWriter(file, digest), counter)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || size > maxPackageBytes {
		return errors.New("升级包下载不完整或超出大小限制")
	}
	if hex.EncodeToString(digest.Sum(nil)) != strings.ToLower(expected) {
		return errors.New("升级包校验失败")
	}
	return nil
}

// extractPackage 拒绝所有链接、重复路径和目录穿越，限制解包体积并验证协议、版本及前后端完整性。
func extractPackage(archive, destination, version string, reports ...func(int64, int64)) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	report := progressCallback(reports)
	report(0, info.Size())
	gz, err := gzip.NewReader(&progressReader{reader: file, total: info.Size(), report: report})
	if err != nil {
		return errors.New("升级包格式无效")
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	count := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("升级包内容损坏")
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name == "" || name == "." {
			if header.Typeflag == tar.TypeDir {
				continue
			}
			return errors.New("升级包路径无效")
		}
		clean := path.Clean(name)
		if strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") || (clean != "bytemuse" && clean != "version.json" && clean != "web" && !strings.HasPrefix(clean, "web/")) {
			return errors.New("升级包包含非法路径")
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		count++
		total += header.Size
		if header.Size < 0 || total > 512<<20 || count > 10000 {
			return errors.New("升级包超出解包限制")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if clean == "bytemuse" {
				mode = 0700
			}
			output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return errors.New("升级包文件重复或不可写")
			}
			_, copyErr := io.Copy(output, reader)
			closeErr := output.Close()
			if copyErr != nil || closeErr != nil {
				return errors.New("升级包解压失败")
			}
		default:
			return errors.New("升级包不允许链接或特殊文件")
		}
	}
	var manifest struct {
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	body, err := os.ReadFile(filepath.Join(destination, "version.json"))
	if err != nil || json.Unmarshal(body, &manifest) != nil || manifest.Version != version || manifest.Protocol != 1 {
		return errors.New("升级包版本或启动协议不兼容，请更新镜像")
	}
	for _, name := range []string{"bytemuse", "web/index.html"} {
		info, err := os.Stat(filepath.Join(destination, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return errors.New("升级包缺少前后端运行文件")
		}
	}
	return nil
}
