package sign

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"net/url"
	"strconv"
)

// 取址接口（/novel/player/video_model/v1/）自 2026-11-10 起不再接受
// x-argus / x-ladon 的 4 字节时间戳桩：匿名设备会被回以 HTTP 200 + 空响应体；
// 且请求若同时携带 x-medusa，即使 551 信封正确也依然空响应。
// 搜索、目录、详情等读接口仍接受桩，所以只有取址路径用这里的真实签名。
//
//   - X-Argus 551：protobuf bean → PKCS#7 → Simon 64/128 → 掩码 XOR + 整体倒序
//     → AES-128-CBC（标准库 AES），前缀 0xf2 0x81 后 base64。
//   - X-Ladon：md5(nonce ‖ aid) 派生 34 轮 ARX 密钥，明文 "<ts>-<licenseID>-<aid>"，
//     明文前附小端 nonce 后 base64。
//
// 移植自 beilunyang/openhongguo（Apache-2.0）的独立 clean-room 实现；
// 逐位对拍向量见 playback551_test.go（其中一组来自本项目上游
// zfdang/hongguo-open-player PR #92，另一组来自 aston314/hongguo-tv）。
// 旧版 x-argus / x-ladon（argus.go）保持不变：其余接口仍需要它们。

// playback551LicenseID 是取址接口使用的 license id。
const playback551LicenseID = "1611921764"

// Argus 551 的公开常量：sign key 参与 Simon 轮密钥扩展，AES key/IV 加密最终帧。
var (
	argus551SignKey = mustHex("fc78e0a9657a0c748ce51559903ccf03510e51d3cff232d71343e88a321c5304")
	argus551AESKey  = mustHex("d31e3718288a1027baab59f146a09a9c")
	argus551AESIV   = mustHex("ea180a0336ed352fcd24e4d50018ae54")
	argus551Mask    = []byte{0xf2, 0xf7, 0xfc, 0xff, 0xf2, 0xf7, 0xfc, 0xff}
	argus551Tail    = []byte{0xea, 0xfb, 0x2c, 0xfe, 0x85, 0x68, 0x51, 0x91, 0x54}
)

// Argus551 生成取址请求的 x-argus 头。
//
// query 是最终发出的查询串（含 ts 与 _rticket）；body 是请求体原文
// （无请求体传 nil，此时按 16 个 0 处理，与上游一致）；nonce 每次请求重新随机。
func Argus551(body []byte, query string, ts, nonce uint32, aid, deviceID, installID string) string {
	bodyMD5 := make([]byte, 16)
	if len(body) > 0 {
		bodyMD5 = md5Digest(body)
	}
	bodyHash := sm3Digest(bodyMD5)
	queryBytes := []byte(query)
	if query == "" {
		queryBytes = make([]byte, 16)
	}
	queryHash := sm3Digest(queryBytes)

	counters := concat([][]byte{
		pb551Varint(1, 1), pb551Varint(2, 1), pb551Varint(3, 1), pb551Varint(7, 0xc792eccc),
	})
	device := concat([][]byte{
		pb551String(1, "NX551J"), pb551Varint(2, 8196), pb551Varint(4, 0x80e0d800),
	})
	bean := concat([][]byte{
		pb551Varint(1, 1077940818),
		pb551Varint(2, 2),
		pb551Varint(3, uint64(nonce)),
		pb551String(4, aid),
		pb551String(5, deviceID),
		pb551String(6, playback551LicenseID),
		pb551String(7, queryValue(query, "version_name")),
		pb551String(8, "v04.04.05-ov-android"),
		pb551Varint(9, 134744640),
		pb551Bytes(10, make([]byte, 8)),
		pb551Varint(11, 0),
		pb551Varint(12, uint64(ts)<<1),
		pb551Bytes(13, bodyHash[:6]),
		pb551Bytes(14, queryHash[:6]),
		pb551Bytes(15, counters),
		pb551String(16, installID),
		pb551String(20, "none"),
		pb551Varint(21, 738),
		pb551Bytes(23, device),
		pb551Varint(25, 2),
	})

	plain := pkcs7Pad(bean)
	simon551Encrypt(plain)

	frame := concat([][]byte{argus551Mask, plain, argus551Tail})
	for i := len(argus551Mask); i < len(frame); i++ {
		frame[i] ^= argus551Mask[i%len(argus551Mask)]
	}
	for i, j := 0, len(frame)-1; i < j; i, j = i+1, j-1 {
		frame[i], frame[j] = frame[j], frame[i]
	}
	frame = pkcs7Pad(append(frame, 0x61, 0x6f))

	block, err := aes.NewCipher(argus551AESKey)
	if err != nil {
		return ""
	}
	cipher.NewCBCEncrypter(block, argus551AESIV).CryptBlocks(frame, frame)
	return base64.StdEncoding.EncodeToString(append([]byte{0xf2, 0x81}, frame...))
}

