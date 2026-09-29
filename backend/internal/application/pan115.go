package application

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

// 115 网盘服务端约束：二维码 5 分钟有效，内存中同时最多保留 32 个扫码会话，
// 访问令牌提前 60 秒视为过期，离线任务回查最多扫描 3 页。
const (
	pan115LoginTTL         = 5 * time.Minute
	pan115LoginLimit       = 32
	pan115TokenSkew        = 60 * time.Second
	pan115OfflineScanPages = 3
	// pan115RootDirectoryID 是 115 根目录的固定标识；保存目录为空时落到这里。
	pan115RootDirectoryID = "0"
	// pan115RootDirectoryName 是 115 对根目录的固定名称，用于目录选择器的面包屑。
	pan115RootDirectoryName = "根目录"
	pan115DefaultFileLimit  = 100
	pan115MaxFileLimit      = 1000
)

var (
	// ErrPan115NotLinked 表示尚未绑定 115 账号，或刷新令牌已被 115 吊销。
	ErrPan115NotLinked = errors.New("115 网盘尚未绑定账号")
	// ErrPan115LoginUnknown 表示扫码会话不存在、已消费或已被清理。
	ErrPan115LoginUnknown = errors.New("115 扫码会话不存在或已过期")
	// ErrPan115InvalidInput 表示调用方提供的参数不满足 115 接口要求。
	ErrPan115InvalidInput = errors.New("115 请求参数无效")
	// ErrPan115OfflineExists 表示同一磁力已在 115 离线列表中；对下载链路等同于提交成功。
	ErrPan115OfflineExists = errors.New("115 已存在该离线任务")
)

// pan115API 是 115 协议客户端的应用层视图。业务规则只依赖这组能力，
// 使扫码、刷新与离线受理可以在不访问 115 真实接口的情况下被验证。
type pan115API interface {
	BeginLogin(context.Context) (*pan115.Login, error)
	LoginStatus(context.Context, *pan115.Login) (pan115.LoginState, error)
	ExchangeToken(context.Context, *pan115.Login) (pan115.Tokens, error)
	RefreshToken(context.Context, string) (pan115.Tokens, error)
	Account(context.Context, string) (pan115.Account, error)
	OfflineQuota(context.Context, string) (pan115.OfflineQuota, error)
	List(context.Context, string, string, int, int) (pan115.FilePage, error)
	Info(context.Context, string, string) (pan115.FileInfo, error)
	DownloadURL(context.Context, string, string, string) (string, error)
	AddOffline(context.Context, string, string, string) (string, error)
	OfflineTasks(context.Context, string, int) (pan115.OfflinePage, error)
	RemoveOffline(context.Context, string, string) error
	Close()
}

// Pan115Service 统一 115 网盘业务：扫码登录、账号绑定、目录浏览、离线下载受理与下载器适配。
// HTTP、设置页与订阅下载链路复用同一份令牌刷新与目录规则，不各自实现。
type Pan115Service struct {
	client   pan115API
	accounts ports.Pan115AccountRepository
	secrets  *secretCipher
	settings func(context.Context) (map[string]string, error)

	mu     sync.Mutex
	logins map[string]*pan115Login
}

// pan115Login 保存一次扫码会话。设备码与 PKCE 校验值只存在于内存，
// 既不落库也不返回给调用方，因此二维码会话不会成为可重放的凭据。
type pan115Login struct {
	mu        sync.Mutex
	login     *pan115.Login
	expiresAt time.Time
	state     pan115.LoginState
	account   *domain.Pan115Account
}

// NewPan115Service 组装 115 服务；settings 用于读取离线下载的目标目录。
func NewPan115Service(accounts ports.Pan115AccountRepository, settings func(context.Context) (map[string]string, error), client pan115API, secret string) (*Pan115Service, error) {
	if accounts == nil {
		return nil, fmt.Errorf("115 account repository is required")
	}
	if settings == nil {
		return nil, fmt.Errorf("115 settings loader is required")
	}
	secrets, err := newSecretCipher(secret)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = pan115.New(nil)
	}
	return &Pan115Service{client: client, accounts: accounts, secrets: secrets, settings: settings, logins: map[string]*pan115Login{}}, nil
}

