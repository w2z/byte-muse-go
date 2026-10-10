package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

// pan115ClientStub 记录调用并返回预设结果，使扫码、刷新与离线受理可在无网络环境下验证。
type pan115ClientStub struct {
	login       *pan115.Login
	loginState  pan115.LoginState
	cookieLogin *pan115.CookieLogin
	cookieState pan115.LoginState
	cookieValue string
	cookieErr   error
	tokens      pan115.Tokens
	account     pan115.Account
	quota       pan115.OfflineQuota
	page        pan115.FilePage
	offline     pan115.OfflinePage
	addHash     string
	addErr      error
	quotaErr    error
	refreshErr  error
	info        pan115.FileInfo
	infoErr     error
	download    string
	downloadErr error

	exchangeCalls int
	cookieCalls   int
	cookieClients []string
	refreshCalls  int
	accountCalls  int
	quotaCalls    int
	listDirectory string
	listOffset    int
	listLimit     int
	addURI        string
	addDirectory  string
	offlinePages  []int
	removed       []string
	infoFileID    string
	downloadPick  string
	downloadUA    string
	downloadCalls int
}

func (s *pan115ClientStub) BeginLogin(context.Context) (*pan115.Login, error) {
	if s.login == nil {
		return &pan115.Login{}, nil
	}
	return s.login, nil
}

func (s *pan115ClientStub) LoginStatus(context.Context, *pan115.Login) (pan115.LoginState, error) {
	return s.loginState, nil
}

func (s *pan115ClientStub) BeginCookieLogin(_ context.Context, clientType string) (*pan115.CookieLogin, error) {
	s.cookieClients = append(s.cookieClients, clientType)
	if s.cookieLogin == nil {
		return &pan115.CookieLogin{QRCode: []byte("png")}, nil
	}
	return s.cookieLogin, nil
}

func (s *pan115ClientStub) CookieLoginStatus(context.Context, *pan115.CookieLogin) (pan115.LoginState, error) {
	return s.cookieState, nil
}

func (s *pan115ClientStub) ExchangeCookie(context.Context, *pan115.CookieLogin) (string, error) {
	s.cookieCalls++
	if s.cookieErr != nil {
		return "", s.cookieErr
	}
	return s.cookieValue, nil
}

func (s *pan115ClientStub) ExchangeToken(context.Context, *pan115.Login) (pan115.Tokens, error) {
	s.exchangeCalls++
	return s.tokens, nil
}

func (s *pan115ClientStub) RefreshToken(context.Context, string) (pan115.Tokens, error) {
	s.refreshCalls++
	if s.refreshErr != nil {
		return pan115.Tokens{}, s.refreshErr
	}
	return s.tokens, nil
}

func (s *pan115ClientStub) Account(context.Context, string) (pan115.Account, error) {
	s.accountCalls++
	return s.account, nil
}

func (s *pan115ClientStub) OfflineQuota(context.Context, string) (pan115.OfflineQuota, error) {
	s.quotaCalls++
	if s.quotaErr != nil {
		return pan115.OfflineQuota{}, s.quotaErr
	}
	return s.quota, nil
}

func (s *pan115ClientStub) List(_ context.Context, _ string, directoryID string, offset, limit int) (pan115.FilePage, error) {
	s.listDirectory, s.listOffset, s.listLimit = directoryID, offset, limit
	return s.page, nil
}

func (s *pan115ClientStub) AddOffline(_ context.Context, _ string, uri, directoryID string) (string, error) {
	s.addURI, s.addDirectory = uri, directoryID
	if s.addErr != nil {
		return "", s.addErr
	}
	return s.addHash, nil
}

func (s *pan115ClientStub) OfflineTasks(_ context.Context, _ string, page int) (pan115.OfflinePage, error) {
	s.offlinePages = append(s.offlinePages, page)
	return s.offline, nil
}

func (s *pan115ClientStub) RemoveOffline(_ context.Context, _ string, hash string) error {
	s.removed = append(s.removed, hash)
	return nil
}

