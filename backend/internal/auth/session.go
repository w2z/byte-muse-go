// Package auth provides stateless signed administrator sessions.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	cookieName = "bytemuse_session"

	// defaultTTL 是未勾选“记住密码”时的会话有效期。
	defaultTTL = 24 * time.Hour
	// rememberTTL 是勾选“记住密码”时的会话有效期。
	rememberTTL = 30 * 24 * time.Hour
	// defaultRefreshGrace 是会话过期后仍允许续签的宽限窗口。
	defaultRefreshGrace = 24 * time.Hour
)

var (
	// ErrInvalidCredentials reports a rejected administrator login without revealing which field differed.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrInvalidSession reports a malformed, forged, or expired session token.
	ErrInvalidSession = errors.New("invalid session")
)

// Config defines administrator credentials and signed-session behavior.
type Config struct {
	Username string
	Password string
	Secret   string
	// TTL 是普通登录的会话有效期；小于等于 0 时回落为 24 小时。
	TTL time.Duration
	// RememberTTL 是勾选“记住密码”后的会话有效期；小于等于 0 时回落为 30 天。
	RememberTTL time.Duration
	// RefreshGrace 是会话过期后仍允许续签的宽限窗口；小于等于 0 时回落为 24 小时。
	RefreshGrace time.Duration
	Secure       bool
	Now          func() time.Time
}

// User is the authenticated administrator identity returned by the public API.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// Session contains a signed bearer value and its absolute expiry.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// Service validates administrator credentials and signs stateless sessions.
type Service struct {
	username     string
	password     [sha256.Size]byte
	secret       []byte
	ttl          time.Duration
	rememberTTL  time.Duration
	refreshGrace time.Duration
	secure       bool
	now          func() time.Time
	user         User
}

// claims 是签名会话的载荷。Expires 为绝对过期时间，Remember 记录签发时的档位以便续签沿用。
type claims struct {
	Username string `json:"username"`
	Expires  int64  `json:"expires"`
	Remember bool   `json:"remember,omitempty"`
}

// New validates authentication configuration and constructs a session service.
func New(config Config) (*Service, error) {
	if strings.TrimSpace(config.Username) == "" {
		return nil, fmt.Errorf("administrator username is required")
	}
	if config.Password == "" {
		return nil, fmt.Errorf("administrator password is required")
	}
	if len(config.Secret) < 32 {
		return nil, fmt.Errorf("session secret must contain at least 32 bytes")
	}
	if config.TTL <= 0 {
		config.TTL = defaultTTL
	}
	if config.RememberTTL <= 0 {
		config.RememberTTL = rememberTTL
	}
	if config.RefreshGrace <= 0 {
		config.RefreshGrace = defaultRefreshGrace
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	userHash := sha256.Sum256([]byte(config.Username))
	return &Service{
		username:     config.Username,
		password:     sha256.Sum256([]byte(config.Password)),
		secret:       []byte(config.Secret),
		ttl:          config.TTL,
		rememberTTL:  config.RememberTTL,
		refreshGrace: config.RefreshGrace,
		secure:       config.Secure,
		now:          config.Now,
		user: User{
			ID:       base64.RawURLEncoding.EncodeToString(userHash[:12]),
			Username: config.Username,
		},
	}, nil
}

// Login compares credentials in constant time and returns a signed expiring session.
// remember 为真时按“记住密码”签发更长有效期的会话。
func (s *Service) Login(username, password string, remember bool) (Session, error) {
	usernameHash := sha256.Sum256([]byte(username))
	expectedUsernameHash := sha256.Sum256([]byte(s.username))
	passwordHash := sha256.Sum256([]byte(password))
	usernameOK := subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:])
	passwordOK := subtle.ConstantTimeCompare(passwordHash[:], s.password[:])
	if usernameOK&passwordOK != 1 {
		return Session{}, ErrInvalidCredentials
	}
	expiresAt := s.now().UTC().Add(s.ttlFor(remember))
	return s.issue(remember, expiresAt)
}

// Refresh 在会话仍然有效、或过期未超过宽限窗口时，按原档位重新签发满额会话。
// 超出宽限窗口视为彻底失效，调用方应要求重新登录。
func (s *Service) Refresh(token string) (Session, error) {
	decoded, err := s.decode(token)
	if err != nil {
		return Session{}, err
	}
	deadline := time.Unix(decoded.Expires, 0).Add(s.refreshGrace)
	if s.now().UTC().After(deadline) {
		return Session{}, ErrInvalidSession
	}
	return s.issue(decoded.Remember, s.now().UTC().Add(s.ttlFor(decoded.Remember)))
}

// Validate verifies the token signature, configured identity, and expiration.
func (s *Service) Validate(token string) (User, error) {
	decoded, err := s.decode(token)
	if err != nil {
		return User{}, err
	}
	if decoded.Expires <= s.now().UTC().Unix() {
		return User{}, ErrInvalidSession
	}
	return s.user, nil
}

// decode 校验签名与身份，但不判断过期时间，供 Validate 和 Refresh 共用。
func (s *Service) decode(token string) (claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return claims{}, ErrInvalidSession
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, s.sign(parts[0])) {
		return claims{}, ErrInvalidSession
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims{}, ErrInvalidSession
	}
	var decoded claims
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return claims{}, ErrInvalidSession
	}
	usernameHash := sha256.Sum256([]byte(decoded.Username))
	expectedUsernameHash := sha256.Sum256([]byte(s.username))
	if subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) != 1 {
		return claims{}, ErrInvalidSession
	}
	return decoded, nil
}

// issue 用固定身份和绝对过期时间签发签名会话。
func (s *Service) issue(remember bool, expiresAt time.Time) (Session, error) {
	payload, err := json.Marshal(claims{Username: s.username, Expires: expiresAt.Unix(), Remember: remember})
	if err != nil {
		return Session{}, fmt.Errorf("encode session: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := s.sign(encodedPayload)
	return Session{Token: encodedPayload + "." + base64.RawURLEncoding.EncodeToString(signature), ExpiresAt: expiresAt}, nil
}

// Cookie builds the protected browser cookie carrying a signed session token.
func (s *Service) Cookie(session Session) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		MaxAge:   max(1, int(session.ExpiresAt.Sub(s.now().UTC()).Seconds())),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// CookieName returns the fixed public session-cookie name.
func CookieName() string { return cookieName }

// ClearCookie 返回用于注销的过期会话 Cookie，属性与登录 Cookie 保持一致。
func (s *Service) ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ttlFor 返回本次登录应使用的会话有效期：勾选“记住密码”时用 rememberTTL，否则用 ttl。
func (s *Service) ttlFor(remember bool) time.Duration {
	if remember {
		return s.rememberTTL
	}
	return s.ttl
}

func (s *Service) sign(payload string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
