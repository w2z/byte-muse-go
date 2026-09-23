package auth_test

import (
	"errors"
	"testing"
	"time"

	"bytemuse/backend/internal/auth"
)

func TestSessionLoginAndValidation(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	service, err := auth.New(auth.Config{
		Username: "operator",
		Password: "correct-password",
		Secret:   "0123456789abcdef0123456789abcdef",
		TTL:      time.Hour,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	session, err := service.Login("operator", "correct-password", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session.Token == "" || !session.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("session = %+v, want signed token expiring in one hour", session)
	}
	user, err := service.Validate(session.Token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if user.Username != "operator" || user.ID == "" {
		t.Fatalf("user = %+v, want stable id and configured username", user)
	}
}

func TestSessionRejectsCredentialsTamperingAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	service, err := auth.New(auth.Config{
		Username: "operator",
		Password: "correct-password",
		Secret:   "0123456789abcdef0123456789abcdef",
		TTL:      time.Minute,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	if _, err := service.Login("operator", "wrong-password", false); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v, want ErrInvalidCredentials", err)
	}
	session, err := service.Login("operator", "correct-password", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := service.Validate(session.Token + "tampered"); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("tampered token error = %v, want ErrInvalidSession", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := service.Validate(session.Token); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("expired token error = %v, want ErrInvalidSession", err)
	}
}

// 未勾选“记住密码”的会话保留 1 天，勾选后保留 30 天；两者都走 TTL 未配置时的默认值。
func TestSessionRememberExtendsExpiry(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	service, err := auth.New(auth.Config{
		Username: "operator",
		Password: "correct-password",
		Secret:   "0123456789abcdef0123456789abcdef",
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	plain, err := service.Login("operator", "correct-password", false)
	if err != nil {
		t.Fatalf("login without remember: %v", err)
	}
	if want := now.Add(24 * time.Hour); !plain.ExpiresAt.Equal(want) {
		t.Fatalf("session without remember expires at %v, want %v", plain.ExpiresAt, want)
	}

	remembered, err := service.Login("operator", "correct-password", true)
	if err != nil {
		t.Fatalf("login with remember: %v", err)
	}
	if want := now.Add(30 * 24 * time.Hour); !remembered.ExpiresAt.Equal(want) {
		t.Fatalf("remembered session expires at %v, want %v", remembered.ExpiresAt, want)
	}

	// 推进到第 25 天：普通会话已过期，勾选“记住密码”的会话仍然有效。
	now = now.Add(25 * 24 * time.Hour)
	if _, err := service.Validate(plain.Token); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("ordinary session at day 25 error = %v, want ErrInvalidSession", err)
	}
	if _, err := service.Validate(remembered.Token); err != nil {
		t.Fatalf("remembered session at day 25 error = %v, want valid session", err)
	}

	// 第 31 天两者都过期。
	now = now.Add(6 * 24 * time.Hour)
	if _, err := service.Validate(remembered.Token); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("remembered session at day 31 error = %v, want ErrInvalidSession", err)
	}
}

// 会话有效期内续签回到满额；过期后 1 天宽限窗口内仍可续签，超出宽限彻底失效。
func TestSessionRefreshSlidesWithinValidityAndGrace(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	service, err := auth.New(auth.Config{
		Username: "operator",
		Password: "correct-password",
		Secret:   "0123456789abcdef0123456789abcdef",
		TTL:      time.Hour,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	session, err := service.Login("operator", "correct-password", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// 有效期内续签：有效期回到满额 1 小时。
	now = now.Add(30 * time.Minute)
	slid, err := service.Refresh(session.Token)
	if err != nil {
		t.Fatalf("refresh within validity: %v", err)
	}
	if want := now.Add(time.Hour); !slid.ExpiresAt.Equal(want) {
		t.Fatalf("slid session expires at %v, want %v", slid.ExpiresAt, want)
	}
	if _, err := service.Validate(slid.Token); err != nil {
		t.Fatalf("validate slid session: %v", err)
	}

	// 已过期但在 1 天宽限窗口内：Validate 拒绝，Refresh 仍可续签。
	now = slid.ExpiresAt.Add(time.Hour)
	if _, err := service.Validate(slid.Token); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("expired session error = %v, want ErrInvalidSession", err)
	}
	revived, err := service.Refresh(slid.Token)
	if err != nil {
		t.Fatalf("refresh within grace: %v", err)
	}
	if want := now.Add(time.Hour); !revived.ExpiresAt.Equal(want) {
		t.Fatalf("revived session expires at %v, want %v", revived.ExpiresAt, want)
	}

	// 超出宽限窗口：连续签也拒绝。
	now = revived.ExpiresAt.Add(24*time.Hour + time.Second)
	if _, err := service.Refresh(revived.Token); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("refresh beyond grace error = %v, want ErrInvalidSession", err)
	}

	// 篡改过的 token 无法续签。
	if _, err := service.Refresh(session.Token + "tampered"); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("refresh tampered token error = %v, want ErrInvalidSession", err)
	}
}

// 续签沿用签发时的档位：普通会话续 1 天，勾选“记住密码”的会话续 30 天。
func TestSessionRefreshKeepsRememberTier(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	service, err := auth.New(auth.Config{
		Username: "operator",
		Password: "correct-password",
		Secret:   "0123456789abcdef0123456789abcdef",
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	plain, err := service.Login("operator", "correct-password", false)
	if err != nil {
		t.Fatalf("login without remember: %v", err)
	}
	remembered, err := service.Login("operator", "correct-password", true)
	if err != nil {
		t.Fatalf("login with remember: %v", err)
	}

	// 推到普通会话刚过期：普通会话续回 1 天，勾选会话续回 30 天。
	now = plain.ExpiresAt.Add(time.Hour)

	slidPlain, err := service.Refresh(plain.Token)
	if err != nil {
		t.Fatalf("refresh ordinary session: %v", err)
	}
	if want := now.Add(24 * time.Hour); !slidPlain.ExpiresAt.Equal(want) {
		t.Fatalf("ordinary refreshed session expires at %v, want %v", slidPlain.ExpiresAt, want)
	}

	slidRemembered, err := service.Refresh(remembered.Token)
	if err != nil {
		t.Fatalf("refresh remembered session: %v", err)
	}
	if want := now.Add(30 * 24 * time.Hour); !slidRemembered.ExpiresAt.Equal(want) {
		t.Fatalf("remembered refreshed session expires at %v, want %v", slidRemembered.ExpiresAt, want)
	}
}