func (s *pan115ClientStub) Info(_ context.Context, _ string, fileID string) (pan115.FileInfo, error) {
	s.infoFileID = fileID
	if s.infoErr != nil {
		return pan115.FileInfo{}, s.infoErr
	}
	return s.info, nil
}

func (s *pan115ClientStub) DownloadURL(_ context.Context, _ string, pickCode, userAgent string) (string, error) {
	s.downloadCalls++
	s.downloadPick, s.downloadUA = pickCode, userAgent
	if s.downloadErr != nil {
		return "", s.downloadErr
	}
	return s.download, nil
}

func (s *pan115ClientStub) Close() {}

// TestPan115PlayUsesPickCode 验证有效令牌下只换取一次直链，且不查询文件信息。
func TestPan115PlayUsesPickCode(t *testing.T) {
	secret := strings.Repeat("x", 32)
	client := &pan115ClientStub{download: "https://example.com/video", infoErr: errors.New("不得查询文件信息")}
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), client, pan115TestSettings(""), secret)
	defer service.Close()
	address, err := service.PlayURL(context.Background(), " pc-example ", "Emby/4.8")
	if err != nil || address != client.download {
		t.Fatalf("播放地址 = %q err=%v", address, err)
	}
	if client.infoFileID != "" || client.downloadCalls != 1 || client.refreshCalls != 0 || client.downloadPick != "pc-example" || client.downloadUA != "Emby/4.8" {
		t.Fatalf("播放请求未直接使用 pick_code: %+v", client)
	}
	if _, err := service.PlayURL(context.Background(), " ", ""); !errors.Is(err, ErrPan115InvalidInput) || client.downloadCalls != 1 {
		t.Fatalf("空 pick_code 应在请求网盘前被拒绝: %v", err)
	}
	client.downloadErr = pan115.ErrDownloadUnavailable
	if _, err := service.PlayURL(context.Background(), "pc-missing", ""); !errors.Is(err, pan115.ErrDownloadUnavailable) || client.infoFileID != "" {
		t.Fatalf("下载地址错误应原样返回且不回退文件查询: %v", err)
	}
}

// pan115MemoryAccounts 只在测试中保存绑定行，用于核验密文落库与清除语义。
type pan115MemoryAccounts struct {
	account ports.Pan115Account
	found   bool
}

func (r *pan115MemoryAccounts) Load(context.Context) (ports.Pan115Account, bool, error) {
	return r.account, r.found, nil
}

func (r *pan115MemoryAccounts) Save(_ context.Context, account ports.Pan115Account) error {
	r.account, r.found = account, true
	return nil
}

func (r *pan115MemoryAccounts) Clear(context.Context) error {
	r.account, r.found = ports.Pan115Account{}, false
	return nil
}

func pan115TestSettings(savePath string) func(context.Context) (map[string]string, error) {
	return func(context.Context) (map[string]string, error) {
		return map[string]string{"PAN115_SAVE_PATH": savePath}, nil
	}
}

// pan115BoundAccounts 返回一个已绑定且访问令牌仍有效的仓储，供不关注登录流程的用例复用。
func pan115BoundAccounts(t *testing.T, secret string) *pan115MemoryAccounts {
	t.Helper()
	secrets, err := newSecretCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	access, err := secrets.encrypt("access-1")
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := secrets.encrypt("refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	return &pan115MemoryAccounts{
		found: true,
		account: ports.Pan115Account{
			UserID:          "100",
			UserName:        "张三",
			AccessToken:     access,
			RefreshToken:    refresh,
			AccessExpiresAt: time.Now().Add(time.Hour),
		},
	}
}

func newPan115TestService(t *testing.T, repo ports.Pan115AccountRepository, client pan115API, settings func(context.Context) (map[string]string, error), secret string) *Pan115Service {
	t.Helper()
	service, err := NewPan115Service(repo, settings, client, secret)
	if err != nil {
		t.Fatalf("组装 115 服务失败: %v", err)
	}
	return service
}

