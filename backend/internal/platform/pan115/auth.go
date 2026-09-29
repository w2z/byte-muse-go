package pan115

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// openListAppID 是扫码登录使用的公开应用标识。
const openListAppID = "100197303"

// LoginState 是一次扫码登录对外可见的状态。
type LoginState string

const (
	LoginWaiting    LoginState = "waiting"
	LoginScanned    LoginState = "scanned"
	LoginAuthorized LoginState = "authorized"
	LoginExpired    LoginState = "expired"
	LoginCanceled   LoginState = "canceled"
)

// Login 保存一次扫码登录的二维码与设备码；设备码与 PKCE 校验值不出本包。
type Login struct {
	QRCode []byte

	uid      string
	issuedAt int64
	sign     string
	verifier string
}

// Tokens 是 115 下发的访问令牌与刷新令牌。
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// BeginLogin 申请设备码并取回登录二维码 PNG。
func (c *Client) BeginLogin(ctx context.Context) (*Login, error) {
	random := make([]byte, 48)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("生成 115 PKCE 校验值失败: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(random)
	digest := sha256.Sum256([]byte(verifier))
	// 115 要求 code_challenge = base64(SHA256(verifier))，方法名为 sha256。
	challenge := base64.StdEncoding.EncodeToString(digest[:])
	data, err := authRequest[struct {
		UID  string `json:"uid"`
		Time int64  `json:"time"`
		Sign string `json:"sign"`
	}](ctx, c, http.MethodPost, c.passport+"/open/authDeviceCode", url.Values{
		"client_id":             {openListAppID},
		"code_challenge":        {challenge},
		"code_challenge_method": {"sha256"},
	})
	if err != nil {
		return nil, err
	}
	if data.UID == "" || data.Time == 0 || data.Sign == "" {
		return nil, fmt.Errorf("115 授权响应缺少设备码字段")
	}
	response, err := c.do(ctx, http.MethodGet, c.qrcode+"/api/1.0/web/1.0/qrcode?uid="+url.QueryEscape(data.UID), "", nil)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, &TransportError{Err: fmt.Errorf("115 返回 HTTP %d", response.StatusCode)}
	}
	image, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	_ = response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("读取 115 登录二维码失败: %w", err)
	}
	if http.DetectContentType(image) != "image/png" {
		return nil, fmt.Errorf("115 未返回登录二维码图片")
	}
	return &Login{QRCode: image, uid: data.UID, issuedAt: data.Time, sign: data.Sign, verifier: verifier}, nil
}

// LoginStatus 查询一次扫码状态。uid/time/sign 由 do 作为查询参数提交，
// 未识别的状态一律按「等待中」处理，避免把仍在进行的登录误判为失败；超时由调用方控制。
func (c *Client) LoginStatus(ctx context.Context, login *Login) (LoginState, error) {
	data, err := authRequest[struct {
		Status *int `json:"status"`
	}](ctx, c, http.MethodGet, c.qrcode+"/get/status/", url.Values{
		"uid":  {login.uid},
		"time": {strconv.FormatInt(login.issuedAt, 10)},
		"sign": {login.sign},
	})
	if err != nil {
		return "", err
	}
	if data.Status == nil {
		return LoginWaiting, nil
	}
	switch *data.Status {
	case 0:
		return LoginWaiting, nil
	case 1:
		return LoginScanned, nil
	case 2:
		return LoginAuthorized, nil
	case -1:
		return LoginExpired, nil
	case -2:
		return LoginCanceled, nil
	default:
		return LoginWaiting, nil
	}
}

// ExchangeToken 用已授权的设备码换取访问令牌。
func (c *Client) ExchangeToken(ctx context.Context, login *Login) (Tokens, error) {
	return c.requestTokens(ctx, "/open/deviceCodeToToken", url.Values{
		"uid":           {login.uid},
		"code_verifier": {login.verifier},
	})
}

// RefreshToken 用刷新令牌换取新的访问令牌。
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (Tokens, error) {
	return c.requestTokens(ctx, "/open/refreshToken", url.Values{"refresh_token": {refreshToken}})
}

func (c *Client) requestTokens(ctx context.Context, path string, form url.Values) (Tokens, error) {
	issuedAt := time.Now()
	data, err := authRequest[struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}](ctx, c, http.MethodPost, c.passport+path, form)
	if err != nil {
		return Tokens{}, err
	}
	if data.AccessToken == "" || data.RefreshToken == "" || data.ExpiresIn <= 0 {
		return Tokens{}, fmt.Errorf("115 令牌响应缺少凭据或有效期")
	}
	return Tokens{
		AccessToken:  data.AccessToken,
		RefreshToken: data.RefreshToken,
		ExpiresAt:    issuedAt.Add(time.Duration(data.ExpiresIn) * time.Second),
	}, nil
}
