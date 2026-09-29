package wechat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// encodingAESKeyLength 是企业微信 EncodingAESKey 的字符数，去掉补位后固定解码为 32 字节。
const encodingAESKeyLength = 43

// pkcs7BlockSize 与官方示例一致：填充按 32 字节块计算，解密时同样按该范围校验。
const pkcs7BlockSize = 32

// errSignatureMismatch 表示回调签名校验失败，调用方应丢弃该请求。
var errSignatureMismatch = errors.New("企业微信回调签名校验失败")

// Crypto 实现企业微信回调的签名校验与 AES 消息解密。
// 同一实例绑定一组 token/EncodingAESKey/corpID，并发安全，可在多个请求间复用。
type Crypto struct {
	token  string
	aesKey []byte
	corpID string
}

// NewCrypto 构造回调加解密器；EncodingAESKey 必须是 43 字符 base64 且解码为 32 字节。
func NewCrypto(token, encodingAESKey, corpID string) (*Crypto, error) {
	key, err := decodeEncodingAESKey(encodingAESKey)
	if err != nil {
		return nil, err
	}
	return &Crypto{token: token, aesKey: key, corpID: corpID}, nil
}

// decodeEncodingAESKey 补齐 base64 补位并校验长度；长度不符直接拒绝，避免带错密钥运行。
func decodeEncodingAESKey(encodingAESKey string) ([]byte, error) {
	trimmed := strings.TrimSpace(encodingAESKey)
	if len(trimmed) != encodingAESKeyLength {
		return nil, fmt.Errorf("企业微信 EncodingAESKey 必须为 %d 位字符，实际 %d 位", encodingAESKeyLength, len(trimmed))
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed + "=")
	if err != nil {
		return nil, fmt.Errorf("企业微信 EncodingAESKey 解码失败：%w", err)
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("企业微信 EncodingAESKey 必须解码为 32 字节，实际 %d 字节", len(decoded))
	}
	return decoded, nil
}

// VerifyURL 校验 GET 回调签名并解密 echostr，返回应原样回写给企业微信的明文。
func (c *Crypto) VerifyURL(signature, timestamp, nonce, echostr string) (string, error) {
	if c == nil {
		return "", errors.New("企业微信回调加解密器未初始化")
	}
	if strings.TrimSpace(echostr) == "" {
		return "", errors.New("企业微信回调缺少 echostr")
	}
	if !c.validSignature(signature, timestamp, nonce, echostr) {
		return "", errSignatureMismatch
	}
	plain, err := c.decrypt(echostr)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Decrypt 校验 POST 回调签名并解密消息体，返回明文 XML。
// 签名基于外层 XML 中的 Encrypt 计算，签名不符时不进入解密流程。
func (c *Crypto) Decrypt(signature, timestamp, nonce string, body []byte) ([]byte, error) {
	if c == nil {
		return nil, errors.New("企业微信回调加解密器未初始化")
	}
	var envelope encryptedEnvelope
	if err := xml.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解析企业微信回调密文失败：%w", err)
	}
	encrypt := strings.TrimSpace(envelope.Encrypt)
	if encrypt == "" {
		return nil, errors.New("企业微信回调缺少 Encrypt 字段")
	}
	if !c.validSignature(signature, timestamp, nonce, encrypt) {
		return nil, errSignatureMismatch
	}
	return c.decrypt(encrypt)
}

// validSignature 按官方规则比较签名：token、timestamp、nonce、encrypt 字典序拼接后取 sha1。
// 使用常量时间比较，避免通过响应时间推断签名。
func (c *Crypto) validSignature(signature, timestamp, nonce, encrypt string) bool {
	parts := []string{c.token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	expected := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// decrypt 完成 base64 解码、AES-256-CBC 解密与明文结构校验。
// 明文结构为 16 字节随机数 + 4 字节大端长度 + 消息体 + corpID，任一环节不符都返回错误。
func (c *Crypto) decrypt(encrypted string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("企业微信密文 base64 解码失败：%w", err)
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, errors.New("企业微信密文长度不合法")
	}
	block, err := aes.NewCipher(c.aesKey)
	if err != nil {
		return nil, fmt.Errorf("企业微信 AES 初始化失败：%w", err)
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, c.aesKey[:aes.BlockSize]).CryptBlocks(plain, raw)
	content, err := unpadPKCS7(plain)
	if err != nil {
		return nil, err
	}
	if len(content) < 20 {
		return nil, errors.New("企业微信明文长度不足")
	}
	messageLength := int(binary.BigEndian.Uint32(content[16:20]))
	if messageLength < 0 || messageLength > len(content)-20 {
		return nil, errors.New("企业微信明文的长度字段越界")
	}
	message := content[20 : 20+messageLength]
	if string(content[20+messageLength:]) != c.corpID {
		return nil, errors.New("企业微信回调 corpID 与当前配置不一致")
	}
	return message, nil
}

// unpadPKCS7 去除 PKCS#7 填充；填充值非法或填充字节不一致时返回错误而不是静默截断。
func unpadPKCS7(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("企业微信明文为空")
	}
	padding := int(data[len(data)-1])
	if padding < 1 || padding > pkcs7BlockSize || padding > len(data) {
		return nil, errors.New("企业微信 PKCS#7 填充非法")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, errors.New("企业微信 PKCS#7 填充非法")
		}
	}
	return data[:len(data)-padding], nil
}

// encryptedEnvelope 是企业微信推送的加密外层 XML，签名与解密都基于 Encrypt 字段。
type encryptedEnvelope struct {
	XMLName xml.Name `xml:"xml"`
	Encrypt string   `xml:"Encrypt"`
}
