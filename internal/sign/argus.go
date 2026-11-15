package sign

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
)

// —— Argus 签名常量（移植自 hongguo-open-player argus.js）——

var (
	signKey = mustHex("fc78e0a9657a0c748ce51559903ccf03510e51d3cff232d71343e88a321c5304")
	aesKey  = mustHex("d31e3718288a1027baab59f146a09a9c")
	aesIV   = mustHex("ea180a0336ed352fcd24e4d50018ae54")
)

var (
	magic8 = []byte{0xf2, 0xf7, 0xfc, 0xff, 0xf2, 0xf7, 0xfc, 0xff}
	tail9  = []byte{0xea, 0xfb, 0x2c, 0xfe, 0x85, 0x68, 0x51, 0x91, 0x54}
)

// mustHex 解析十六进制字符串为字节切片。
func mustHex(s string) []byte {
	b, err := hexDecode(s)
	if err != nil {
		panic(err)
	}
	return b
}

// —— Simon 64/128 分组密码 ——

const zConstant = 0x3DC94C3A046D678B

// simonKeyExpansion 展开 4×64 位密钥到 72 个轮密钥。
func simonKeyExpansion(k []uint64) []uint64 {
	key := make([]uint64, 72)
	for i := 0; i < 4; i++ {
		key[i] = k[i]
	}
	for i := 4; i < 72; i++ {
		tmp := rotr64(key[i-1], 3) ^ key[i-3]
		tmp ^= rotr64(tmp, 1)
		bit := (zConstant >> uint((i-4)%62)) & 1
		key[i] = (^key[i-4] & mask64) ^ tmp ^ uint64(bit) ^ 3
	}
	return key
}

// simonEncrypt Simon 64/128 加密一对 64 位块。
func simonEncrypt(xInput, x1Input uint64, k []uint64) (uint64, uint64) {
	key := simonKeyExpansion(k)
	xi := xInput
	xi1 := x1Input
	for _, roundKey := range key {
		tmp := xi1
		f := rotl64(xi1, 1) & rotl64(xi1, 8)
		xi1 = xi ^ f ^ rotl64(xi1, 2) ^ roundKey
		xi = tmp
	}
	return xi, xi1
}

// simonEncryptPadded 对 protobuf 缓冲做 Simon 加密（按 16 字节分组）。
func simonEncryptPadded(protobuf []byte) []byte {
	key := [4]uint64{
		u64le(signKey[0:8]),
		u64le(signKey[8:16]),
		u64le(signKey[16:24]),
		u64le(signKey[24:32]),
	}
	out := make([]byte, len(protobuf))
	for i := 0; i < len(protobuf); i += 16 {
		xInput := u64le(protobuf[i : i+8])
		x1Input := u64le(protobuf[i+8 : i+16])
		ct0, ct1 := simonEncrypt(xInput, x1Input, key[:])
		binary.LittleEndian.PutUint64(out[i:i+8], ct0)
		binary.LittleEndian.PutUint64(out[i+8:i+16], ct1)
	}
	return out
}

// —— Ladon 34 轮 ARX 密码 ——

// ladonExpandTable 从 md5 hex 展开 36 个轮密钥。
func ladonExpandTable(md5Hex []byte) []uint64 {
	ht := make([]byte, 288)
	n := copy(ht, md5Hex)
	if n > 32 {
		n = 32
	}
	b0 := binary.LittleEndian.Uint64(ht[0:8])
	b8 := binary.LittleEndian.Uint64(ht[8:16])
	temp := []uint64{
		binary.LittleEndian.Uint64(ht[16:24]),
		binary.LittleEndian.Uint64(ht[24:32]),
	}
	for i := 0; i < 34; i++ {
		x8 := (rotr64(b8, 8) + b0) & mask64
		x8 ^= uint64(i)
		temp = append(temp, x8)
		x8 ^= rotl64(b0, 3)
		binary.LittleEndian.PutUint64(ht[(i+1)*8:(i+1)*8+8], x8)
		b0 = x8
		b8 = temp[0]
		temp = temp[1:]
	}
	table := make([]uint64, 36)
	for i := 0; i < 36; i++ {
		table[i] = binary.LittleEndian.Uint64(ht[i*8 : i*8+8])
	}
	return table
}

