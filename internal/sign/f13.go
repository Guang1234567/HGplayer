package sign

import "encoding/binary"

// x-medusa 的 f13 哈希链。
//
// 移植自 hongguo-open-player/src/server/native/app-source.js（GPL-3.0-only）。

// branch2SV 是 md5SumV3 的常量表。
var branch2SV = [64]uint32{
	0xa7aefe20, 0x7149f1d6, 0x47e4ca07, 0xe9b58f67, 0x93b924de, 0xc614d0f5, 0x38afe0ef, 0xb2bbad73,
	0xe24444c3, 0x9d3aec9b, 0xdf7b37e4, 0xd8b16d40, 0xf8ac31b8, 0x76b9a90b, 0x31d833ee, 0x953fce64,
	0x353595a4, 0x4609c13b, 0x36925008, 0x8c6d0925, 0x5df5c177, 0x1cfbf52b, 0x8a4fa7f0, 0x114ca35e,
	0x8193f984, 0x7a7a8733, 0x316ab4d5, 0x3c20cfc9, 0xa6d84453, 0x3a18500c, 0x798ec47a, 0x97a76b28,
	0x66c4ff96, 0x51716443, 0x0dd2fc3b, 0xb5696da7, 0xbbeb3ac5, 0x5c53d204, 0xd32608ce, 0x7279b9ec,
	0xf4188ecf, 0xf7d793db, 0x332cc491, 0xab76ae15, 0x9bebe727, 0x18a01384, 0x5be9f8a7, 0x5f90a754,
	0x39b663c0, 0x36673c83, 0x7c92f514, 0x9d7d94d7, 0xe2e8d9aa, 0x5f7e9ea9, 0x7abd4551, 0x569e05da,
	0x40a25632, 0x3df5a9a5, 0xbab37d80, 0x454286dc, 0x3f5d4e78, 0x03d7b75d, 0xb1fe4af7, 0xa5ab26a3,
}

// branch2Orders 是 md5SumV3 的置换表（320 字节，按 64 字节分段）。
var branch2Orders = mustHex("0f0704000908030a060b050d0e010c020f05080c0009020103070e060b0a0d04060500070c000a04080f010b0d09020e060b02050403080101070a000d0c090e0d070e0f0b0208030c0509010004060a0d0902060f0b0a040807000c0503010e0c090f07060f030e020d0405010b0a000c050a090e0802040407030f01060b000b0f0408020a0700090d06010e03050c0b060a0508020c03070f0e090d0001040906080f050800040a0b030d01020c0e090d0c0604070a03030f00080105020e01000d0f090a0b0e0402080703060c0501080a0c0f0905060b000304020e070d04080f000c0f0e0d0a01060207090503040205080d0b0a0606000e0f070c09030a08040f000b01060d0c0709030e05020a070b050f00020e0108030d0c0609040d070f08050f06040b0a0e0c090002030d0c020704010b0e0e08060f09050003")

// sumMd5 是源实现中的 sumMd5。
func sumMd5(data []byte) uint32 {
	check := uint32(0x20220420)
	for i := 0; i < 12; i++ {
		var temp uint32
		if i&1 == 0 {
			temp = (check >> 3) ^ check
			check = uint32(data[i]) ^ (check << 7)
		} else {
			temp = (check >> 5) ^ check
			check = uint32(data[i]) | (check << 11)
			check = ^check
		}
		check ^= temp
	}
	return (check | 4) ^ 0x1000000
}

// md5V3Step 是 md5SumV3 的单步。
func md5V3Step(kind int, a, b, c, d, m, constant uint32, shift uint) uint32 {
	var f uint32
	switch kind {
	case 0:
		f = (b & c) | (^b & d)
	case 1:
		f = (b & d) | (c & ^d)
	case 2:
		f = b ^ c ^ d
	default:
		f = c ^ (b | ^d)
	}
	return b + rol32(a+f+m+constant, shift)
}

