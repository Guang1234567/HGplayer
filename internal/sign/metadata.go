package sign

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// —— 元数据请求（目录 / 详情 / 搜索）的签名能力 ——
//
// 与播放请求不同，元数据请求的 x-ladon / x-argus 只是时间戳的 base64 编码，
// 真正的签名由 x-gorgon / x-helios / x-medusa 承担。

// Gorgon 生成 X-Gorgon 头（小写 hex）。
func Gorgon(query string, body []byte, timestamp, random uint32) string {
	return xGorgon(query, body, timestamp, random)
}

// Helios 生成 X-Helios 头（base64），内部使用密码学随机数。
func Helios(khronos uint32) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return heliosWithRandom(khronos, 0)
	}
	return heliosWithRandom(khronos, binary.LittleEndian.Uint32(b[:]))
}

// Base64LE32 生成小端 uint32 的 base64（用于 x-argus）。
func Base64LE32(v uint32) string {
	return base64.StdEncoding.EncodeToString(le32(v))
}

// Base64BE32 生成大端 uint32 的 base64（用于 x-ladon）。
func Base64BE32(v uint32) string {
	return base64.StdEncoding.EncodeToString(be32(v))
}

// MD5Upper 返回大写十六进制 MD5（用于 x-ss-stub）。
func MD5Upper(data []byte) string {
	return md5HexUpper(data)
}

// RandomUint16 返回 [0,65536) 的随机数，用于 X-Gorgon 的随机字段。
func RandomUint16() uint32 {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return uint32(binary.BigEndian.Uint16(b[:]))
}

// BranchOf 计算请求的签名分支。
//
// 元数据请求必须使该值为 0，否则服务端会拒绝签名；调用方通过递增
// _rticket 重试直到分支为 0（与源实现一致）。
func BranchOf(rawURL string, body []byte, khronos uint32) int {
	var bodyMd5 []byte
	if body != nil {
		bodyMd5 = md5Digest(body)
	} else {
		bodyMd5 = make([]byte, 16)
	}

	query := rawURL
	if i := strings.IndexByte(query, '?'); i >= 0 {
		query = query[i+1:]
	}

	q := sm3Digest([]byte(query))
	iv := getIV(getIV(getIV(0x20230928, q), bodyMd5), le32(khronos))

	low := int(iv & 15)
	return low - ((low*171)>>9)*3
}

// getIV 是源实现中的 getIv：按字节逐位混淆。
func getIV(iv uint32, data []byte) uint32 {
	value := iv
	for i := 0; i < len(data); i++ {
		if i&1 == 0 {
			value = (value >> 4) ^ value ^ (value << 6) ^ uint32(data[i])
		} else {
			value = ^((value >> 7) ^ value ^ (uint32(data[i]) | (value << 12)))
		}
	}
	return value
}