// ladonEncryptBlock 单块 16 字节加密。
func ladonEncryptBlock(ht []uint64, data []byte, at int) []byte {
	data0 := u64le(data[at : at+8])
	data1 := u64le(data[at+8 : at+16])
	for i := 0; i < 34; i++ {
		data1 = ht[i] ^ ((data0 + rotr64(data1, 8)) & mask64)
		data0 = data1 ^ rotl64(data0, 3)
	}
	res := make([]byte, 16)
	binary.LittleEndian.PutUint64(res[0:8], data0)
	binary.LittleEndian.PutUint64(res[8:16], data1)
	return res
}

// ladonEncrypt 生成 X-Ladon 头值。
func ladonEncrypt(khronos, licenseID uint64, aid int64, randomBytes []byte) string {
	data := []byte(itoa(int64(khronos)) + "-" + itoa(int64(licenseID)) + "-" + itoa(aid))

	h := md5.Sum(append(append([]byte{}, randomBytes...), []byte(itoa(aid))...))
	md5Hex := hexEncode(h[:])
	ht := ladonExpandTable([]byte(md5Hex))

	size := len(data)
	newSize := size + (16 - (size % 16))
	padVal := byte(newSize - size)
	input := make([]byte, newSize)
	for i := range input {
		input[i] = padVal
	}
	copy(input, data)

	out := make([]byte, newSize)
	for i := 0; i < newSize/16; i++ {
		copy(out[i*16:i*16+16], ladonEncryptBlock(ht, input, i*16))
	}

	combined := append(append([]byte{}, randomBytes...), out...)
	return base64.StdEncoding.EncodeToString(combined)
}

// —— PKCS#7 填充 ——

