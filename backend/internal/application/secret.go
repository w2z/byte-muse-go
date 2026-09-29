package application

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// secretCipher 用 SESSION_SECRET 派生的 AES-GCM 密钥保护落库凭据。
// 站点凭据、翻译密钥与 115 令牌共用同一实现，避免出现第二套加密口径。
type secretCipher struct{ aead cipher.AEAD }

func newSecretCipher(secret string) (*secretCipher, error) {
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create secret AEAD: %w", err)
	}
	return &secretCipher{aead: aead}, nil
}

// encrypt 生成 nonce 前置的密文并做无填充 base64 编码。
func (c *secretCipher) encrypt(value string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create secret nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(value), nil)
	return base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

// decrypt 还原密文；空值表示未配置，直接返回空串。
func (c *secretCipher) decrypt(value string) (string, error) {
	encoded := strings.TrimSpace(value)
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode encrypted secret: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("encrypted secret is too short")
	}
	plain, err := c.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("open encrypted secret: %w", err)
	}
	return string(plain), nil
}