// TestPan115ScanLoginStoresEncryptedTokens 验证扫码授权后令牌以密文落库，
// 且重复轮询返回同一账号快照、不再重复换取令牌。
func TestPan115ScanLoginStoresEncryptedTokens(t *testing.T) {
	secret := strings.Repeat("x", 32)
	secrets, err := newSecretCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	repo := &pan115MemoryAccounts{}
	client := &pan115ClientStub{
		login:      &pan115.Login{QRCode: []byte("png")},
		loginState: pan115.LoginScanned,
		tokens:     pan115.Tokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresAt: time.Now().Add(time.Hour)},
		account:    pan115.Account{ID: "100", Name: "张三"},
	}
	service := newPan115TestService(t, repo, client, pan115TestSettings(""), secret)

	session, err := service.StartLogin(context.Background())
	if err != nil {
		t.Fatalf("开始扫码登录失败: %v", err)
	}
	if session.SessionID == "" || !strings.HasPrefix(session.QRCode, "data:image/png;base64,") {
		t.Fatalf("扫码会话 = %+v", session)
	}
	pending, err := service.LoginStatus(context.Background(), session.SessionID)
	if err != nil || pending.Status != "scanned" {
		t.Fatalf("已扫码状态 = %+v err=%v", pending, err)
	}

	client.loginState = pan115.LoginAuthorized
	authorized, err := service.LoginStatus(context.Background(), session.SessionID)
	if err != nil {
		t.Fatalf("授权状态查询失败: %v", err)
	}
	if authorized.Status != "authorized" || authorized.Account == nil || authorized.Account.Name != "张三" {
		t.Fatalf("授权结果 = %+v", authorized)
	}
	if !repo.found || repo.account.UserID != "100" {
		t.Fatalf("绑定落库结果 = %+v", repo.account)
	}
	if repo.account.RefreshToken == "refresh-1" || repo.account.AccessToken == "access-1" {
		t.Fatal("令牌必须以密文落库")
	}
	if plain, err := secrets.decrypt(repo.account.RefreshToken); err != nil || plain != "refresh-1" {
		t.Fatalf("刷新令牌解密结果 = %q err=%v", plain, err)
	}

	exchanges := client.exchangeCalls
	again, err := service.LoginStatus(context.Background(), session.SessionID)
	if err != nil || again.Status != "authorized" || client.exchangeCalls != exchanges {
		t.Fatalf("重复查询 = %+v err=%v exchange=%d", again, err, client.exchangeCalls)
	}
}

// TestPan115LoginStatusRejectsUnknownSession 验证不存在的会话不会被当作等待中。
func TestPan115LoginStatusRejectsUnknownSession(t *testing.T) {
	service := newPan115TestService(t, &pan115MemoryAccounts{}, &pan115ClientStub{}, pan115TestSettings(""), strings.Repeat("x", 32))
	if _, err := service.LoginStatus(context.Background(), "missing"); !errors.Is(err, ErrPan115LoginUnknown) {
		t.Fatalf("未知会话错误 = %v", err)
	}
}

// TestPan115ExpiredAccessTokenRefreshesAndPersists 验证访问令牌过期时用刷新令牌换新并落库。
func TestPan115ExpiredAccessTokenRefreshesAndPersists(t *testing.T) {
	secret := strings.Repeat("x", 32)
	secrets, err := newSecretCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	staleAccess, err := secrets.encrypt("stale-access")
	if err != nil {
		t.Fatal(err)
	}
	storedRefresh, err := secrets.encrypt("refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	repo := &pan115MemoryAccounts{
		found: true,
		account: ports.Pan115Account{
			UserID:          "100",
			UserName:        "张三",
			AccessToken:     staleAccess,
			RefreshToken:    storedRefresh,
			AccessExpiresAt: time.Now().Add(-time.Minute),
		},
	}
	client := &pan115ClientStub{
		tokens:  pan115.Tokens{AccessToken: "fresh-access", RefreshToken: "refresh-2", ExpiresAt: time.Now().Add(time.Hour)},
		account: pan115.Account{ID: "100", Name: "张三"},
	}
	service := newPan115TestService(t, repo, client, pan115TestSettings(""), secret)

	if _, err := service.Account(context.Background()); err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	if client.refreshCalls != 1 {
		t.Fatalf("刷新次数 = %d，期望 1", client.refreshCalls)
	}
	if plain, err := secrets.decrypt(repo.account.AccessToken); err != nil || plain != "fresh-access" {
		t.Fatalf("刷新后的访问令牌 = %q err=%v", plain, err)
	}
	if repo.account.UserID != "100" || repo.account.UserName != "张三" {
		t.Fatalf("刷新不应丢失账号标识: %+v", repo.account)
	}
}

