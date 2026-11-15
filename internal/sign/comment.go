package sign

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"sort"
	"strconv"
	"strings"
)

// —— 弹幕签名变体（独立密钥与字段结构，移植自 danmaku-sign.js）——

const commentArgusKeyHex = "ac1adaae95a7af94a5114ab3b3a97dd80050aa0a39314c40528caec95256c28c"

var commentArgusKey = mustHex(commentArgusKeyHex)

// nonceBytes 是弹幕签名的 nonce 长度。
const nonceBytes = 11

// —— X-Gorgon（弹幕变体）——

// commentXGorgon 带 nonce 变体的 X-Gorgon。
//
// 注意 box[i]=box[j] 是赋值而非交换：照抄参考实现。
func commentXGorgon(rawQuery string, stamp uint64, nonce []byte) string {
	head2 := uint8(((nonce[0] % 7) + 1) << 5)
	head3 := uint8(0xE0 | (nonce[1] & 0x0F))

	seed := make([]byte, 20)
	digest := md5Digest([]byte(rawQuery))
	copy(seed, digest[:4])
	seed[12], seed[13], seed[14], seed[15] = 0, 1, 7, 4
	binaryPutUint32BE(seed[16:20], uint32(stamp&0xFFFFFFFF))

	key := []byte{0x4A, 0x00, 0x16, head3, 0x47, 0x6C, 0x00, head2}

	box := make([]byte, 256)
	for i := 0; i < 256; i++ {
		box[i] = byte(i)
	}
	var j uint8
	for i := 0; i < 256; i++ {
		j = (j + box[i] + key[i&7]) & 0xFF
		box[i] = box[j]
	}

	j = 0
	for offset := 0; offset < 20; offset++ {
		i := (offset + 1) & 0xFF
		j = (j + box[i]) & 0xFF
		box[i] = box[j]
		seed[offset] ^= box[(int(box[i])+int(box[j]))&0xFF]
	}

	for i := 0; i < 20; i++ {
		seed[i] = (reverse8(rotl8(seed[i], 4)^seed[(i+1)%20]) ^ 0xEB) & 0xFF
	}

	prefix := []byte{0x84, 0x04, head2 & 0xFF, head3 & 0xFF, 0x00, 0x00}
	return hexEncode(concat([][]byte{prefix, seed}))
}

