package sign

import (
	"encoding/base64"
	"encoding/binary"
)

// AesV3 是源实现中的自研 AES 变体，用于加密 x-medusa 的报文。
//
// 移植自 hongguo-open-player/src/server/native/app-source.js（GPL-3.0-only）。

// aesV3SBox 是 SBOX（1024 字节，按 wordSize 分成 4 段各 256 字节）。
const aesV3SBoxB64 = "+n0Ia5xZs0sEXznQOEqRmQBnpiCf9U2Ccybu3xhmgzOAAxn72f6uqqmwUsYL83klTni0NqxdGieeiNu9PGPsSRXBMB/cuFbUbM3KCUPINaPvHvSW0vwOcnuUhNHqRVpiAj/TEoE0K91+5ijypUYTATsh9mE3KSoN7Yyvv51cuyR2D3XkU4nhmI2xmmVwT1RMWKtub4sjxAcRDLrPoKSO2AU9FLLadMPX577Wf95IFj6FkKFVt3dCIsmGUC4X+WQxLJvxbRxEaOPpqJOXyzJX6+Vxaq3AzMfF/WAdoi1Hp+JRaV56zgpBtpWP97mH4DoGEIq1+FvV8LyS/3wvwugbQOwb2r26mJEMsiuDQTRn+wrYdrVGBVlhI3WQhyrjUBVMrLF5667llUcEaPCGPVGLD8qO5LlO8hKCvA7V9+8oJc9bXelqVQLhM76T5/WtnT45JKji+hdX0HoNCDDWuKON/QeaxB5uImSX0h2wv0VmP2zd2yeApxHcpsVS+MC2yFwAc2B7oBkTqsk1SEvTpM2fmfMQREBUfin0Bh+iq6EvPPavhWI2IX9e3yAas7Tm/3KEj2UmlFp36kN4x0rMLBRrxuh0U/zUHM4xcAMYjJY4MonxOl/X+alpt2M3WMI7w3HLnpIBigtNiJu7T21v4P6lSd5WFgntnMEt7oF92XzRLkJbTcGmXepE/UVOG6E/0YnhfS+q26utWcuxzpooyeD2cDlK1/8w9d28VzsRjbLuALbmGlp8+d7EzS6Au7lMpZ+ECMZvQmzwJ+eLOpxR+2chdUExp8ogQyq3v9l68rX4jCwjg0+PYKAEEzcU4wHFY2ZcdIHfWL1okD3SszT0GZMyKdZJrg1L2AeerB4tC0C4crp2EHGo5FYdSP7lwkeR2ocmnR+Ia8CYviUJlzOjhRZef9xuVOn3qcjow3fQgivsAmKKkg4+sA8F8/GWeDiGNhg8JM8KtFPMYWWkx5TVFX5t73kiNRJqjlIGVXtGZFCV4gzt0xcDopuZ6xz8r9Rzafpf9ywev8jh8592gHFIqpStZPuJxmDDMrNN0uBE3V+oscdoIzTJbRJ/t+sVvqnReJOgDJKk10fjisJwqyZBmnmn2BSFj8BvVtCMEbkuPOKdzw7eA11GPs04Qw8zWtkaZWwiO/wwpojqN6K0jY5RnNZA7vn4hPSul+nKCkVnVwQvg1zVxcSCtqORmB9KrJaBbssbCQivGJVJfVTt+hYxOtq4ZvWl8f4QAQZ0zGPffCgl9s6yT4vlvIdpu4YhBwA25wtQWZsc6GJYGWHyvSdeuh3mmUI9DSq13Fsp8C1MU3tqc04/df9LoTUXVXI5INOw/e8C7Hd+5CvbkMEFnnrUUmskEw=="

var aesV3SBox = mustBase64(aesV3SBoxB64)

// mustBase64 解析 base64（标准编码）。
func mustBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

