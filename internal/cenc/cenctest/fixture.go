// Package cenctest 提供 CENC 加密 MP4 夹具，供 cenc 与 media 的测试复用。
//
// 夹具刻意贴近真实片源：moov 在 mdat 之前、样本按 chunk 分组（多条 stco 项）、
// moov 尾部追加 free box，用于覆盖 chunk 偏移重定位与载荷之后的 box。
package cenctest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
)

// Fixture 是一份构造好的加密片源。
type Fixture struct {
	// File 是加密后的完整文件。
	File []byte
	// Key 是 16 字节内容密钥。
	Key []byte
	// Plaintexts 是各样本的明文（按文件顺序）。
	Plaintexts [][]byte
	// IVs 是各样本的 8 字节 nonce。
	IVs [][]byte
}

// chunkSamples 是每个 chunk 的样本数。
const chunkSamples = 4

// Build 构造 CENC 加密 fMP4：ftyp + moov(trak/mdia/minf/stbl(stsd(encv+hvcC+sinf)、
// stsz、stsc、stco、senc)) + mdat + free。
//
// 样本用 cenc-aes-ctr 加密（计数器 = 8 字节 nonce ‖ 8 字节 0），IV 由样本序号派生。
// 明文按 chunkSamples 个样本一组写入 chunk，stco 记录每个 chunk 的绝对偏移。
func Build(plaintexts [][]byte, key []byte) Fixture {
	ciphertexts := make([][]byte, len(plaintexts))
	ivs := make([][]byte, len(plaintexts))
	for i, plain := range plaintexts {
		block, err := aes.NewCipher(key)
		if err != nil {
			panic(err)
		}
		nonce := make([]byte, 8)
		binary.BigEndian.PutUint64(nonce, uint64(i+1))
		counter := make([]byte, aes.BlockSize)
		copy(counter, nonce)
		out := make([]byte, len(plain))
		cipher.NewCTR(block, counter).XORKeyStream(out, plain)
		ciphertexts[i] = out
		ivs[i] = nonce
	}

	chunks := (len(ciphertexts) + chunkSamples - 1) / chunkSamples
	if chunks == 0 {
		chunks = 1
	}

	stszPayload := make([]byte, 12+4*len(ciphertexts))
	binary.BigEndian.PutUint32(stszPayload[8:12], uint32(len(ciphertexts)))
	for i, c := range ciphertexts {
		binary.BigEndian.PutUint32(stszPayload[12+i*4:], uint32(len(c)))
	}
	stscPayload := make([]byte, 8+12)
	binary.BigEndian.PutUint32(stscPayload[4:8], 1)
	binary.BigEndian.PutUint32(stscPayload[8:12], 1)
	binary.BigEndian.PutUint32(stscPayload[12:16], chunkSamples)
	stcoPayload := make([]byte, 8+4*chunks)
	binary.BigEndian.PutUint32(stcoPayload[4:8], uint32(chunks))
	sencPayload := make([]byte, 8+8*len(ivs))
	binary.BigEndian.PutUint32(sencPayload[4:8], uint32(len(ivs)))

	sinf := makeBox("sinf", makeBox("frma", []byte("hvc1")))
	encvPayload := make([]byte, 78) // VisualSampleEntry 字段长度
	encvPayload = append(encvPayload, makeBox("hvcC", []byte{0x01})...)
	encvPayload = append(encvPayload, sinf...)
	stsdPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdPayload[4:8], 1)
	stsd := makeBox("stsd", append(stsdPayload, makeBox("encv", encvPayload)...))

	stcoBox := makeBox("stco", stcoPayload)
	sencBox := makeBox("senc", sencPayload)
	stbl := makeBox("stbl", bytes.Join([][]byte{
		stsd, makeBox("stsz", stszPayload), makeBox("stsc", stscPayload), stcoBox, sencBox,
	}, nil))
	moov := makeBox("moov", makeBox("trak", makeBox("mdia", makeBox("minf", stbl))))
	ftyp := makeBox("ftyp", []byte("isom"))
	mdat := makeBox("mdat", bytes.Join(ciphertexts, nil))
	free := makeBox("free", makeBox("skip", nil))
	file := bytes.Join([][]byte{ftyp, moov, mdat, free}, nil)

	// 就地回填 stco（各 chunk 的绝对偏移）与 senc（样本 IV）。
	mdatStart := len(ftyp) + len(moov) + 8
	stcoAt := bytes.Index(file, stcoBox) + 8
	sencAt := bytes.Index(file, sencBox) + 8
	at := mdatStart
	for chunk := 0; chunk < chunks; chunk++ {
		binary.BigEndian.PutUint32(file[stcoAt+8+chunk*4:], uint32(at))
		for i := chunk * chunkSamples; i < (chunk+1)*chunkSamples && i < len(ciphertexts); i++ {
			at += len(ciphertexts[i])
		}
	}
	for i, iv := range ivs {
		copy(file[sencAt+8+i*8:], iv)
	}

	return Fixture{File: file, Key: key, Plaintexts: plaintexts, IVs: ivs}
}

// Pattern 生成 size 字节的确定性明文，便于按内容定位。
func Pattern(marker byte, size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = marker + byte(i%251)
	}
	return out
}

// makeBox 构造一个标准 8 字节 header 的 box。
func makeBox(typ string, payload []byte) []byte {
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(buf)))
	copy(buf[4:8], typ)
	copy(buf[8:], payload)
	return buf
}
