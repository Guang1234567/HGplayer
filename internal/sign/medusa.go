package sign

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"
)

// x-medusa：目录 / 搜索请求的核心签名头。
//
// 移植自 hongguo-open-player/src/server/native/app-source.js（GPL-3.0-only）。

var (
	medusaSignKey   = mustHex("8ebdfa3806ecc5cee79423e6029ed82540bc2218bb7eaef71cb691f7aa8aa2f5")
	medusaAESKey    = mustHex("f1593376766ea98d34f31b057a9d5be4")
	medusaAESIV     = mustHex("1fe109a4125283f418de9e051a969e12")
	medusaVersion   = mustHex("03000000f7e85ffad7d7dc3bd62ac87057cf6118")
	medusaMagic8    = mustHex("f7e85ffad7d7dc3bd62ac87057cf6118")
	medusaMessage10 = mustHex("4001000000000000")
	medusaFlowBlob  = []byte(`{"cmr":16777216,"cmr2":16777216,"un_h":1879194040,"vpn":0,"kd":0,"fkd":3672518972,"pd":-1872573247,"dyn":"","do":0,"tk":true}`)
)

// —— 通用 protobuf 组包（对齐源实现的 proto/protoField）——

type pbType int

const (
	pbSint pbType = iota
	pbBytes
	pbString
	pbMessage
	pbFloat
)

type pbField struct {
	tag uint64
	typ pbType
	s   string
	b   []byte
	i   int64
	f   float32
}

func pbs(tag uint64, v int64) pbField   { return pbField{tag: tag, typ: pbSint, i: v} }
func pbt(tag uint64, v string) pbField  { return pbField{tag: tag, typ: pbString, s: v} }
func pbb(tag uint64, v []byte) pbField  { return pbField{tag: tag, typ: pbBytes, b: v} }
func pbm(tag uint64, v []byte) pbField  { return pbField{tag: tag, typ: pbMessage, b: v} }
func pbf(tag uint64, v float32) pbField { return pbField{tag: tag, typ: pbFloat, f: v} }

// encode 编码单个字段；空字符串按源实现跳过。
func (f pbField) encode() []byte {
	switch f.typ {
	case pbString:
		if f.s == "" {
			return nil
		}
		body := []byte(f.s)
		return concat([][]byte{varint(f.tag<<3 | 2), varint(uint64(len(body))), body})
	case pbBytes, pbMessage:
		return concat([][]byte{varint(f.tag<<3 | 2), varint(uint64(len(f.b))), f.b})
	case pbFloat:
		body := make([]byte, 4)
		binary.LittleEndian.PutUint32(body, math.Float32bits(f.f))
		return concat([][]byte{varint(f.tag<<3 | 5), body})
	default:
		return concat([][]byte{varint(f.tag << 3), varint(zigzag(f.i))})
	}
}

// proto 按序拼接字段。
func proto(fields ...pbField) []byte {
	var parts [][]byte
	for _, f := range fields {
		if enc := f.encode(); enc != nil {
			parts = append(parts, enc)
		}
	}
	return concat(parts)
}

// deviceProtoBody 构造设备指纹 protobuf。
func deviceProtoBody(deviceID, nonce string, startedMs int64) []byte {
	return proto(
		pbs(1, 1), pbs(2, 2), pbt(3, "8662"), pbt(4, deviceID), pbt(5, nonce), pbt(6, "!noperm!"),
		pbs(7, -888888), pbs(8, -888888), pbs(9, 3), pbs(10, -888888), pbt(11, "!notset!"),
		pbt(12, "Asia/Shanghai,8"), pbt(13, "zh_CN"), pbs(14, 4),
		pbf(16, 0), pbf(17, 0), pbf(18, 0), pbf(19, 0), pbf(20, 0), pbf(21, 0),
		pbt(22, "16"), pbs(23, 41), pbs(24, 36),
		pbs(25, startedMs), pbs(26, startedMs), pbs(27, startedMs), pbs(28, startedMs),
		pbs(29, -1), pbt(30, "25053RT47C"), pbt(31, "Redmi"), pbt(32, "25053RT47C"),
		pbt(33, "25053RT47C"), pbt(34, "Xiaomi"), pbt(35, "Redmi"), pbt(36, "Redmi"),
		pbs(38, 31),
	)
}

