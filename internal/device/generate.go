package device

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
)

// Generate 生成一个匿名设备身份。
//
// 只生成本地随机的数字标识，不模仿任何具体机型或真机指纹。
// 用于首次启动时建立本地身份；若上游要求注册，再由注册流程替换。
func Generate() Info {
	return Info{
		DeviceID: randomDigits(16),
		IID:      randomDigits(16),
		CDID:     randomUUID(),
	}
}

// randomDigits 生成 n 位数字串（首位非 0）。
func randomDigits(n int) string {
	out := make([]byte, n)
	for i := range n {
		// 首位取 1-9，其余取 0-9。
		lo := int64(0)
		if i == 0 {
			lo = 1
		}
		v, err := rand.Int(rand.Reader, big.NewInt(10-lo))
		if err != nil {
			out[i] = byte('0' + lo)
			continue
		}
		out[i] = byte('0' + lo + v.Int64())
	}
	return string(out)
}

// randomUUID 生成 v4 风格 UUID 字符串。
func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