// TestPan115AccountIncludesOfflineQuota 验证账号快照携带云下载配额，
// 且配额接口失败时只把 Quota 置空，账号与容量仍照常返回，不连带隐藏解绑入口。
func TestPan115AccountIncludesOfflineQuota(t *testing.T) {
	secret := strings.Repeat("x", 32)
	client := &pan115ClientStub{
		account: pan115.Account{ID: "100", Name: "张三"},
		quota:   pan115.OfflineQuota{Total: 1500, Used: 3, Remaining: 1497},
	}
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), client, pan115TestSettings(""), secret)

	account, err := service.Account(context.Background())
	if err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	if account.Quota == nil || account.Quota.Total != 1500 || account.Quota.Used != 3 || account.Quota.Remaining != 1497 {
		t.Fatalf("云下载配额 = %+v，期望 1500/3/1497", account.Quota)
	}

	client.quotaErr = &pan115.APIError{Code: 990002, Message: "参数错误"}
	account, err = service.Account(context.Background())
	if err != nil {
		t.Fatalf("配额失败不应中断账号读取: %v", err)
	}
	if account.Quota != nil {
		t.Fatalf("配额失败时 Quota = %+v，期望 nil", account.Quota)
	}
	if account.Name != "张三" || account.ID != "100" {
		t.Fatalf("配额失败时账号快照 = %+v，期望保留用户 ID 与昵称", account)
	}
}

// TestPan115RevokedRefreshTokenClearsBinding 验证刷新令牌被吊销时清除本地绑定，
// 让设置页回到未登录状态，而不是持续报同一个错误。
func TestPan115RevokedRefreshTokenClearsBinding(t *testing.T) {
	secret := strings.Repeat("x", 32)
	secrets, err := newSecretCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := secrets.encrypt("refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	repo := &pan115MemoryAccounts{found: true, account: ports.Pan115Account{RefreshToken: refresh}}
	client := &pan115ClientStub{refreshErr: &pan115.APIError{Code: 40140123, Message: "刷新令牌已过期"}}
	service := newPan115TestService(t, repo, client, pan115TestSettings(""), secret)

	if _, err := service.Account(context.Background()); !errors.Is(err, ErrPan115NotLinked) {
		t.Fatalf("吊销后的错误 = %v，期望 ErrPan115NotLinked", err)
	}
	if repo.found {
		t.Fatal("刷新令牌被吊销后必须清除本地绑定")
	}
}

// TestPan115DownloaderTreatsDuplicateAsSubmitted 验证下载链路把「已存在该离线任务」视为提交成功，
// 而接口层仍然把重复提交报给调用方。
func TestPan115DownloaderTreatsDuplicateAsSubmitted(t *testing.T) {
	client := &pan115ClientStub{addErr: pan115.ErrOfflineExists}
	secret := strings.Repeat("x", 32)
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), client, pan115TestSettings(""), secret)

	if err := service.Downloader("123").Submit(context.Background(), "magnet:?xt=urn:btih:abc"); err != nil {
		t.Fatalf("重复磁力应视为提交成功，实际错误: %v", err)
	}
	if _, err := service.AddOffline(context.Background(), "magnet:?xt=urn:btih:abc", "123"); !errors.Is(err, ErrPan115OfflineExists) {
		t.Fatalf("接口层重复提交错误 = %v", err)
	}
}