// xmxor 是源实现中的收尾混淆。
func xmxor(data, key []byte) []byte {
	n := len(data)
	encoded := make([]byte, n)
	for i := 0; i < n; i++ {
		at := (i * 4) & 28
		d0 := uint32(key[at])
		d1 := uint32(key[at+1])
		v := uint32(data[i])
		d2 := (((v<<4 | v>>4) & 255) + d0)
		d2 = (^d2) ^ d1
		d2 = ((d2&255)<<3 | (d2&255)>>5) & 255
		d2 = (d2 + d1) & 255
		d2 = (d2 ^ d0) & 255
		encoded[n-1-i] = byte((^d2) & 255)
	}

	last := encoded[n-1] ^ encoded[n-2]
	first := encoded[0]
	encoded[0] = byte((^uint32(last) + uint32(first)) & 255)
	encoded[1] = byte(((uint32(encoded[0]) ^ uint32(encoded[n-1]) ^ 254) + uint32(encoded[1])) & 255)
	rotl5 := func(x byte) uint32 { return ((uint32(x) << 3) | (uint32(x) >> 5)) & 255 }
	encoded[2] = byte((uint32(encoded[2]) + ((uint32(int(int(last)-int(first))) ^ rotl5(encoded[1]) ^ 2) & 255)) & 255)

	for i := 0; i < n-4; i++ {
		temp := rotl5(encoded[i+2]) ^ uint32(encoded[i+1]) ^ uint32(i+3)
		encoded[i+3] = byte((^temp + uint32(encoded[i+3])) & 255)
	}
	encoded[n-1] ^= encoded[n-2]

	var sum uint32
	for i := 0; i < n-1; i++ {
		sum += uint32(encoded[i+1])
	}
	encoded[0] = byte(((uint32(encoded[0]) ^ uint32(encoded[1])) + sum) & 255)
	return encoded
}

// keyHash 派生 xmxor 的密钥与掩码。
func keyHash(signKey []byte, random uint32) ([]byte, []byte) {
	input := concat([][]byte{signKey, le32(random), signKey})
	hash := sm3Digest(input)
	d1 := (random >> 16) & 255
	d2 := ((d1 << 11) | (random >> 24)) ^ (d1 >> 5) ^ d1
	d2 = ^d2
	return hash, le32(d2)
}

// —— 对外接口 ——

// MedusaDevice 是构造 x-medusa 所需的设备信息。
type MedusaDevice struct {
	DeviceID    string
	VersionName string
}

// MedusaOptions 注入非确定性输入，便于测试复现。
type MedusaOptions struct {
	Nonce       string
	StartedMs   int64
	MessageRand uint32
	EnvLaunch   int64
	EnvPID      int64
	NowSec      int64
	Random      uint32
	XmRand      uint32
}

// 源实现中 PROCESS_NONCE / PROCESS_STARTED 是进程级常量，
// 进程内所有请求共享同一个 nonce 与启动时间。
var (
	processOnce      sync.Once
	processNonce     string
	processStartedMs int64
)

// processIdentity 返回进程级 nonce 与启动时间。
func processIdentity() (string, int64) {
	processOnce.Do(func() {
		raw := make([]byte, 18)
		_, _ = rand.Read(raw)
		processNonce = base64.RawURLEncoding.EncodeToString(raw)
		processStartedMs = time.Now().UnixMilli()
	})
	return processNonce, processStartedMs
}

