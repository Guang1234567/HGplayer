package cenc

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"testing"
)

// buildEncryptedMP4 构造一个最小但结构完整的 CENC 加密 fMP4：
// moov(trak/mdia/minf/stbl(stsd(encv + hvcC + sinf(frma=hvc1)), stsz, stsc, stco, senc)) + mdat。
//
// 样本按 cenc-aes-ctr 加密：计数器 = 8 字节 nonce ‖ 8 字节 0。
// 先按占位长度组装整份文件，再就地回填 stco 偏移与 senc 的 IV，保证布局稳定。
func buildEncryptedMP4(t *testing.T, plaintexts [][]byte, ivs [][]byte, key []byte) []byte {
	t.Helper()

	ciphertexts := make([][]byte, len(plaintexts))
	for i, plain := range plaintexts {
		block, err := aes.NewCipher(key)
		if err != nil {
			t.Fatalf("NewCipher: %v", err)
		}
		iv := make([]byte, aes.BlockSize)
		copy(iv, ivs[i])
		out := make([]byte, len(plain))
		cipher.NewCTR(block, iv).XORKeyStream(out, plain)
		ciphertexts[i] = out
	}

	stszPayload := make([]byte, 12+4*len(ciphertexts))
	binary.BigEndian.PutUint32(stszPayload[8:12], uint32(len(ciphertexts)))
	for i, c := range ciphertexts {
		binary.BigEndian.PutUint32(stszPayload[12+i*4:], uint32(len(c)))
	}
	stscPayload := make([]byte, 8+12)
	binary.BigEndian.PutUint32(stscPayload[4:8], 1)
	binary.BigEndian.PutUint32(stscPayload[8:12], 1)
	binary.BigEndian.PutUint32(stscPayload[12:16], uint32(len(ciphertexts)))
	stcoPayload := make([]byte, 12+4)
	binary.BigEndian.PutUint32(stcoPayload[4:8], 1) // 1 个 chunk
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
	file := bytes.Join([][]byte{ftyp, moov, mdat}, nil)

	// 就地回填 stco（样本绝对偏移）与 senc（样本 IV）。
	stcoAt := bytes.Index(file, stcoBox) + 8
	sencAt := bytes.Index(file, sencBox) + 8
	mdatStart := len(ftyp) + len(moov) + 8
	binary.BigEndian.PutUint32(file[stcoAt+8:], uint32(mdatStart))
	for i, iv := range ivs {
		copy(file[sencAt+8+i*8:], iv)
	}
	return file
}

func TestEncryptedSamplesAndDecrypt(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	ivs := [][]byte{
		{0, 0, 0, 0, 0, 0, 0, 1},
		{0, 0, 0, 0, 0, 0, 0, 2},
	}
	plaintexts := [][]byte{
		bytes.Repeat([]byte{0xA1}, 32),
		bytes.Repeat([]byte{0xB2}, 48),
	}
	file := buildEncryptedMP4(t, plaintexts, ivs, key)

	samples, encrypted, err := EncryptedSamples(file)
	if err != nil {
		t.Fatalf("EncryptedSamples: %v", err)
	}
	if !encrypted {
		t.Fatal("应识别出加密容器")
	}
	if len(samples) != 2 {
		t.Fatalf("样本数 = %d, want 2", len(samples))
	}
	if samples[0].Size != 32 || samples[1].Size != 48 {
		t.Fatalf("样本长度 = %d/%d", samples[0].Size, samples[1].Size)
	}
	if !bytes.Equal(samples[0].IV, ivs[0]) || !bytes.Equal(samples[1].IV, ivs[1]) {
		t.Fatalf("IV 提取错误: %x %x", samples[0].IV, samples[1].IV)
	}
	// 加密容器里读到的应是密文。
	if bytes.Equal(file[samples[0].Offset:samples[0].Offset+samples[0].Size], plaintexts[0]) {
		t.Fatal("夹具未加密")
	}

	out, err := Decrypt(file, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	// 1) 样本位置已重定位：按新容器的 chunk 偏移应能读到明文。
	outSamples, stillEncrypted, err := EncryptedSamples(out)
	if err != nil {
		t.Fatalf("重解析失败: %v", err)
	}
	if stillEncrypted || len(outSamples) != 0 {
		t.Fatalf("输出仍被识别为加密: encrypted=%v samples=%d", stillEncrypted, len(outSamples))
	}

	boxes, err := Parse(out)
	if err != nil {
		t.Fatalf("输出无法解析: %v", err)
	}
	if Find(boxes, "senc") != nil {
		t.Fatal("输出仍含 senc")
	}
	if Find(boxes, "sinf") != nil {
		t.Fatal("输出仍含 sinf")
	}
	stsd := Find(boxes, "stsd")
	if stsd == nil {
		t.Fatal("输出缺少 stsd")
	}
	if types := sampleEntryTypes(out, stsd); len(types) != 1 || types[0] != "hvc1" {
		t.Fatalf("sample entry 未恢复为明文编码: %v", types)
	}

	// 2) 明文样本内容正确（按 stco 定位）。
	topTypes := make([]string, 0, len(boxes))
	for i := range boxes {
		topTypes = append(topTypes, string(boxes[i].Type[:]))
	}
	// 布局：保持其他 box 原序，moov 提前到第一个 mdat 之前。
	if len(topTypes) != 3 || topTypes[0] != "ftyp" || topTypes[1] != "moov" || topTypes[2] != "mdat" {
		t.Fatalf("顶层布局 = %v, want [ftyp moov mdat]", topTypes)
	}
	moovBox := Find(boxes, "moov")
	if moovBox == nil {
		t.Fatal("输出缺少 moov")
	}
	chunkOffsets, err := chunkOffsetsOf(out, findDescendant(moovBox, "stco"))
	if err != nil {
		t.Fatalf("读取 stco: %v", err)
	}
	if len(chunkOffsets) != 1 {
		t.Fatalf("chunk 数 = %d, want 1", len(chunkOffsets))
	}
	at := chunkOffsets[0]
	for i, want := range plaintexts {
		got := out[at : at+len(want)]
		if !bytes.Equal(got, want) {
			t.Fatalf("样本 %d 明文不匹配: got %x want %x", i, got[:8], want[:8])
		}
		at += len(want)
	}
}

// TestDecryptPassesThroughClearMP4 未加密容器原样返回。
func TestDecryptPassesThroughClearMP4(t *testing.T) {
	file := bytes.Join([][]byte{
		makeBox("ftyp", []byte("isom")),
		makeBox("moov", makeBox("trak", nil)),
		makeBox("mdat", []byte("clear-media")),
	}, nil)
	out, err := Decrypt(file, bytes.Repeat([]byte{1}, 16))
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(out, file) {
		t.Fatal("未加密容器不应被改写")
	}
}

// TestDecryptRejectsBadKey 密钥长度非法时报错。
func TestDecryptRejectsBadKey(t *testing.T) {
	if _, err := Decrypt([]byte("not-an-mp4"), []byte{1, 2, 3}); err == nil {
		t.Fatal("期望密钥长度错误")
	}
}

// findBoxByType 在顶层 box 中按类型查找。
func findBoxByType(boxes []Box, typ string) *Box {
	for i := range boxes {
		if string(boxes[i].Type[:]) == typ {
			return &boxes[i]
		}
	}
	return nil
}