// TestPan115OfflineUsesSavePathSetting 验证未显式指定目录时使用设置里的保存目录。
func TestPan115OfflineUsesSavePathSetting(t *testing.T) {
	client := &pan115ClientStub{addHash: "HASH1"}
	secret := strings.Repeat("x", 32)
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), client, pan115TestSettings("456"), secret)

	hash, err := service.AddOffline(context.Background(), "magnet:?xt=urn:btih:abc", "")
	if err != nil || hash != "HASH1" {
		t.Fatalf("提交结果 = %q err=%v", hash, err)
	}
	if client.addDirectory != "456" {
		t.Fatalf("目标目录 = %q，期望设置中的 456", client.addDirectory)
	}
	if _, err := service.AddOffline(context.Background(), "  ", ""); !errors.Is(err, ErrPan115InvalidInput) {
		t.Fatalf("空磁力错误 = %v", err)
	}
}

// TestPan115HasHashScansBoundedOfflinePages 验证按信息哈希回查离线任务，
// 命中即返回，未命中时按页数上限结束。
func TestPan115HasHashScansBoundedOfflinePages(t *testing.T) {
	hit := &pan115ClientStub{offline: pan115.OfflinePage{PageCount: 5, Tasks: []pan115.OfflineTask{{Hash: "ABC"}}}}
	secret := strings.Repeat("x", 32)
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), hit, pan115TestSettings(""), secret)
	found, err := service.Downloader("").HasHash(context.Background(), "abc")
	if err != nil || !found {
		t.Fatalf("命中结果 = %v err=%v", found, err)
	}
	if len(hit.offlinePages) != 1 {
		t.Fatalf("命中后不应继续翻页: %v", hit.offlinePages)
	}

	miss := &pan115ClientStub{offline: pan115.OfflinePage{PageCount: 9, Tasks: []pan115.OfflineTask{{Hash: "OTHER"}}}}
	service = newPan115TestService(t, pan115BoundAccounts(t, secret), miss, pan115TestSettings(""), secret)
	found, err = service.Downloader("").HasHash(context.Background(), "abc")
	if err == nil || found {
		t.Fatalf("不完整回查应返回未知 = %v err=%v", found, err)
	}
	if len(miss.offlinePages) != pan115OfflineScanPages {
		t.Fatalf("回查页数 = %v，期望 %d 页上限", miss.offlinePages, pan115OfflineScanPages)
	}
}

// TestPan115FilesDefaultsToRootAndBoundsLimit 验证空目录落到根目录，且分页上限被收敛。
func TestPan115FilesDefaultsToRootAndBoundsLimit(t *testing.T) {
	client := &pan115ClientStub{page: pan115.FilePage{
		Path:  []pan115.Directory{{ID: "0", Name: "根目录"}},
		Files: []pan115.File{{ID: "f1", Name: "影片.mp4", Size: 2048, PickCode: "pc-1"}},
		Total: 1,
	}}
	secret := strings.Repeat("x", 32)
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), client, pan115TestSettings(""), secret)

	page, err := service.Files(context.Background(), "", -5, 99999)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	// 超出上限的分页大小收敛为默认值，避免一次请求拉取过多条目。
	if client.listDirectory != "0" || client.listOffset != 0 || client.listLimit != pan115DefaultFileLimit {
		t.Fatalf("目录请求参数 = %q offset=%d limit=%d", client.listDirectory, client.listOffset, client.listLimit)
	}
	if page.DirectoryID != "0" || len(page.Files) != 1 || page.Files[0].PickCode != "pc-1" {
		t.Fatalf("目录结果 = %+v", page)
	}
	if len(page.Path) != 1 || page.Path[0].Name != "根目录" {
		t.Fatalf("目录路径 = %+v", page.Path)
	}
}