// DefaultMedusaOptions 用随机值填充选项。
//
// nonce 与启动时间取自进程级常量，其余字段每次调用重新随机。
func DefaultMedusaOptions() MedusaOptions {
	nonce, startedMs := processIdentity()

	var b [8]byte
	_, _ = rand.Read(b[:])
	seed := binary.BigEndian.Uint64(b[:])
	next := func() uint32 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return uint32(seed >> 32)
	}

	return MedusaOptions{
		Nonce:       nonce,
		StartedMs:   startedMs,
		MessageRand: next(),
		EnvLaunch:   int64(next()%21) + 100,
		EnvPID:      int64(next()%2000) + 10001,
		NowSec:      time.Now().Unix(),
		Random:      next(),
		XmRand:      next(),
	}
}

// Medusa 生成 X-Medusa 头（base64）。
//
// rawURL 必须是最终发出的完整 URL；body 为请求体（无 body 传 nil）。
func Medusa(rawURL string, body []byte, khronos uint32, device MedusaDevice, opts MedusaOptions) (result []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, fmt.Errorf("medusa 计算失败: %v", r)
		}
	}()

	var bodyMd5 []byte
	if body != nil {
		bodyMd5 = md5Digest(body)
	} else {
		bodyMd5 = make([]byte, 16)
	}

	rawQuery := rawURL
	if i := indexOfByte(rawURL, '?'); i >= 0 {
		rawQuery = rawURL[i+1:]
	}
	querySm3 := sm3Digest([]byte(rawQuery))
	ts := le32(khronos)

	queryBodyTs := hashF13(querySm3, bodyMd5, ts, khronos)

	nested := proto(pbs(1, 111), pbs(2, 10), pbs(3, 694367), pbs(5, 586952199))

	envTime := proto(pbs(1, opts.NowSec), pbs(2, -2), pbs(4, 200))
	env := proto(
		pbs(1, opts.EnvLaunch), pbs(2, 146331399), pbs(3, 146331396), pbs(5, 7),
		pbt(6, "v04.06.04.03-bugfix"), pbs(7, opts.EnvPID),
		pbm(12, deviceProtoBody(device.DeviceID, opts.Nonce, opts.StartedMs)),
		pbm(13, envTime), pbt(14, device.VersionName),
	)

	queryHash := sm3Digest(concat([][]byte{[]byte(rawQuery), bodyMd5, []byte("none")}))

	message := proto(
		pbb(1, medusaMagic8), pbs(2, 3), pbs(3, int64(opts.MessageRand)), pbt(4, "8662"),
		pbt(5, device.DeviceID), pbt(6, "1588093228"), pbt(7, device.VersionName),
		pbt(8, "v04.06.04-ml-android"), pbs(9, 67503104), pbb(10, medusaMessage10),
		pbs(12, int64(khronos)), pbb(13, queryBodyTs), pbb(14, querySm3[:6]),
		pbm(15, nested), pbt(16, opts.Nonce), pbs(17, int64(khronos)),
		pbb(19, queryHash), pbt(20, "none"), pbs(21, 312),
		pbm(23, env), pbt(24, string(medusaFlowBlob)),
	)

	hash, seed := keyHash(medusaSignKey, opts.Random)

	transformed := xmxor(message, hash)
	transformed = append(append([]byte{}, medusaMessage10...), transformed...)
	reverseBytes(transformed)
	for i := range transformed {
		transformed[i] ^= seed[(^uint32(i))&3]
	}

	check := ((uint32(querySm3[0]) & 63) << 14) | 0x18000001 | ((uint32(queryBodyTs[0]) & 63) << 8)

	packed := concat([][]byte{
		{0x35},
		le32(opts.XmRand),
		le32(check),
		transformed,
		{byte(opts.Random >> 16), byte(opts.Random >> 24)},
	})

	encrypted := newAesV3(medusaAESKey, khronos).encrypt(packed, medusaAESIV)

	prefix := make([]byte, 20)
	for i := 0; i < 20; i += 4 {
		v := binary.LittleEndian.Uint32(medusaVersion[i:]) ^ khronos
		binary.LittleEndian.PutUint32(prefix[i:], v)
	}

	out := concat([][]byte{
		prefix,
		{byte(opts.Random), byte(opts.Random >> 8), 0, 1},
		encrypted,
	})
	return []byte(base64.StdEncoding.EncodeToString(out)), nil
}