// md5SumV3 是 md5 的变体（32 字节输出）。
func md5SumV3(message []byte, countV2 uint32, orders []byte, countV1 uint32) []byte {
	var sv [64]uint32
	for i, v := range branch2SV {
		sv[i] = ror32(v, uint(countV1))
	}

	start := [4]uint32{
		ror32(0x79e0f2fb, uint(countV2)), ror32(0xc8b52570, uint(countV2)),
		ror32(0xebc2f8cd, uint(countV2)), ror32(0x7c104d93, uint(countV2)),
	}
	endCount := (countV2 + 6) & 255
	end := [4]uint32{
		ror32(0x19be4866, uint(endCount)), ror32(0xe85986b4, uint(endCount)),
		ror32(0xe19b326e, uint(endCount)), ror32(0x71d1d7d4, uint(endCount)),
	}

	var m [16]uint32
	for i := range 16 {
		m[i] = binary.LittleEndian.Uint32(message[i*4:])
	}

	a, b, c, d := start[0], start[1], start[2], start[3]
	run := func(kind int, shifts [16]uint, offset int) {
		for i := range 16 {
			idx := int(orders[offset+i])
			sh := shifts[i]
			svv := sv[offset+i]
			phase := i & 3

			var next uint32
			switch phase {
			case 0:
				next = md5V3Step(kind, a, b, c, d, m[idx], svv, sh)
				a = next
			case 1:
				next = md5V3Step(kind, d, a, b, c, m[idx], svv, sh)
				d = next
			case 2:
				next = md5V3Step(kind, c, d, a, b, m[idx], svv, sh)
				c = next
			default:
				next = md5V3Step(kind, b, c, d, a, m[idx], svv, sh)
				b = next
			}
		}
	}

	run(0, [16]uint{7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22}, 0)
	run(1, [16]uint{5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20}, 16)
	run(2, [16]uint{4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23}, 32)
	run(3, [16]uint{6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21}, 48)

	ret := make([]byte, 16)
	binary.LittleEndian.PutUint32(ret[0:], (start[0]+a)^end[0])
	binary.LittleEndian.PutUint32(ret[4:], (start[1]+b)^end[1])
	binary.LittleEndian.PutUint32(ret[8:], (start[2]+c)^end[2])
	binary.LittleEndian.PutUint32(ret[12:], (start[3]+d)^end[3])
	return append(ret, le32(sumMd5(ret))...)
}

// branch2F13 是 f13 的分支 2 路径。
func branch2F13(iv uint32, querySm3, bodyMd5, tsBytes []byte, khronos uint32) []byte {
	v := (iv & 13) * 86
	ivV0 := int(((v >> 15) & 255) + ((v >> 8) & 255))

	n1List := [5]uint32{0x8980f29b, 0xeb549c7f, 0xb08726db, 0xd40cb5e6, 0xe8f559e4}
	n1 := n1List[ivV0]
	countV1 := (n1 + khronos + 1) & 255
	countV2 := n1 + khronos
	shift := (countV2 + 5) & 7

	seed := [8]uint32{0x84, 0x96, 0x77, 0x9d, 0xd4, 0x15, 0x0b, 0xf8}
	pad := make([]byte, len(seed))
	for i, s := range seed {
		pad[i] = byte(((s | (s << 8)) >> shift) & 255)
	}

	input := concat([][]byte{querySm3, bodyMd5, tsBytes, pad, {0xa0, 0x01, 0x00, 0x00}})
	orders := branch2Orders[ivV0<<6 : (ivV0+1)<<6]
	return md5SumV3(input, countV2, orders, countV1)
}