// TestPan115DirectoryPathAppendsCurrentDirectory 验证目录路径由上级链加目录自身组成。
// 115 的目录信息只回传上级目录，缺末级会让目录选择器的面包屑无法定位当前目录。
func TestPan115DirectoryPathAppendsCurrentDirectory(t *testing.T) {
	client := &pan115ClientStub{info: pan115.FileInfo{
		File: pan115.File{ID: "d2", Name: "套图", IsDirectory: true},
		Path: []pan115.Directory{{ID: "0", Name: "根目录"}, {ID: "d1", Name: "存档"}},
	}}
	secret := strings.Repeat("x", 32)
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), client, pan115TestSettings(""), secret)

	path, err := service.DirectoryPath(context.Background(), "d2")
	if err != nil {
		t.Fatalf("读取目录路径失败: %v", err)
	}
	if client.infoFileID != "d2" {
		t.Fatalf("目录信息请求标识 = %q，期望 d2", client.infoFileID)
	}
	if len(path) != 3 || path[1].Name != "存档" || path[2].ID != "d2" || path[2].Name != "套图" {
		t.Fatalf("目录路径 = %+v", path)
	}
}

// TestPan115DirectoryPathForRoot 验证根目录直接返回固定路径，不额外请求 115 接口。
func TestPan115DirectoryPathForRoot(t *testing.T) {
	client := &pan115ClientStub{}
	secret := strings.Repeat("x", 32)
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), client, pan115TestSettings(""), secret)

	path, err := service.DirectoryPath(context.Background(), "")
	if err != nil {
		t.Fatalf("读取根目录路径失败: %v", err)
	}
	if client.infoFileID != "" {
		t.Fatalf("根目录不应请求目录信息，实际请求 %q", client.infoFileID)
	}
	if len(path) != 1 || path[0].ID != "0" || path[0].Name != "根目录" {
		t.Fatalf("根目录路径 = %+v", path)
	}
}

// TestPan115CookieLoginReturnsCookieOnceAndKeepsChannel 验证 Cookie 扫码按渠道申请二维码、
// 授权后返回一次 Cookie 且重复轮询不再重复换取；Cookie 只存在于内存，不写账号仓储。
func TestPan115CookieLoginReturnsCookieOnceAndKeepsChannel(t *testing.T) {
	secret := strings.Repeat("x", 32)
	repo := &pan115MemoryAccounts{}
	client := &pan115ClientStub{
		cookieLogin: &pan115.CookieLogin{QRCode: []byte("png")},
		cookieState: pan115.LoginScanned,
		cookieValue: "UID=1_A; CID=c; SEID=s",
	}
	service := newPan115TestService(t, repo, client, pan115TestSettings(""), secret)

	session, err := service.StartCookieLogin(context.Background(), "115ipad")
	if err != nil {
		t.Fatalf("开始 Cookie 扫码失败: %v", err)
	}
	if session.ClientType != "115ipad" || !strings.HasPrefix(session.QRCode, "data:image/png;base64,") {
		t.Fatalf("Cookie 扫码会话 = %+v", session)
	}
	if len(client.cookieClients) != 1 || client.cookieClients[0] != "115ipad" {
		t.Fatalf("渠道透传 = %v", client.cookieClients)
	}
	// 未知渠道回落默认值，不把非法值透传给 115。
	if _, err := service.StartCookieLogin(context.Background(), "unknown-app"); err != nil {
		t.Fatalf("未知渠道申请失败: %v", err)
	}
	if len(client.cookieClients) != 2 || client.cookieClients[1] != "alipaymini" {
		t.Fatalf("未知渠道兜底 = %v", client.cookieClients)
	}

	pending, err := service.CookieLoginStatus(context.Background(), session.SessionID)
	if err != nil || pending.Status != "scanned" || pending.Cookie != "" {
		t.Fatalf("已扫码状态 = %+v err=%v", pending, err)
	}

	client.cookieState = pan115.LoginAuthorized
	authorized, err := service.CookieLoginStatus(context.Background(), session.SessionID)
	if err != nil {
		t.Fatalf("授权状态查询失败: %v", err)
	}
	if authorized.Status != "authorized" || authorized.Cookie != "UID=1_A; CID=c; SEID=s" {
		t.Fatalf("授权结果 = %+v", authorized)
	}
	if repo.found {
		t.Fatal("Cookie 扫码不应写入账号仓储")
	}

	exchanges := client.cookieCalls
	again, err := service.CookieLoginStatus(context.Background(), session.SessionID)
	if err != nil || again.Cookie != authorized.Cookie || client.cookieCalls != exchanges {
		t.Fatalf("重复查询 = %+v err=%v calls=%d", again, err, client.cookieCalls)
	}
}