type aesV3 struct {
	wordSize int
	box      []byte
	con      [4]int
	con2     [4]int
	order    [16]int
	keys     [12][4]byte
}

// conTable / con2Table / orderTable 对应源实现中的三维常量表。
var (
	conTable = [4][4]int{
		{1, 0, 2, 3}, {1, 3, 0, 2}, {0, 1, 3, 2}, {1, 0, 2, 3},
	}
	con2Table = [4][4]int{
		{1, 0, 2, 3}, {2, 0, 3, 1}, {0, 1, 3, 2}, {1, 0, 2, 3},
	}
	orderTable = [4][16]int{
		{0, 9, 14, 11, 4, 13, 2, 7, 8, 1, 6, 15, 12, 5, 10, 3},
		{0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11},
		{0, 9, 14, 7, 4, 13, 2, 11, 8, 1, 6, 3, 12, 5, 10, 15},
		{0, 9, 14, 11, 4, 13, 2, 7, 8, 1, 6, 15, 12, 5, 10, 3},
	}
	initTable = [4]uint32{0xca025ddc, 0x823dc546, 0xc9420583, 0xc298225f}
)

// newAesV3 构造 AesV3 实例（key 为 16 字节）。
func newAesV3(key []byte, khronos uint32) *aesV3 {
	ws := int(khronos & 3)
	a := &aesV3{
		wordSize: ws,
		box:      aesV3SBox[ws*256 : (ws+1)*256],
		con:      conTable[ws],
		con2:     con2Table[ws],
		order:    orderTable[ws],
	}

	init := initTable[ws]
	initial := make([]byte, 16)
	binary.LittleEndian.PutUint32(initial[0:], init)
	binary.LittleEndian.PutUint32(initial[4:], init)
	binary.LittleEndian.PutUint32(initial[8:], init)
	binary.LittleEndian.PutUint32(initial[12:], init)
	// 源实现：for i<4 initial.writeUInt32LE(init, i*4) —— 同一个 init 写 4 次。
	mk := xorBytes(initial, key)
	expanded := make([]byte, 48)
	copy(expanded, mk)

	rounds := 8
	for i := 4; i < 12; i++ {
		at := 4 * (i - 1)
		a0, b0, c0, d0 := expanded[at], expanded[at+1], expanded[at+2], expanded[at+3]
		if i&3 == 0 {
			t := byte((init >> uint(rounds&24)) ^ uint32(a.box[b0]))
			b0 = a.box[c0]
			c0 = a.box[d0]
			d0 = a.box[a0]
			a0 = t
		}
		rounds += 2
		expanded[at+4] = a0 ^ expanded[at-12]
		expanded[at+5] = b0 ^ expanded[at-11]
		expanded[at+6] = c0 ^ expanded[at-10]
		expanded[at+7] = d0 ^ expanded[at-9]
	}

	for i := range 12 {
		copy(a.keys[i][:], expanded[i*4:i*4+4])
	}
	return a
}

// matrix 把 16 字节按行切成 4x4。
func matrix(bytes []byte) [4][4]byte {
	var rows [4][4]byte
	for i := range 4 {
		copy(rows[i][:], bytes[i*4:i*4+4])
	}
	return rows
}

// flat 把 4x4 展平为 16 字节。
func flat(rows [4][4]byte) []byte {
	out := make([]byte, 16)
	for i := range 4 {
		copy(out[i*4:], rows[i][:])
	}
	return out
}

// aesMix 是 AesV3 的列混合。
func aesMix(rows *[4][4]byte) {
	mix := func(x byte) byte {
		if x&128 != 0 {
			return (x << 1) ^ 0x1b
		}
		return x << 1
	}
	for i := range 4 {
		t := rows[0][i] ^ rows[1][i] ^ rows[2][i] ^ rows[3][i]
		u := rows[0][i]
		rows[0][i] ^= t ^ mix(rows[0][i]^rows[1][i])
		rows[1][i] ^= t ^ mix(rows[1][i]^rows[2][i])
		rows[2][i] ^= t ^ mix(rows[2][i]^rows[3][i])
		rows[3][i] ^= t ^ mix(rows[3][i]^u)
	}
}