// hashF13 计算 f13 摘要（按分支选择不同实现）。
func hashF13(querySm3, bodyMd5, tsBytes []byte, khronos uint32) []byte {
	iv := getIV(getIV(getIV(0x20230928, querySm3), bodyMd5), tsBytes)
	low := int(iv & 15)
	ivV0 := (low * 171) >> 9
	branch := low - ivV0*3

	if branch == 2 {
		return branch2F13(iv, querySm3, bodyMd5, tsBytes, khronos)
	}
	if branch != 0 {
		head := make([]byte, 16)
		copy(head, querySm3[:16])
		return concat([][]byte{head, bodyMd5, le32(sumMd5(concat([][]byte{querySm3, bodyMd5})))})
	}

	ivV1List := [6]uint32{0xc4a78580, 0xb3c0fd39, 0xc58c5686, 0xc9aa3ba7, 0xf5a7adf2, 0x963c2ed1}
	ivV1 := ivV1List[ivV0]
	countV1 := (ivV1 + khronos + 1) & 255
	countV2 := ivV1 + khronos

	tt02 := [64]uint32{
		0xebb64faf, 0x07aadcc2, 0xcf3187bf, 0xe01138ff, 0x6d0bfcff, 0x5a30a3be, 0xb41ad638, 0x34180eb8,
		0xf233eb6f, 0xb1a584cc, 0xccc30dc7, 0x47d1db51, 0xd55653de, 0x70a84fa1, 0x57473c12, 0xf76f0288,
		0x2c077f0a, 0xda0dcad0, 0xfbb86f6c, 0xfdc4cf00, 0x688a020d, 0xe676c6a6, 0x8cd6338b, 0x1a3c8d0e,
		0xcce8b06b, 0x6ad0ed0b, 0xa0522717, 0xdc71ac83, 0x2285db71, 0xd5b4dda6, 0x736f8650, 0x6560306c,
		0x617ce2a6, 0xe423417e, 0x0a40e143, 0x544e4032, 0x88dffb2a, 0x716c1ae0, 0x4c467a88, 0x05b23bb3,
		0xe1d0b866, 0xbaa3dcb8, 0xae3374d3, 0xc3381a50, 0x1702f75b, 0xfe6da368, 0xf0b4cf48, 0x4e0ffbb8,
		0x72aad10d, 0x26c53a3d, 0xf2bce0f6, 0xb4557581, 0x4a257fdd, 0x8c3182a2, 0xab0b3b86, 0x3d5dfb14,
		0x4f103634, 0xd37b52d7, 0x444eff16, 0xeb0a33d1, 0x6ca86f6e, 0x00284ba7, 0x08387cfa, 0x5fb37586,
	}

	var tt [64]uint32
	for i, v := range tt02 {
		tt[i] = ror32(v, uint(countV1))
	}

	n0 := (countV2 + 2) & 7
	seed := [4]uint32{0xfa, 0x45, 0x61, 0xd7}
	pad := make([]byte, len(seed))
	for i, s := range seed {
		pad[i] = byte(((s | (s << 8)) >> n0) & 255)
	}

	data := concat([][]byte{querySm3, bodyMd5, tsBytes, pad,
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0xa0}})

	// 消息扩展：112 轮。
	di := make([]uint32, 0, 128)
	for i := 0; i < len(data); i += 4 {
		di = append(di, binary.BigEndian.Uint32(data[i:]))
	}
	di0 := di[0]
	for i := 0; i < 112; i++ {
		di1 := di[i+1]
		di14 := di[i+14]
		r1 := rol32(di1, 14) ^ rol32(di1, 25) ^ (di1 >> 3)
		r2 := rol32(di14, 13) ^ rol32(di14, 15) ^ (di14 >> 10)
		di = append(di, di0+di[i+9]+r1+r2)
		di0 = di1
	}

	initList := [8]uint32{0x7aba4fc8, 0x67166507, 0x6403fa00, 0x340f512f, 984304912, 3005047866, 2874125293, 2152413264}
	var init [8]uint32
	for i, v := range initList {
		init[i] = ror32(v, uint(countV2&31))
	}

	chosen := [6][11]int{
		{101, 5, 7, 6, 3, 2, 1, 0, 5, 4, 3},
		{96, 0, 6, 7, 5, 3, 2, 1, 5, 4, 4},
		{96, 7, 6, 2, 1, 4, 0, 5, 4, 3, 5},
		{99, 3, 6, 2, 4, 5, 1, 0, 0, 7, 6},
		{96, 0, 5, 6, 7, 3, 1, 2, 5, 4, 4},
		{100, 2, 0, 3, 5, 4, 6, 7, 2, 1, 5},
	}[ivV0]

	d := make([]uint32, 8)
	copy(d, init[:])

	for i := 0; i < chosen[0]; i++ {
		base := int(ivV1) + i
		n0v := di[base&127]
		n1 := ((d[chosen[3]] ^ d[chosen[4]]) & d[chosen[1]]) ^ d[chosen[3]]
		n2 := rol32(d[chosen[1]], 26) ^ rol32(d[chosen[1]], 21) ^ rol32(d[chosen[1]], 7)
		n4 := n0v + n1 + n2 + tt[base&63] + d[chosen[5]]
		n5 := rol32(d[chosen[2]], 30) ^ rol32(d[chosen[2]], 19) ^ rol32(d[chosen[2]], 10)
		n6 := (d[chosen[2]] & d[chosen[6]]) | ((d[chosen[2]] | d[chosen[6]]) & d[chosen[7]])
		n7 := n5 + n6
		old := d[chosen[9]]

		// d.unshift(d.pop())
		last := d[7]
		copy(d[1:], d[0:7])
		d[0] = last

		d[chosen[10]] = n7 + n4
		d[chosen[8]] = old + n4
	}

	ret := make([]byte, 32)
	for i := range 8 {
		binary.BigEndian.PutUint32(ret[i*4:], d[i]+init[i])
	}
	folded := bxor(ret[0:16], ret[16:32])
	copy(ret, folded)
	return concat([][]byte{ret[0:16], le32(sumMd5(ret[0:16]))})
}