// TestPan115CookieLoginRejectsUnknownSession 验证不存在的 Cookie 扫码会话不会被当作等待中。
func TestPan115CookieLoginRejectsUnknownSession(t *testing.T) {
	service := newPan115TestService(t, &pan115MemoryAccounts{}, &pan115ClientStub{}, pan115TestSettings(""), strings.Repeat("x", 32))
	if _, err := service.CookieLoginStatus(context.Background(), "missing"); !errors.Is(err, ErrPan115LoginUnknown) {
		t.Fatalf("未知会话错误 = %v", err)
	}
}

// TestPan115CookieLoginDropsSessionAfterFailure 验证过期或取消后会话被清理，再次查询按未知会话处理，
// 避免前端在二维码失效后仍反复拿到旧状态。
func TestPan115CookieLoginDropsSessionAfterFailure(t *testing.T) {
	for _, state := range []pan115.LoginState{pan115.LoginExpired, pan115.LoginCanceled} {
		client := &pan115ClientStub{
			cookieLogin: &pan115.CookieLogin{QRCode: []byte("png")},
			cookieState: state,
		}
		service := newPan115TestService(t, &pan115MemoryAccounts{}, client, pan115TestSettings(""), strings.Repeat("x", 32))
		session, err := service.StartCookieLogin(context.Background(), "")
		if err != nil {
			t.Fatalf("开始 Cookie 扫码失败: %v", err)
		}
		result, err := service.CookieLoginStatus(context.Background(), session.SessionID)
		if err != nil || string(result.Status) != string(state) {
			t.Fatalf("状态 %s 结果 = %+v err=%v", state, result, err)
		}
		if _, err := service.CookieLoginStatus(context.Background(), session.SessionID); !errors.Is(err, ErrPan115LoginUnknown) {
			t.Fatalf("状态 %s 二次查询错误 = %v", state, err)
		}
	}
}

// TestPan115CancelCookieLoginForgetsSession 验证主动取消后会话立即失效。
func TestPan115CancelCookieLoginForgetsSession(t *testing.T) {
	client := &pan115ClientStub{cookieLogin: &pan115.CookieLogin{QRCode: []byte("png")}}
	service := newPan115TestService(t, &pan115MemoryAccounts{}, client, pan115TestSettings(""), strings.Repeat("x", 32))
	session, err := service.StartCookieLogin(context.Background(), "tv")
	if err != nil {
		t.Fatalf("开始 Cookie 扫码失败: %v", err)
	}
	service.CancelCookieLogin(session.SessionID)
	if _, err := service.CookieLoginStatus(context.Background(), session.SessionID); !errors.Is(err, ErrPan115LoginUnknown) {
		t.Fatalf("取消后查询错误 = %v", err)
	}
}

// TestPan115ObserveCompleted 保留下载完成事实而非只返回任务存在。
func TestPan115ObserveCompleted(t *testing.T) {
	client := &pan115ClientStub{offline: pan115.OfflinePage{PageCount: 1, Tasks: []pan115.OfflineTask{{Hash: "abc", Status: 2, Progress: 100}}}}
	secret := strings.Repeat("x", 32)
	service := newPan115TestService(t, pan115BoundAccounts(t, secret), client, pan115TestSettings(""), secret)
	state, err := service.Downloader("").(interface {
		Observe(context.Context, string) (*ports.TransferState, error)
	}).Observe(context.Background(), "abc")
	if err != nil || state == nil || state.Status != "completed" {
		t.Fatalf("state=%v err=%v", state, err)
	}
}