// Ladon551 生成取址请求的 x-ladon 头。
func Ladon551(ts, nonce uint32, aid string) string {
	seed := le32(nonce)
	digest := md5Digest(append(append([]byte{}, seed...), aid...))
	hexKey := []byte(hex.EncodeToString(digest))

	keys := make([]uint64, 34)
	k := binary.LittleEndian.Uint64(hexKey)
	pending := [3]uint64{
		binary.LittleEndian.Uint64(hexKey[8:]),
		binary.LittleEndian.Uint64(hexKey[16:]),
		binary.LittleEndian.Uint64(hexKey[24:]),
	}
	for i := range keys {
		keys[i] = k
		x := (rotr64(pending[0], 8) + k) ^ uint64(i)
		pending = [3]uint64{pending[1], pending[2], x}
		k = x ^ rotl64(k, 3)
	}

	plain := pkcs7Pad([]byte(strconv.FormatUint(uint64(ts), 10) + "-" + playback551LicenseID + "-" + aid))
	for at := 0; at < len(plain); at += 16 {
		a := binary.LittleEndian.Uint64(plain[at:])
		b := binary.LittleEndian.Uint64(plain[at+8:])
		for _, roundKey := range keys {
			b = roundKey ^ (a + rotr64(b, 8))
			a = b ^ rotl64(a, 3)
		}
		binary.LittleEndian.PutUint64(plain[at:], a)
		binary.LittleEndian.PutUint64(plain[at+8:], b)
	}
	return base64.StdEncoding.EncodeToString(append(seed, plain...))
}

// RandomUint32 返回 [0, 2^32) 的随机数，用于 551 信封的随机字段。
func RandomUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b[:])
}

// simon551Encrypt 用 Simon 64/128 就地加密每个 16 字节块。
func simon551Encrypt(plain []byte) {
	keys := simon551KeySchedule()
	for at := 0; at < len(plain); at += 16 {
		a := binary.LittleEndian.Uint64(plain[at:])
		b := binary.LittleEndian.Uint64(plain[at+8:])
		for _, roundKey := range keys {
			a, b = b, a^(rotl64(b, 1)&rotl64(b, 8))^rotl64(b, 2)^roundKey
		}
		binary.LittleEndian.PutUint64(plain[at:], a)
		binary.LittleEndian.PutUint64(plain[at+8:], b)
	}
}

// simon551KeySchedule 把 256 位 sign key 扩展成 72 个 Simon 64/128 轮密钥。
func simon551KeySchedule() [72]uint64 {
	var k [72]uint64
	for i := 0; i < 4; i++ {
		k[i] = binary.LittleEndian.Uint64(argus551SignKey[i*8:])
	}
	const z uint64 = 0x3dc94c3a046d678b
	for i := 4; i < len(k); i++ {
		x := rotr64(k[i-1], 3) ^ k[i-3]
		x ^= rotr64(x, 1)
		k[i] = ^k[i-4] ^ x ^ ((z >> uint((i-4)%62)) & 1) ^ 3
	}
	return k
}

// pb551Varint 编码一个 wire type 0 的 protobuf 字段（不做 zig-zag）。
func pb551Varint(tag uint32, value uint64) []byte {
	return append(pb551Key(tag, 0), varint(value)...)
}

// pb551Bytes 编码一个 length-delimited 字段；空 body 也必须发出。
func pb551Bytes(tag uint32, body []byte) []byte {
	out := append(pb551Key(tag, 2), varint(uint64(len(body)))...)
	return append(out, body...)
}

// pb551String 编码一个字符串字段。
func pb551String(tag uint32, s string) []byte {
	return pb551Bytes(tag, []byte(s))
}

// pb551Key 编码 protobuf 的 tag+wire type。
func pb551Key(tag uint32, wire uint64) []byte {
	return varint(uint64(tag)<<3 | wire)
}

// queryValue 从查询串中取一个参数值。
func queryValue(query, name string) string {
	values, err := url.ParseQuery(query)
	if err != nil {
		return ""
	}
	return values.Get(name)
}