func pkcs7Pad(buf []byte) []byte {
	pad := 16 - (len(buf) % 16)
	out := make([]byte, len(buf)+pad)
	copy(out, buf)
	for i := len(buf); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// aesEncryptNoPad AES-128-CBC 加密，不自动填充。
func aesEncryptNoPad(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	mode := cipher.NewCBCEncrypter(block, iv)
	out := make([]byte, len(data))
	mode.CryptBlocks(out, data)
	return out, nil
}

// —— ProtoBuf 极简写入器 ——

type protoBuf struct {
	fields []protoField
	blobs  [][]byte
}

type protoField struct {
	tag     uint64
	typ     int
	value   uint64
	blobIdx int
}

func newProtoBuf() *protoBuf { return &protoBuf{} }

func (p *protoBuf) putVarint(idx uint64, v uint64) *protoBuf {
	p.fields = append(p.fields, protoField{tag: idx, typ: 0, value: v})
	return p
}

func (p *protoBuf) putBlob(idx uint64, data []byte) *protoBuf {
	p.blobs = append(p.blobs, data)
	p.fields = append(p.fields, protoField{tag: idx, typ: 2, blobIdx: len(p.blobs) - 1})
	return p
}

func (p *protoBuf) putUtf8(idx uint64, s string) *protoBuf {
	return p.putBlob(idx, []byte(s))
}

func (p *protoBuf) putBytes(idx uint64, data []byte) *protoBuf {
	return p.putBlob(idx, data)
}

func (p *protoBuf) toBuf() []byte {
	var parts [][]byte
	for _, f := range p.fields {
		parts = append(parts, varint((f.tag<<3)|uint64(f.typ)))
		if f.typ == 0 {
			parts = append(parts, varint(f.value))
		} else if f.typ == 2 {
			b := p.blobs[f.blobIdx]
			parts = append(parts, varint(uint64(len(b))))
			parts = append(parts, b)
		}
	}
	return concat(parts)
}

// —— Argus 551 签名 ——

// argusBodyHash 计算 body 摘要（SM3 前 6 字节）。
func argusBodyHash(stubHex string) []byte {
	if stubHex == "" {
		return sm3Digest(make([]byte, 16))[:6]
	}
	b, err := hexDecode(stubHex)
	if err != nil {
		return sm3Digest(nil)[:6]
	}
	return sm3Digest(b)[:6]
}

// argusQueryHash 计算 query 摘要（SM3 前 6 字节）。
func argusQueryHash(query string) []byte {
	if query == "" {
		return sm3Digest(make([]byte, 16))[:6]
	}
	return sm3Digest([]byte(query))[:6]
}

// argusSign 生成 X-Argus 签名（base64）。
func argusSign(query, stubHex string, ts, aid, licenseID int64, deviceID, installId string, randField int64) (string, error) {
	bean := newProtoBuf()
	bean.putVarint(1, 1077940818)
	bean.putVarint(2, 2)
	bean.putVarint(3, uint64(randField)&0xffffffff)
	bean.putUtf8(4, itoa(aid))
	bean.putUtf8(5, deviceID)
	bean.putUtf8(6, itoa(licenseID))
	bean.putUtf8(7, queryParam(query, "version_name")+"")
	bean.putUtf8(8, "v04.04.05-ov-android")
	bean.putVarint(9, 134744640)
	bean.putBytes(10, make([]byte, 8))
	bean.putVarint(11, 0)
	bean.putVarint(12, (uint64(ts)<<1)&0xffffffff)
	bean.putBytes(13, argusBodyHash(stubHex))
	bean.putBytes(14, argusQueryHash(query))

	sub15 := newProtoBuf()
	sub15.putVarint(1, 1).putVarint(2, 1).putVarint(3, 1).putVarint(7, 0xC792ECCC)
	bean.putBlob(15, sub15.toBuf())

	bean.putUtf8(16, installId)
	bean.putUtf8(20, "none")
	bean.putVarint(21, 738)

	sub23 := newProtoBuf()
	sub23.putUtf8(1, "NX551J").putVarint(2, 8196).putVarint(4, 0x80E0D800)
	bean.putBlob(23, sub23.toBuf())
	bean.putVarint(25, 2)

	protobuf := pkcs7Pad(bean.toBuf())
	encPb := simonEncryptPadded(protobuf)

	un := concat([][]byte{magic8, encPb, tail9})
	buf := make([]byte, len(un))
	copy(buf, magic8)
	for i := 8; i < len(un); i++ {
		buf[i] = un[i] ^ magic8[i%8]
	}
	reverseBytes(buf)
	padded := pkcs7Pad(concat([][]byte{buf, []byte{0x61, 0x6f}}))

	encrypted, err := aesEncryptNoPad(padded, aesKey, aesIV)
	if err != nil {
		return "", err
	}
	result := concat([][]byte{{0xf2, 0x81}, encrypted})
	return base64.StdEncoding.EncodeToString(result), nil
}

// PlayerSigner 生成播放请求的签名头。
const playerLicenseID = int64(1611921764)

// playerSignerHeaders 生成 x-khronos / x-ss-req-ticket / x-ladon / x-argus / x-ss-stub 头。
func playerSignerHeaders(query string, body []byte, ts int64) (map[string]string, error) {
	aid := queryParamInt(query, "aid", 1967)

	var stub string
	if len(body) > 0 {
		h := md5.Sum(body)
		stub = hexEncode(h[:])
	}

	ladonRandom := make([]byte, 4)
	if _, err := rand.Read(ladonRandom); err != nil {
		return nil, err
	}

	randBytes := make([]byte, 4)
	if _, err := rand.Read(randBytes); err != nil {
		return nil, err
	}
	randField := int64(int32(binary.LittleEndian.Uint32(randBytes)))

	deviceID := queryParam(query, "device_id")
	installId := queryParam(query, "iid")

	argusVal, err := argusSign(query, stub, ts, aid, playerLicenseID, deviceID, installId, randField)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"x-ss-req-ticket": itoa(ts * 1000),
		"x-khronos":       itoa(ts),
		"x-ladon":         ladonEncrypt(uint64(ts), uint64(playerLicenseID), aid, ladonRandom),
		"x-argus":         argusVal,
	}
	if stub != "" {
		headers["x-ss-stub"] = toUpper(stub)
	}
	return headers, nil
}

// —— 小工具 ——

func concat(parts [][]byte) []byte {
	var total int
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func reverseBytes(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var neg bool
	if v < 0 {
		neg = true
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func toUpper(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}

func queryParam(query, name string) string {
	if query == "" {
		return ""
	}
	for _, part := range splitAnd(query, '&') {
		eq := indexOfByte(part, '=')
		k := part
		if eq >= 0 {
			k = part[:eq]
		}
		if k == name {
			if eq < 0 {
				return ""
			}
			return part[eq+1:]
		}
	}
	return ""
}

func queryParamInt(query, name string, def int64) int64 {
	v := queryParam(query, name)
	if v == "" {
		return def
	}
	var n int64
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return def
		}
		n = n*10 + int64(v[i]-'0')
	}
	return n
}

func splitAnd(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func indexOfByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