func binaryPutUint32BE(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

// —— X-Ladon（弹幕变体）——

// commentLadon 34 轮 uint64 密钥扩展 + TEA 风格变换 + base64。
func commentLadon(aid string, timestamp uint64, prefix []byte) string {
	digest := []byte(hexEncode(md5Digest(concat([][]byte{prefix, []byte(aid)}))))

	left := u64le(digest[0:8])
	right := u64le(digest[8:16])
	queue := []uint64{u64le(digest[16:24]), u64le(digest[24:32])}

	keys := make([]uint64, 0, 34)
	for i := 0; i < 34; i++ {
		keys = append(keys, left)
		mixed := ((rotr64(right, 8) + left) ^ uint64(i)) & mask64
		queue = append(queue, mixed)
		left = (mixed ^ rotl64(left, 3)) & mask64
		right = queue[0]
		queue = queue[1:]
	}

	data := pkcs7Pad([]byte(itoa(int64(timestamp)) + "-1611921764-3019"))
	for offset := 0; offset < len(data); offset += 16 {
		bl := u64le(data[offset : offset+8])
		br := u64le(data[offset+8 : offset+16])
		for _, key := range keys {
			br = (key ^ (bl + rotr64(br, 8))) & mask64
			bl = (br ^ rotl64(bl, 3)) & mask64
		}
		putU64le(bl, data, offset)
		putU64le(br, data, offset+8)
	}

	return base64.StdEncoding.EncodeToString(concat([][]byte{prefix, data}))
}

// —— X-Argus（弹幕变体）——

// commentArgus 手写 protobuf 组包 + SM3 派生 + 72 轮变换 + AES-128-CBC。
func commentArgus(rawQuery string, timestamp uint64, nonce []byte) string {
	var parts [][]byte

	vfield := func(field uint64, value uint64) {
		parts = append(parts, varint(field<<3), varint(value))
	}
	bfield := func(field uint64, value []byte) {
		parts = append(parts, varint((field<<3)|2), varint(uint64(len(value))), value)
	}

	vfield(1, 0x20200929*2)
	vfield(2, 2)
	vfield(3, uint64(uint32(nonce[6])|uint32(nonce[7])<<8|uint32(nonce[8])<<16|uint32(nonce[9])<<24))
	bfield(4, []byte("3019"))
	bfield(6, []byte("1611921764"))
	bfield(7, []byte("6.8.1.32"))
	bfield(8, []byte("v04.07.01-ml-android"))
	vfield(9, 135135744)
	bfield(10, make([]byte, 8))
	vfield(12, (timestamp*2)&mask64)

	bodyHash := sm3Digest(make([]byte, 16))
	queryHash := sm3Digest([]byte(rawQuery))
	bfield(13, bodyHash[:6])
	bfield(14, queryHash[:6])

	nested := []byte{0x08, byte((2 + (nonce[10]%49)*2) & 0xFF)}
	for _, field := range []uint64{2, 3, 4} {
		nested = append(nested, varint(field<<3)...)
		nested = append(nested, varint(1388734)...)
	}
	bfield(15, nested)
	bfield(20, []byte("none"))
	vfield(21, 738)

	data := pkcs7Pad(concat(parts))

	derived := sm3Digest(concat([][]byte{commentArgusKey, []byte{0x3C, 0xCC, 0x56, 0x7B}, commentArgusKey}))

	rounds := make([]uint64, 72)
	for i := 0; i < 4; i++ {
		rounds[i] = u64le(derived[i*8 : i*8+8])
	}
	for i := 4; i < 72; i++ {
		mixed := (rotr64(rounds[i-1], 3) ^ rounds[i-3]) & mask64
		mixed = (mixed ^ rotr64(mixed, 1)) & mask64
		rounds[i] = ((^rounds[i-4] & mask64) ^ mixed ^
			((zConstant >> uint((i-4)%62)) & 1) ^ 3) & mask64
	}

	for offset := 0; offset < len(data); offset += 16 {
		left := u64le(data[offset : offset+8])
		right := u64le(data[offset+8 : offset+16])
		for _, round := range rounds {
			next := (left ^ (rotl64(right, 1) & rotl64(right, 8)) ^ rotl64(right, 2) ^ round) & mask64
			left = right
			right = next
		}
		putU64le(left, data, offset)
		putU64le(right, data, offset+8)
	}

	xor := []byte{0xD0, 0x4F, 0xFD, 0xFF, 0xD0, 0x4F, 0xFD, 0xFF}
	for i := range data {
		data[i] ^= xor[i%8]
	}
	reversed := reverseCopy(concat([][]byte{xor, data}))

	container := pkcs7Pad(concat([][]byte{
		[]byte{0xA6, 0xE7, 0x83, 0xEE, 0x70, 0x01, 0x10, 0x09, 0x18},
		reversed,
		{0x56, 0x7B},
	}))

	aesBlock, err := aes.NewCipher(md5Digest(commentArgusKey[0:16]))
	if err != nil {
		return ""
	}
	mode := cipher.NewCBCEncrypter(aesBlock, md5Digest(commentArgusKey[16:32]))
	encrypted := make([]byte, len(container))
	mode.CryptBlocks(encrypted, container)

	result := concat([][]byte{{0x3C, 0xCC}, encrypted})
	return base64.StdEncoding.EncodeToString(result)
}

// —— 头部组装 ——

// commentHeaders 产出弹幕接口的签名头部。
func commentHeaders(rawQuery string, stamp uint64, nonce []byte, aid string) map[string]string {
	trunc := stamp
	return map[string]string{
		"X-Gorgon":        commentXGorgon(rawQuery, trunc, nonce),
		"X-Khronos":       strconv.FormatUint(trunc, 10),
		"X-SS-Req-Ticket": strconv.FormatUint(trunc*1000, 10),
		"X-SS-STUB":       "",
		"X-Argus":         commentArgus(rawQuery, trunc, nonce),
		"X-Ladon":         commentLadon(aid, trunc, nonce[2:6]),
	}
}

// —— query 构造 ——

// baseParams 弹幕请求的固定 query 参数。
func baseParams(deviceID, installID string) map[string]string {
	p := map[string]string{
		"aid":                   "8662",
		"app_name":              "novelread",
		"version_code":          "73532",
		"version_name":          "7.3.5.32",
		"manifest_version_code": "73532",
		"update_version_code":   "73532",
		"channel":               "update_64",
		"device_platform":       "android",
		"os":                    "android",
		"ssmix":                 "a",
		"device_type":           "25053RT47C",
		"device_brand":          "Redmi",
		"language":              "zh",
		"os_api":                "36",
		"os_version":            "16",
		"resolution":            "1280*2772",
		"dpi":                   "520",
		"ac":                    "wifi",
		"device_id":             deviceID,
		"iid":                   installID,
	}
	return p
}

// quotePlus 对齐 Python urlencode（quote_plus）。
func quotePlus(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		const hex = "0123456789ABCDEF"
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0xF])
	}
	// 空格用 +
	return strings.ReplaceAll(b.String(), "%20", "+")
}

// buildQuery 键按字典序排序后编码。
func buildQuery(stampMs uint64, deviceID, installID string) string {
	params := baseParams(deviceID, installID)
	params["_rticket"] = itoa(int64(stampMs))

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, quotePlus(k)+"="+quotePlus(params[k]))
	}
	return strings.Join(pairs, "&")
}

// newDeviceID 生成 19 位数字设备 ID（1e18 + 随机数）。
func newDeviceID(random []byte) string {
	raw := u64le(random[:8])
	return itoa(int64(1000000000000000000 + (raw % 8000000000000000000)))
}

// randomNonce 生成 11 字节 nonce。
func randomNonce() []byte {
	n := make([]byte, nonceBytes)
	_, _ = rand.Read(n)
	return n
}

// reverseCopy 返回反转后的副本（不改原切片）。
func reverseCopy(b []byte) []byte {
	out := make([]byte, len(b))
	for i, j := 0, len(b)-1; i < len(b); i, j = i+1, j-1 {
		out[i] = b[j]
	}
	return out
}
