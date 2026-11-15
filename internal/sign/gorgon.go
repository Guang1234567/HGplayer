package sign

import "encoding/base64"

// —— X-Gorgon（主实现）——

// rc4Gorgon RC4 变体。移植自 hongguo-open-player app-source.js。
func rc4Gorgon(data, key []byte) []byte {
	s := make([]byte, 256)
	for i := 0; i < 256; i++ {
		s[i] = byte(i)
	}
	var j byte
	for i := 0; i < 256; i++ {
		j = (j + s[i] + key[i%len(key)]) & 0xFF
		s[i] = s[j]
	}

	var i, j2 byte
	out := make([]byte, len(data))
	for n := 0; n < len(data); n++ {
		i++
		x := s[i]
		j2 += x
		y := s[j2]
		s[i] = y
		out[n] = data[n] ^ s[(int(y)+int(y))&0xFF]
	}
	return out
}

// xGorgon 生成 X-Gorgon 头值（hex）。
func xGorgon(query string, body []byte, timestamp, random uint32) string {
	sdk := le32(67503104)

	md5Query := md5Digest([]byte(query))[:4]
	var md5Body []byte
	if body != nil {
		md5Body = md5Digest(body)[:4]
	} else {
		md5Body = make([]byte, 4)
	}

	input := concat([][]byte{
		md5Query,
		md5Body,
		make([]byte, 4),
		sdk,
		be32(timestamp),
	})

	key := []byte{
		0x4a, 0x40, 0x16, byte((random >> 8) & 0xFF),
		0x47, 0x6c, 0x01, byte(random & 0xFF),
	}
	out := rc4Gorgon(input, key)

	for i := range out {
		value := out[i]
		value = ((value >> 4) | (value << 4)) & 0xFF
		var next byte
		if i+1 < len(out) {
			next = out[i+1]
		} else {
			next = out[0]
		}
		out[i] = (^(reverseBits((next^value)&0xFF) ^ 20)) & 0xFF
	}

	prefix := concat([][]byte{
		[]byte{0x84, 0x04},
		{byte(random & 0xFF), byte((random >> 8) & 0xFF), 0x40, 0x01},
	})
	return hexEncode(concat([][]byte{prefix, out}))
}

// —— X-Helios ——

// heliosWithRandom 用指定随机数生成 X-Helios。
func heliosWithRandom(khronos, random uint32) string {
	data := concat([][]byte{le32(random), []byte("8662")})
	digest := md5Digest(data)
	ascii := []byte(hexEncode(digest))

	var words []uint64
	for i := 0; i < 4; i++ {
		words = append(words, u64le(ascii[i*8:i*8+8]))
	}

	table := []uint64{words[0]}
	b0 := words[0]
	b8 := words[1]
	words = words[2:]

	for i := 0; i < 34; i++ {
		x8 := (rotr64(b8, 8) + b0) & mask64
		x8 ^= uint64(i)
		words = append(words, x8)
		x8 = (x8 ^ rotr64(b0, 61)) & mask64
		table = append(table, x8)
		b0 = x8
		b8 = words[0]
		words = words[1:]
	}

	text := []byte(itoa(int64(khronos)) + "-1588093228-8662")
	padLen := (len(text) + 1 + 15) / 16 * 16
	pad := make([]byte, padLen)
	padVal := byte(16 - (len(text) % 16))
	for i := range pad {
		pad[i] = padVal
	}
	copy(pad, text)

	var output [][]byte
	for at := 0; at < len(pad); at += 16 {
		a := u64le(pad[at : at+8])
		b := u64le(pad[at+8 : at+16])
		for i := 0; i < 34; i++ {
			b = (table[i] ^ (a + rotr64(b, 8))) & mask64
			a = (b ^ rotr64(a, 61)) & mask64
		}
		block := make([]byte, 16)
		binaryPutUint64LE(block[0:8], a)
		binaryPutUint64LE(block[8:16], b)
		output = append(output, block)
	}

	result := concat(append([][]byte{le32(random)}, output...))
	return base64.StdEncoding.EncodeToString(result)
}

// binaryPutUint64LE 写入小端 64 位（避免在多个文件重复 import）。
func binaryPutUint64LE(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * uint(i)))
	}
}
