// Package sign 实现上游请求签名算法（内部加密算法）。
//
// 算法移植自开源项目 hongguo-open-player（GPL-3.0-only），其算法部分
// 又源自 dawei233/hongguo-drama、N3urda/hongguoTV、woshishiq1/drpys
// 等 GPL-3.0 项目，均为用户工作区内、允许使用的逆向/开源来源。
//
// 本包位于 internal/ 下，Go 语言层面禁止外部导入，作为内部算法的
// 隔离边界。
package sign

import "encoding/binary"

// —— 位运算辅助 ——

// u32 归一化到 uint32。
func u32(v uint64) uint32 { return uint32(v) }

// rol32 循环左移 32 位。
func rol32(value uint32, count uint) uint32 {
	n := count & 31
	return (value << n) | (value >> (32 - n))
}

// rotl32 循环左移 32 位（与 rol32 同义，对齐源算法命名）。
func rotl32(value uint32, count uint) uint32 {
	return rol32(value, count)
}

// ror32 循环右移 32 位。
func ror32(value uint32, count uint) uint32 {
	n := count & 31
	return (value >> n) | (value << (32 - n))
}

// rotl64 循环左移 64 位。
//
// Go 的 uint64 左移会立即截断高位，而源实现用 BigInt 保留 64 位外
// 的位再整体掩码。需对左移结果单独掩码，否则高位在组合前就丢失。
func rotl64(x, n uint64) uint64 {
	s := n & 63
	return ((x << s) & mask64) | (x >> (64 - s))
}

// rotr64 循环右移 64 位。同理，左移部分需先掩码。
func rotr64(x, n uint64) uint64 {
	s := n & 63
	return (x >> s) | ((x << (64 - s)) & mask64)
}

// mask64 全 64 位掩码。
const mask64 = ^uint64(0)

// rotl8 循环左移 8 位。
func rotl8(x, n uint8) uint8 {
	s := n & 7
	return (x << s) | (x >> (8 - s))
}

// reverse8 位反转。
func reverse8(x uint8) uint8 {
	v := x
	v = ((v & 0xF0) >> 4) | ((v & 0x0F) << 4)
	v = ((v & 0xCC) >> 2) | ((v & 0x33) << 2)
	v = ((v & 0xAA) >> 1) | ((v & 0x55) << 1)
	return v & 0xFF
}

// reverseBits 反转 8 位中的比特序。
func reverseBits(value uint8) uint8 {
	var result uint8
	for i := 0; i < 8; i++ {
		result = (result << 1) | ((value >> i) & 1)
	}
	return result
}

// le32 小端 32 位字节。
func le32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

// be32 大端 32 位字节。
func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// zigzag zigzag 编码（protobuf sint）。
func zigzag(v int64) uint64 {
	if v < 0 {
		return uint64(uint64(-v)*2 - 1)
	}
	return uint64(v) * 2
}

// varint protobuf 变长无符号整数。
func varint(v uint64) []byte {
	var out []byte
	for v > 127 {
		out = append(out, byte(v&127)|128)
		v >>= 7
	}
	out = append(out, byte(v))
	return out
}

// u64le 从字节切片读取小端 64 位。
func u64le(b []byte) uint64 {
	return binary.LittleEndian.Uint64(b[:8])
}

// putU64le 向字节切片写入小端 64 位。
func putU64le(value uint64, target []byte, offset int) {
	binary.LittleEndian.PutUint64(target[offset:offset+8], value)
}

// xorBytes 按 key 循环异或。
func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := 0; i < len(out); i++ {
		out[i] = a[i] ^ b[i%len(b)]
	}
	return out
}

// bxor 取两者较短长度逐字节异或。
func bxor(a, b []byte) []byte {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = a[i] ^ b[i]
	}
	return out
}
