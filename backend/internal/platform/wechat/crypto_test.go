package wechat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"
	"testing"
)

const (
	testToken  = "bytemuseCallbackToken"
	testCorpID = "ww0123456789abcdef"
)

// testAESKey 生成一对夹具密钥：43 位 EncodingAESKey 与其 32 字节原始值。
func testAESKey(t *testing.T) (string, []byte) {
	t.Helper()
	raw := make([]byte, 32)
	for index := range raw {
		raw[index] = byte((index*7 + 11) % 251)
	}
	encoded := strings.TrimRight(base64.StdEncoding.EncodeToString(raw), "=")
	if len(encoded) != encodingAESKeyLength {
		t.Fatalf("夹具 EncodingAESKey 长度异常：%d", len(encoded))
	}
	return encoded, raw
}

// encryptContent 用企业微信明文结构生成密文，用于在无外网依赖下构造回调夹具。
func encryptContent(t *testing.T, key, content []byte) string {
	t.Helper()
	return encryptRaw(t, key, padPKCS7(content, pkcs7BlockSize))
}

// encryptRaw 直接加密整块数据，不补填充，用于构造非法填充等异常夹具。
func encryptRaw(t *testing.T, key, block []byte) string {
	t.Helper()
	if len(block)%aes.BlockSize != 0 {
		t.Fatalf("待加密数据必须是 %d 字节的整数倍，实际 %d", aes.BlockSize, len(block))
	}
	cipherBlock, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("构造 AES 失败：%v", err)
	}
	out := make([]byte, len(block))
	cipher.NewCBCEncrypter(cipherBlock, key[:aes.BlockSize]).CryptBlocks(out, block)
	return base64.StdEncoding.EncodeToString(out)
}

// wrapMessage 按官方明文结构封装消息：16 字节随机数 + 4 字节大端长度 + 消息 + corpID。
func wrapMessage(t *testing.T, message, corpID string) []byte {
	t.Helper()
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("生成随机数失败：%v", err)
	}
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(message)))
	content := append([]byte{}, random...)
	content = append(content, length...)
	content = append(content, []byte(message)...)
	content = append(content, []byte(corpID)...)
	return content
}

func encryptMessage(t *testing.T, key []byte, message, corpID string) string {
	t.Helper()
	return encryptContent(t, key, wrapMessage(t, message, corpID))
}

func padPKCS7(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(append([]byte{}, data...), bytes.Repeat([]byte{byte(padding)}, padding)...)
}

// signFixture 复刻官方签名规则，用于生成与实现相互独立的期望签名。
func signFixture(token, timestamp, nonce, encrypt string) string {
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

func newTestCrypto(t *testing.T) (*Crypto, []byte) {
	t.Helper()
	encoded, raw := testAESKey(t)
	crypt, err := NewCrypto(testToken, encoded, testCorpID)
	if err != nil {
		t.Fatalf("构造 Crypto 失败：%v", err)
	}
	return crypt, raw
}

func TestCryptoDecryptRoundTrip(t *testing.T) {
	crypt, key := newTestCrypto(t)
	message := "<xml><Content>ABC-123</Content></xml>"
	encrypt := encryptMessage(t, key, message, testCorpID)
	body := []byte("<xml><Encrypt><![CDATA[" + encrypt + "]]></Encrypt></xml>")
	signature := signFixture(testToken, "1717000000", "nonce-value", encrypt)

	plain, err := crypt.Decrypt(signature, "1717000000", "nonce-value", body)
	if err != nil {
		t.Fatalf("解密失败：%v", err)
	}
	if string(plain) != message {
		t.Fatalf("明文不匹配：%q", string(plain))
	}
}

func TestCryptoDecryptRejectsSignatureMismatch(t *testing.T) {
	crypt, key := newTestCrypto(t)
	encrypt := encryptMessage(t, key, "<xml/>", testCorpID)
	body := []byte("<xml><Encrypt>" + encrypt + "</Encrypt></xml>")
	badSignature := signFixture("other-token", "1717000000", "nonce-value", encrypt)

	if _, err := crypt.Decrypt(badSignature, "1717000000", "nonce-value", body); err == nil {
		t.Fatal("签名不匹配时必须返回错误")
	}
}

func TestCryptoDecryptRejectsCorpIDMismatch(t *testing.T) {
	crypt, key := newTestCrypto(t)
	encrypt := encryptMessage(t, key, "<xml/>", "ww-other-corp")
	body := []byte("<xml><Encrypt>" + encrypt + "</Encrypt></xml>")
	signature := signFixture(testToken, "1717000000", "nonce-value", encrypt)

	if _, err := crypt.Decrypt(signature, "1717000000", "nonce-value", body); err == nil {
		t.Fatal("corpID 不一致时必须返回错误")
	}
}

func TestCryptoDecryptRejectsInvalidPadding(t *testing.T) {
	crypt, key := newTestCrypto(t)
	content := make([]byte, 32)
	content[len(content)-1] = 0
	encrypt := encryptRaw(t, key, content)
	body := []byte("<xml><Encrypt>" + encrypt + "</Encrypt></xml>")
	signature := signFixture(testToken, "1717000000", "nonce-value", encrypt)

	if _, err := crypt.Decrypt(signature, "1717000000", "nonce-value", body); err == nil {
		t.Fatal("PKCS#7 填充非法时必须返回错误")
	}
}

func TestCryptoDecryptRejectsMessageLengthOverflow(t *testing.T) {
	crypt, key := newTestCrypto(t)
	random := make([]byte, 16)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, 4096)
	content := append([]byte{}, random...)
	content = append(content, length...)
	content = append(content, []byte(testCorpID)...)
	encrypt := encryptContent(t, key, content)
	body := []byte("<xml><Encrypt>" + encrypt + "</Encrypt></xml>")
	signature := signFixture(testToken, "1717000000", "nonce-value", encrypt)

	if _, err := crypt.Decrypt(signature, "1717000000", "nonce-value", body); err == nil {
		t.Fatal("长度字段越界时必须返回错误")
	}
}

func TestCryptoDecryptRejectsMalformedEnvelope(t *testing.T) {
	crypt, _ := newTestCrypto(t)
	if _, err := crypt.Decrypt("signature", "1717000000", "nonce-value", []byte("not-xml")); err == nil {
		t.Fatal("外层 XML 非法时必须返回错误")
	}
	if _, err := crypt.Decrypt("signature", "1717000000", "nonce-value", []byte("<xml><Other>1</Other></xml>")); err == nil {
		t.Fatal("缺少 Encrypt 字段时必须返回错误")
	}
}

func TestCryptoVerifyURLReturnsEcho(t *testing.T) {
	crypt, key := newTestCrypto(t)
	echo := "1616140317555161061"
	encrypt := encryptMessage(t, key, echo, testCorpID)
	signature := signFixture(testToken, "1717000000", "nonce-value", encrypt)

	plain, err := crypt.VerifyURL(signature, "1717000000", "nonce-value", encrypt)
	if err != nil {
		t.Fatalf("VerifyURL 失败：%v", err)
	}
	if plain != echo {
		t.Fatalf("回写明文不匹配：%q", plain)
	}
}

func TestNewCryptoRejectsInvalidKey(t *testing.T) {
	if _, err := NewCrypto(testToken, "too-short", testCorpID); err == nil {
		t.Fatal("长度不足的 EncodingAESKey 必须报错")
	}
	if _, err := NewCrypto(testToken, strings.Repeat("!", encodingAESKeyLength), testCorpID); err == nil {
		t.Fatal("非法 base64 的 EncodingAESKey 必须报错")
	}
}
