package sign

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
)

// TT-Encrypt 是设备注册请求体的封装（"tt-data" 信封）。
//
// 结构：魔数 74 63 05 10 00 00 ‖ 前缀 32B ‖ AES-128-CBC(明文 ‖ PKCS#7)，
// 其中明文 = sha512(data) ‖ data，密钥/IV 由 sha512(sha512(前缀) ‖ 盐) 派生。
//
// 移植自 beilunyang/openhongguo（Apache-2.0）的 clean-room 实现：
// 前缀与盐都是算法常量，不是设备凭据。
var (
	ttEncryptSalt = mustHex("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")
	ttEncryptKey  = mustHex("3176c9b6d71579d7e82179c9e7cbde787b54d541bad3b15f2e8fed92293fa25a")
)

// TTEncrypt 把输入封进 tt-data 信封。
func TTEncrypt(input []byte) []byte {
	prefixHash := sha512.Sum512(ttEncryptKey)
	keyIV := sha512.Sum512(append(prefixHash[:], ttEncryptSalt...))
	digest := sha512.Sum512(input)

	plain := make([]byte, 0, len(digest)+len(input)+aes.BlockSize)
	plain = append(plain, digest[:]...)
	plain = append(plain, input...)
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	for i := 0; i < padding; i++ {
		plain = append(plain, byte(padding))
	}

	block, err := aes.NewCipher(keyIV[:16])
	if err != nil {
		return nil
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, keyIV[16:32]).CryptBlocks(encrypted, plain)

	out := make([]byte, 0, 6+len(ttEncryptKey)+len(encrypted))
	out = append(out, 0x74, 0x63, 0x05, 0x10, 0x00, 0x00)
	out = append(out, ttEncryptKey...)
	out = append(out, encrypted...)
	return out
}