func (a *aesV3) add(rows *[4][4]byte, keys [][4]byte) {
	for i := range 4 {
		for j := range 4 {
			rows[i][j] ^= keys[i][j]
		}
	}
}

func (a *aesV3) addCon(rows *[4][4]byte, keys [][4]byte) {
	for i := range 4 {
		for j := range 4 {
			rows[i][j] ^= keys[i][a.con2[j]]
		}
	}
}

func (a *aesV3) sub(rows *[4][4]byte) {
	for i := range 4 {
		for j := range 4 {
			rows[i][j] = a.box[rows[i][j]]
		}
	}
	old := *rows
	for i := range 4 {
		rows[i] = old[a.con2[i]]
	}
}

func (a *aesV3) shift(rows *[4][4]byte) {
	b := flat(*rows)
	for i := range 16 {
		rows[i/4][i%4] = b[a.order[i]]
	}
}

func (a *aesV3) shiftCon(rows *[4][4]byte) {
	old := *rows
	for i := range 4 {
		for j := range 4 {
			rows[i][j] = old[i][a.con2[j]]
		}
	}
}

func (a *aesV3) block(value []byte) []byte {
	rows := matrix(value)
	a.addCon(&rows, a.keys[0:4])
	for i := 1; i < 3; i++ {
		a.sub(&rows)
		a.shift(&rows)
		if i == 1 {
			a.shiftCon(&rows)
			aesMix(&rows)
		}
		a.addCon(&rows, a.keys[i*4:i*4+4])
	}
	a.add(&rows, a.keys[4:8])
	return flat(rows)
}

// encrypt 加密 data（要求 len(data) >= 248），iv 为 16 字节。
func (a *aesV3) encrypt(data, iv []byte) []byte {
	source := make([]byte, len(data))
	copy(source, data)

	plaintext := make([]byte, 32)
	for i := 0; i < 31; i++ {
		at := i * 8
		n0 := (source[at] >> 4) & 2
		n1 := n0 | (source[at+1] & 64)
		n2 := n1 | ((source[at+2] >> 2) & 1)
		n3 := n2 | ((source[at+3] << 3) & 128)
		n4 := n3 | ((source[at+4] >> 1) & 4)
		n5 := n4 | ((source[at+5] << 3) & 16)
		n6 := n5 | ((source[at+6] << 5) & 32)
		plaintext[i] = n6 | ((source[at+7] >> 4) & 8)
	}
	plaintext[31] = 1

	var blocks []byte
	previous := make([]byte, len(iv))
	copy(previous, iv)
	for i := 0; i < len(plaintext); i += 16 {
		value := a.block(xorBytes(plaintext[i:i+16], previous))
		blocks = append(blocks, value...)
		previous = value
	}
	key := blocks

	out := make([]byte, len(source))
	copy(out, source)
	for i := 0; i < 31; i++ {
		at := i * 8
		k := key[i]
		out[at] &= 0xdf
		out[at] |= (k << 4) & 32
		out[at+1] &= 0xbf
		out[at+1] |= k & 64
		out[at+2] &= 0xfb
		out[at+2] |= (k << 2) & 4
		out[at+3] &= 0xef
		out[at+3] |= (k >> 3) & 16
		out[at+4] &= 0xf7
		out[at+4] |= (k + k) & 8
		out[at+5] &= 0xfd
		out[at+5] |= (k >> 3) & 2
		out[at+6] &= 0xfe
		out[at+6] |= (k >> 5) & 1
		out[at+7] &= 0x7f
		out[at+7] |= (k << 4) & 128
	}

	result := make([]byte, 0, 1+len(out))
	result = append(result, key[len(key)-1])
	result = append(result, out...)
	return result
}
