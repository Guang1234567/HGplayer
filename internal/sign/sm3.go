package sign

// SM3 (GB/T 32905) 国密哈希，纯 Go 实现。
//
// 移植自 hongguo-open-player 的 sm3.js（GPL-3.0-only）。原实现为纯 JS，
// 因 Electron 内置 BoringSSL 无 SM3；Go 同样自实现以保证行为一致。

// sm3Digest 返回输入的 SM3 摘要（32 字节）。
func sm3Digest(input []byte) []byte {
	// 补齐：0x80 + 长度（bit 数，大端 64 位），按 64 字节分组。
	padded := make([]byte, ((len(input)+9+63)/64)*64)
	copy(padded, input)
	padded[len(input)] = 0x80
	// 长度写入末尾 8 字节（bit 数，大端）。
	var bitLen [8]byte
	// input 长度上限用 uint64；JS 用 BigInt，此处按长度 *8 计算。
	l := uint64(len(input)) * 8
	for i := 0; i < 8; i++ {
		bitLen[7-i] = byte(l >> (8 * i))
	}
	copy(padded[len(padded)-8:], bitLen[:])

	// 初始状态。
	v := [8]uint32{
		0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600,
		0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e,
	}

	var words [68]uint32
	for offset := 0; offset < len(padded); offset += 64 {
		for i := 0; i < 16; i++ {
			words[i] = (uint32(padded[offset+i*4]) << 24) |
				(uint32(padded[offset+i*4+1]) << 16) |
				(uint32(padded[offset+i*4+2]) << 8) |
				uint32(padded[offset+i*4+3])
		}
		for i := 16; i < 68; i++ {
			x := words[i-16] ^ words[i-9] ^ rotl32(words[i-3], 15)
			words[i] = x ^ rotl32(x, 15) ^ rotl32(x, 23) ^ rotl32(words[i-13], 7) ^ words[i-6]
		}

		a, b, c, d, e, f, g, h := v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]
		for i := 0; i < 64; i++ {
			a12 := rotl32(a, 12)
			var t uint32
			if i < 16 {
				t = 0x79cc4519
			} else {
				t = 0x7a879d8a
			}
			s1 := rotl32(a12+e+rotl32(t, uint(i)), 7)
			s2 := s1 ^ a12

			var ff, gg uint32
			if i < 16 {
				ff = a ^ b ^ c
				gg = e ^ f ^ g
			} else {
				ff = (a & b) | (a & c) | (b & c)
				gg = (e & f) | (^e & g)
			}

			t1 := ff + d + s2 + (words[i] ^ words[i+4])
			t2 := gg + h + s1 + words[i]

			d = c
			c = rotl32(b, 9)
			b = a
			a = t1
			h = g
			g = rotl32(f, 19)
			f = e
			e = t2 ^ rotl32(t2, 9) ^ rotl32(t2, 17)
		}

		v[0] ^= a
		v[1] ^= b
		v[2] ^= c
		v[3] ^= d
		v[4] ^= e
		v[5] ^= f
		v[6] ^= g
		v[7] ^= h
	}

	out := make([]byte, 32)
	for i := 0; i < 8; i++ {
		out[i*4] = byte(v[i] >> 24)
		out[i*4+1] = byte(v[i] >> 16)
		out[i*4+2] = byte(v[i] >> 8)
		out[i*4+3] = byte(v[i])
	}
	return out
}