// Close 释放 115 的空闲连接；进程退出时调用。
func (s *Pan115Service) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

// StartLogin 申请设备码并返回可直接渲染的二维码。
func (s *Pan115Service) StartLogin(ctx context.Context) (domain.Pan115LoginSession, error) {
	login, err := s.client.BeginLogin(ctx)
	if err != nil {
		return domain.Pan115LoginSession{}, err
	}
	sessionID, err := pan115SessionID()
	if err != nil {
		return domain.Pan115LoginSession{}, err
	}
	expiresAt := time.Now().Add(pan115LoginTTL)
	s.mu.Lock()
	s.pruneLoginsLocked(time.Now())
	if len(s.logins) >= pan115LoginLimit {
		s.dropOldestLoginLocked()
	}
	s.logins[sessionID] = &pan115Login{login: login, expiresAt: expiresAt, state: pan115.LoginWaiting}
	s.mu.Unlock()
	return domain.Pan115LoginSession{
		SessionID: sessionID,
		QRCode:    "data:image/png;base64," + base64.StdEncoding.EncodeToString(login.QRCode),
		ExpiresAt: expiresAt,
	}, nil
}

// LoginStatus 查询一次扫码状态；已授权时立刻换取令牌并落库，之后重复查询返回同一账号快照。
func (s *Pan115Service) LoginStatus(ctx context.Context, sessionID string) (domain.Pan115LoginResult, error) {
	entry, err := s.login(sessionID)
	if err != nil {
		return domain.Pan115LoginResult{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.state == pan115.LoginAuthorized {
		return domain.Pan115LoginResult{Status: domain.Pan115LoginAuthorized, Account: entry.account}, nil
	}
	if time.Now().After(entry.expiresAt) {
		s.forgetLogin(sessionID)
		return domain.Pan115LoginResult{Status: domain.Pan115LoginExpired}, nil
	}
	state, err := s.client.LoginStatus(ctx, entry.login)
	if err != nil {
		return domain.Pan115LoginResult{}, err
	}
	switch state {
	case pan115.LoginAuthorized:
		tokens, err := s.client.ExchangeToken(ctx, entry.login)
		if err != nil {
			return domain.Pan115LoginResult{}, err
		}
		account, err := s.client.Account(ctx, tokens.AccessToken)
		if err != nil {
			return domain.Pan115LoginResult{}, err
		}
		if err := s.saveBinding(ctx, tokens, account); err != nil {
			return domain.Pan115LoginResult{}, err
		}
		view := pan115AccountView(account)
		entry.state = pan115.LoginAuthorized
		entry.account = &view
		return domain.Pan115LoginResult{Status: domain.Pan115LoginAuthorized, Account: &view}, nil
	case pan115.LoginExpired, pan115.LoginCanceled:
		s.forgetLogin(sessionID)
	}
	return domain.Pan115LoginResult{Status: pan115LoginState(state)}, nil
}

// CancelLogin 结束一次扫码会话；会话不存在时同样视为已取消。
func (s *Pan115Service) CancelLogin(sessionID string) {
	s.forgetLogin(sessionID)
}

// Linked 报告是否已绑定 115 账号；订阅下载装配据此决定是否注册 115 下载器。
func (s *Pan115Service) Linked(ctx context.Context) (bool, error) {
	_, found, err := s.accounts.Load(ctx)
	return found, err
}

// Unlink 解除 115 绑定并删除本地令牌；115 侧的授权记录不受影响。
func (s *Pan115Service) Unlink(ctx context.Context) error {
	return s.accounts.Clear(ctx)
}

// Account 读取当前绑定账号的资料、容量与云下载配额；未绑定时返回 ErrPan115NotLinked。
// 配额是账号面板的附加信息：115 未提供或临时不可用时仍返回账号快照，只把 Quota 置为 nil，
// 避免配额接口的偶发故障连带隐藏账号、容量与解绑入口。
func (s *Pan115Service) Account(ctx context.Context) (domain.Pan115Account, error) {
	var account pan115.Account
	var quota *domain.Pan115Quota
	err := s.withToken(ctx, func(token string) error {
		found, err := s.client.Account(ctx, token)
		if err != nil {
			return err
		}
		account = found
		value, err := s.client.OfflineQuota(ctx, token)
		if err != nil {
			return nil
		}
		view := pan115QuotaView(value)
		quota = &view
		return nil
	})
	if err != nil {
		return domain.Pan115Account{}, err
	}
	result := pan115AccountView(account)
	result.Quota = quota
	return result, nil
}

// Files 读取一个 115 目录的分页内容；directoryID 为空时读取根目录。
func (s *Pan115Service) Files(ctx context.Context, directoryID string, offset, limit int) (domain.Pan115FilePage, error) {
	directoryID = strings.TrimSpace(directoryID)
	if directoryID == "" {
		directoryID = pan115RootDirectoryID
	}
	if limit <= 0 || limit > pan115MaxFileLimit {
		limit = pan115DefaultFileLimit
	}
	if offset < 0 {
		offset = 0
	}
	var page pan115.FilePage
	err := s.withToken(ctx, func(token string) error {
		found, err := s.client.List(ctx, token, directoryID, offset, limit)
		if err != nil {
			return err
		}
		page = found
		return nil
	})
	if err != nil {
		return domain.Pan115FilePage{}, err
	}
	return pan115FilePageView(directoryID, page), nil
}

// DirectoryPath 返回目录在 115 中的完整路径（含根目录与目录自身），供目录选择器展示与面包屑导航。
// 115 的文件列表接口只回传条目，路径统一由目录信息接口解析，避免前端自行拼接出不一致的路径。
func (s *Pan115Service) DirectoryPath(ctx context.Context, directoryID string) ([]domain.Pan115Directory, error) {
	directoryID = strings.TrimSpace(directoryID)
	if directoryID == "" || directoryID == pan115RootDirectoryID {
		return []domain.Pan115Directory{{ID: pan115RootDirectoryID, Name: pan115RootDirectoryName}}, nil
	}
	var path []domain.Pan115Directory
	err := s.withToken(ctx, func(token string) error {
		info, err := s.client.Info(ctx, token, directoryID)
		if err != nil {
			return err
		}
		// 115 的目录信息只回传上级目录，末级要补上目录自身，面包屑才能定位到当前目录。
		path = append(pan115DirectoryView(info.Path), domain.Pan115Directory{ID: info.ID, Name: info.Name})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return path, nil
}

// PlayURL 把 115 文件标识解析为带时效的播放直链：先取文件信息得到提取码，再换取下载地址。
// userAgent 由播放端提供：115 会把直链绑定到换取直链时的 User-Agent，播放端必须使用同一个 UA。
func (s *Pan115Service) PlayURL(ctx context.Context, fileID, userAgent string) (string, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return "", fmt.Errorf("%w: 文件标识不能为空", ErrPan115InvalidInput)
	}
	var address string
	err := s.withToken(ctx, func(token string) error {
		info, err := s.client.Info(ctx, token, fileID)
		if err != nil {
			return err
		}
		if info.IsDirectory {
			return fmt.Errorf("%w: 该标识是目录，无法播放", ErrPan115InvalidInput)
		}
		if strings.TrimSpace(info.PickCode) == "" {
			return fmt.Errorf("%w: 未能获取文件的 115 提取码", ErrPan115InvalidInput)
		}
		found, err := s.client.DownloadURL(ctx, token, info.PickCode, userAgent)
		if err != nil {
			return err
		}
		address = found
		return nil
	})
	if err != nil {
		return "", err
	}
	return address, nil
}

// AddOffline 提交一个磁力或下载地址到 115 离线下载，返回 115 侧的信息哈希。
// directoryID 为空时使用设置里的保存目录，仍未配置则落到 115 根目录。
func (s *Pan115Service) AddOffline(ctx context.Context, uri, directoryID string) (string, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return "", fmt.Errorf("%w: 磁力链接或下载地址不能为空", ErrPan115InvalidInput)
	}
	directoryID, err := s.resolveDirectoryID(ctx, directoryID)
	if err != nil {
		return "", err
	}
	var hash string
	err = s.withToken(ctx, func(token string) error {
		value, err := s.client.AddOffline(ctx, token, uri, directoryID)
		if errors.Is(err, pan115.ErrOfflineExists) {
			return ErrPan115OfflineExists
		}
		if err != nil {
			return err
		}
		hash = value
		return nil
	})
	if err != nil {
		return "", err
	}
	return hash, nil
}

// OfflineTasks 读取一页 115 离线任务；页码从 1 开始。
func (s *Pan115Service) OfflineTasks(ctx context.Context, page int) (domain.Pan115OfflinePage, error) {
	if page < 1 {
		page = 1
	}
	var result pan115.OfflinePage
	err := s.withToken(ctx, func(token string) error {
		found, err := s.client.OfflineTasks(ctx, token, page)
		if err != nil {
			return err
		}
		result = found
		return nil
	})
	if err != nil {
		return domain.Pan115OfflinePage{}, err
	}
	return pan115OfflinePageView(page, result), nil
}

// RemoveOffline 只删除 115 离线任务记录，不删除已下载的源文件。
func (s *Pan115Service) RemoveOffline(ctx context.Context, hash string) error {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return fmt.Errorf("%w: 信息哈希不能为空", ErrPan115InvalidInput)
	}
	return s.withToken(ctx, func(token string) error {
		return s.client.RemoveOffline(ctx, token, hash)
	})
}

// Downloader 返回订阅下载链路使用的 115 下载器；savePath 为空时按设置解析目标目录。
func (s *Pan115Service) Downloader(savePath string) MagnetDownloader {
	return pan115Downloader{service: s, savePath: strings.TrimSpace(savePath)}
}

// withToken 用当前访问令牌执行一次 115 调用；令牌被拒时强制刷新一次后重试，
// 避免把正常的令牌过期当成业务失败。
func (s *Pan115Service) withToken(ctx context.Context, action func(string) error) error {
	token, err := s.accessToken(ctx, false)
	if err != nil {
		return err
	}
	err = action(token)
	if err == nil || !pan115.Unauthorized(err) {
		return err
	}
	token, err = s.accessToken(ctx, true)
	if err != nil {
		return err
	}
	return action(token)
}

// accessToken 返回可用的访问令牌；force 为真时忽略本地有效期直接刷新。
func (s *Pan115Service) accessToken(ctx context.Context, force bool) (string, error) {
	stored, found, err := s.accounts.Load(ctx)
	if err != nil {
		return "", fmt.Errorf("读取 115 绑定信息失败: %w", err)
	}
	if !found {
		return "", ErrPan115NotLinked
	}
	refreshToken, err := s.secrets.decrypt(stored.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("解密 115 刷新令牌失败: %w", err)
	}
	if strings.TrimSpace(refreshToken) == "" {
		return "", ErrPan115NotLinked
	}
	if !force {
		accessToken, err := s.secrets.decrypt(stored.AccessToken)
		if err != nil {
			return "", fmt.Errorf("解密 115 访问令牌失败: %w", err)
		}
		if accessToken != "" && time.Now().Add(pan115TokenSkew).Before(stored.AccessExpiresAt) {
			return accessToken, nil
		}
	}
	tokens, err := s.client.RefreshToken(ctx, refreshToken)
	if err != nil {
		if !pan115.Unauthorized(err) {
			return "", err
		}
		// 刷新令牌已被 115 吊销：本地绑定不再可用，清除后让设置页回到未登录状态。
		if clearErr := s.accounts.Clear(ctx); clearErr != nil {
			return "", fmt.Errorf("清除失效的 115 绑定失败: %w", clearErr)
		}
		return "", ErrPan115NotLinked
	}
	if err := s.persistTokens(ctx, stored, tokens); err != nil {
		return "", err
	}
	return tokens.AccessToken, nil
}

// saveBinding 落库一次完整的账号绑定，令牌以密文保存。
func (s *Pan115Service) saveBinding(ctx context.Context, tokens pan115.Tokens, account pan115.Account) error {
	return s.writeTokens(ctx, ports.Pan115Account{UserID: account.ID, UserName: account.Name}, tokens)
}

// persistTokens 只更新令牌与有效期，保留账号标识，避免刷新令牌时丢失账号展示信息。
func (s *Pan115Service) persistTokens(ctx context.Context, stored ports.Pan115Account, tokens pan115.Tokens) error {
	return s.writeTokens(ctx, ports.Pan115Account{UserID: stored.UserID, UserName: stored.UserName}, tokens)
}

func (s *Pan115Service) writeTokens(ctx context.Context, account ports.Pan115Account, tokens pan115.Tokens) error {
	access, err := s.secrets.encrypt(tokens.AccessToken)
	if err != nil {
		return err
	}
	refresh, err := s.secrets.encrypt(tokens.RefreshToken)
	if err != nil {
		return err
	}
	account.AccessToken = access
	account.RefreshToken = refresh
	account.AccessExpiresAt = tokens.ExpiresAt
	account.UpdatedAt = time.Now().UTC()
	return s.accounts.Save(ctx, account)
}

// resolveDirectoryID 解析离线任务的目标目录：显式传入优先，其次使用设置里的保存目录，
// 都为空时落到 115 根目录。
func (s *Pan115Service) resolveDirectoryID(ctx context.Context, explicit string) (string, error) {
	if value := strings.TrimSpace(explicit); value != "" {
		return value, nil
	}
	values, err := s.settings(ctx)
	if err != nil {
		return "", fmt.Errorf("读取 115 保存目录失败: %w", err)
	}
	if value := strings.TrimSpace(values["PAN115_SAVE_PATH"]); value != "" {
		return value, nil
	}
	return pan115RootDirectoryID, nil
}

// login 取出一个扫码会话；不改变会话状态。
func (s *Pan115Service) login(sessionID string) (*pan115Login, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.logins[sessionID]
	if !ok {
		return nil, ErrPan115LoginUnknown
	}
	return entry, nil
}

func (s *Pan115Service) forgetLogin(sessionID string) {
	s.mu.Lock()
	delete(s.logins, sessionID)
	s.mu.Unlock()
}

// pruneLoginsLocked 清理已过期的扫码会话；expiresAt 创建后不再变化，可无锁读取。
func (s *Pan115Service) pruneLoginsLocked(now time.Time) {
	for id, entry := range s.logins {
		if now.After(entry.expiresAt) {
			delete(s.logins, id)
		}
	}
}

// dropOldestLoginLocked 在会话数达到上限时淘汰最早创建的一个，避免内存无限增长。
func (s *Pan115Service) dropOldestLoginLocked() {
	oldestID := ""
	var oldest time.Time
	for id, entry := range s.logins {
		if oldestID == "" || entry.expiresAt.Before(oldest) {
			oldestID, oldest = id, entry.expiresAt
		}
	}
	if oldestID != "" {
		delete(s.logins, oldestID)
	}
}

// pan115Downloader 把 BT 磁力提交到 115 离线下载，并按信息哈希回查受理结果。
type pan115Downloader struct {
	service  *Pan115Service
	savePath string
}

// HasHash 在最近的离线任务页中查找信息哈希。115 没有按哈希精确查询的接口，
// 因此回查范围限定在最近若干页，找不到即视为尚未受理。
func (d pan115Downloader) HasHash(ctx context.Context, hash string) (bool, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if hash == "" {
		return false, nil
	}
	for page := 1; page <= pan115OfflineScanPages; page++ {
		result, err := d.service.OfflineTasks(ctx, page)
		if err != nil {
			return false, err
		}
		for _, task := range result.Tasks {
			if strings.ToLower(strings.TrimSpace(task.Hash)) == hash {
				return true, nil
			}
		}
		if page >= result.PageCount {
			break
		}
	}
	return false, nil
}

// Submit 提交磁力到 115 离线下载；115 报告同一磁力已存在时视为提交成功。
func (d pan115Downloader) Submit(ctx context.Context, uri string) error {
	_, err := d.service.AddOffline(ctx, uri, d.savePath)
	if errors.Is(err, ErrPan115OfflineExists) {
		return nil
	}
	return err
}

// pan115SessionID 生成不可预测的扫码会话标识。
func pan115SessionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成 115 扫码会话标识失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// pan115LoginState 显式映射协议状态，避免两端常量定义各自漂移。
func pan115LoginState(state pan115.LoginState) domain.Pan115LoginState {
	switch state {
	case pan115.LoginScanned:
		return domain.Pan115LoginScanned
	case pan115.LoginAuthorized:
		return domain.Pan115LoginAuthorized
	case pan115.LoginExpired:
		return domain.Pan115LoginExpired
	case pan115.LoginCanceled:
		return domain.Pan115LoginCanceled
	default:
		return domain.Pan115LoginWaiting
	}
}

func pan115AccountView(account pan115.Account) domain.Pan115Account {
	return domain.Pan115Account{
		ID:     account.ID,
		Name:   account.Name,
		Avatar: account.Avatar,
		Level:  account.Level,
		Space: domain.Pan115Space{
			Total:     pan115AmountView(account.Space.Total),
			Used:      pan115AmountView(account.Space.Used),
			Remaining: pan115AmountView(account.Space.Remaining),
		},
	}
}

func pan115AmountView(amount pan115.SpaceAmount) domain.Pan115SpaceAmount {
	return domain.Pan115SpaceAmount{Size: amount.Size, Formatted: amount.Formatted}
}

func pan115QuotaView(quota pan115.OfflineQuota) domain.Pan115Quota {
	return domain.Pan115Quota{Total: quota.Total, Used: quota.Used, Remaining: quota.Remaining}
}

func pan115FilePageView(directoryID string, page pan115.FilePage) domain.Pan115FilePage {
	files := make([]domain.Pan115File, len(page.Files))
	for index, file := range page.Files {
		files[index] = domain.Pan115File{
			ID:          file.ID,
			ParentID:    file.ParentID,
			Name:        file.Name,
			IsDirectory: file.IsDirectory,
			Size:        file.Size,
			PickCode:    file.PickCode,
		}
	}
	return domain.Pan115FilePage{
		DirectoryID: directoryID,
		Path:        pan115DirectoryView(page.Path),
		Files:       files,
		Total:       page.Total,
		HasMore:     page.HasMore,
	}
}

// pan115DirectoryView 把协议层的目录路径转换成对外结构。
func pan115DirectoryView(path []pan115.Directory) []domain.Pan115Directory {
	directories := make([]domain.Pan115Directory, len(path))
	for index, item := range path {
		directories[index] = domain.Pan115Directory{ID: item.ID, Name: item.Name}
	}
	return directories
}

func pan115OfflinePageView(page int, result pan115.OfflinePage) domain.Pan115OfflinePage {
	tasks := make([]domain.Pan115OfflineTask, len(result.Tasks))
	for index, task := range result.Tasks {
		tasks[index] = domain.Pan115OfflineTask{
			Hash:        task.Hash,
			Status:      task.Status,
			Progress:    task.Progress,
			FileID:      task.FileID,
			DirectoryID: task.DirectoryID,
		}
	}
	return domain.Pan115OfflinePage{Page: page, PageCount: result.PageCount, Tasks: tasks}
}
