package sign

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/bits"
	"regexp"
	"strings"
)

// spadeKDFSalt 是 spade 地址解密的 KDF 盐（源实现 decryptSpadeUrl 的 constants）。
var spadeKDFSalt = mustHex("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")

// spadeKeyPattern 校验派生出的内容密钥文本（对齐源实现的 /^[0-9a-f]{32}$/i）。
var spadeKeyPattern = regexp.MustCompile(`(?i)^[0-9a-f]{32}$`)

// SpadeKeyDeriver 实现 KeyDeriver：从 spade_a 派生 16 字节内容密钥。
//
// 移植自 hongguo-open-player/src/server/native/app-source.js（GPL-3.0-only）的
// deriveContentKey 与 decryptSpadeUrl。
type SpadeKeyDeriver struct{}

// 编译期确认接口实现。
var _ KeyDeriver = SpadeKeyDeriver{}

// DeriveContentKey 从 spade_a 文本派生 16 字节内容密钥。
//
// encryptInfo 传入 spade_a 字符串的字节（可能带 base64url 字母表或省略填充）。
// 逐位对齐源实现的 deriveContentKey：长度推导 → 85/246 游走 → popcount 修正 →
// 取 1..33 字节当 ascii 十六进制校验。
func (SpadeKeyDeriver) DeriveContentKey(encryptInfo []byte) ([]byte, error) {
	raw := decodeSpadeBase64(strings.TrimSpace(string(encryptInfo)))
	if len(raw) < 3 {
		return nil, errors.New("spade_a 长度不足")
	}

	// 长度由前三个字节异或推导，并不得超过缓冲区本身。
	size := len(raw) - int(raw[0]^raw[1]^raw[2]) + 47
	if size > len(raw)-1 {
		size = len(raw) - 1
	}
	if size < 33 {
		return nil, errors.New("spade_a 密钥长度无效")
	}

	work := make([]byte, size)
	copy(work, raw[1:1+size])

	a, b := byte(85), byte(246)
	for i := range size {
		old := work[i]
		var previous byte
		if i&1 == 1 {
			previous = a
			a = old
		} else {
			previous = b
			b = old
		}
		work[i] = byte((-21 - bits.OnesCount(uint(i)) + int(previous^old)) & 0xFF)
	}

	text := string(work[1:33])
	if !spadeKeyPattern.MatchString(text) {
		return nil, errors.New("App 视频密钥格式无效")
	}
	return hex.DecodeString(text)
}

// DecryptSpadeURL 用内容密钥种子解密 spade 加密的片源地址。
//
// 逐位对齐源实现的 decryptSpadeUrl：sha512 两段 KDF 派生 AES-128 密钥与 IV，
// CBC 解密（禁用自动填充），再依次去掉 PKCS 填充与尾部 0 字节。
// seed 传入 key_seed 的原始字节；value 为空时返回空串。
func DecryptSpadeURL(value string, seed []byte) (string, error) {
	encoded := strings.TrimSpace(value)
	if encoded == "" {
		return "", nil
	}

	raw := decodeSpadeBase64(encoded)
	if len(raw) < 5 {
		return "", errors.New("spade 地址密文长度不足")
	}
	if raw[0] != 0xA8 || raw[2] != 0x01 || raw[3] != 0x00 {
		return "", errors.New("spade 地址头部格式错误")
	}
	cipherLength := (len(raw) - 4) / 16 * 16
	if cipherLength == 0 {
		return "", errors.New("spade 地址密文缺少完整分组")
	}

	first := sha512.Sum512(seed)
	second := sha512.Sum512(append(first[:], spadeKDFSalt...))
	block, err := aes.NewCipher(second[:16])
	if err != nil {
		return "", errors.New("spade 地址解密失败")
	}

	plain := make([]byte, cipherLength)
	cipher.NewCBCDecrypter(block, second[16:32]).CryptBlocks(plain, raw[4:4+cipherLength])

	if pad := int(plain[len(plain)-1]); pad >= 1 && pad <= 16 && pad <= len(plain) {
		plain = plain[:len(plain)-pad]
	}
	for len(plain) > 0 && plain[len(plain)-1] == 0 {
		plain = plain[:len(plain)-1]
	}
	return string(plain), nil
}

// decodeSpadeBase64 对齐 Node 的 Buffer.from(value, 'base64')：
// 兼容 base64url 字母表（-/ → +/）、忽略非法字符，并允许省略填充。
func decodeSpadeBase64(value string) []byte {
	var b strings.Builder
	b.Grow(len(value))
	for i := range len(value) {
		switch c := value[i]; {
		case c == '-':
			b.WriteByte('+')
		case c == '_':
			b.WriteByte('/')
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			b.WriteByte(c)
		}
	}

	text := b.String()
	switch len(text) % 4 {
	case 1:
		// 孤立的 1 个字符无法构成有效分组，Node 会直接丢弃。
		text = text[:len(text)-1]
	case 2:
		text += "=="
	case 3:
		text += "="
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil
	}
	return raw
}
