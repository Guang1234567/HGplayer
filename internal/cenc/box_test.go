package cenc

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// makeBox 构造一个标准 8 字节 header 的 box。
func makeBox(typ string, payload []byte) []byte {
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(buf)))
	copy(buf[4:8], typ)
	copy(buf[8:], payload)
	return buf
}

func TestParseSimpleBoxes(t *testing.T) {
	data := bytes.Join([][]byte{
		makeBox("ftyp", []byte("isom")),
		makeBox("moov", nil),
		makeBox("mdat", []byte("media")),
	}, nil)

	boxes, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(boxes) != 3 {
		t.Fatalf("len = %d, want 3", len(boxes))
	}
	if string(boxes[0].Type[:]) != "ftyp" {
		t.Fatalf("type0 = %q", string(boxes[0].Type[:]))
	}
	if string(boxes[2].Type[:]) != "mdat" {
		t.Fatalf("type2 = %q", string(boxes[2].Type[:]))
	}
}

func TestParseNestedContainers(t *testing.T) {
	inner := makeBox("stbl", makeBox("stsd", nil))
	moov := makeBox("moov", bytes.Join([][]byte{
		makeBox("trak", inner),
	}, nil))
	data := bytes.Join([][]byte{makeBox("ftyp", nil), moov}, nil)

	boxes, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	moovBox := Find(boxes, "moov")
	if moovBox == nil {
		t.Fatal("moov not found")
	}
	trak := Find(moovBox.Children, "trak")
	if trak == nil {
		t.Fatal("trak not found under moov")
	}
	stbl := Find(trak.Children, "stbl")
	if stbl == nil {
		t.Fatal("stbl not found under trak")
	}
}

func TestParseRejectsTruncated(t *testing.T) {
	// 声明 100 字节但只给 20 字节。
	buf := make([]byte, 20)
	binary.BigEndian.PutUint32(buf[:4], 100)
	copy(buf[4:8], "moov")
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error for truncated box")
	}
}

func TestParseRejectsDeepNesting(t *testing.T) {
	// 构造超过 maxNesting 的嵌套。
	inner := makeBox("moov", nil)
	for range maxNesting + 2 {
		inner = makeBox("moov", inner)
	}
	_, err := Parse(inner)
	if err == nil {
		t.Fatal("expected error for excessive nesting")
	}
}

func TestParseExtendedSize(t *testing.T) {
	payload := make([]byte, 100)
	buf := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint32(buf[:4], 1) // size == 1 → 用扩展 size
	copy(buf[4:8], "mdat")
	binary.BigEndian.PutUint64(buf[8:16], uint64(16+len(payload)))
	copy(buf[16:], payload)

	boxes, err := Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(boxes) != 1 || boxes[0].HeaderSize != 16 {
		t.Fatalf("boxes = %+v", boxes)
	}
}

func TestFind(t *testing.T) {
	data := bytes.Join([][]byte{
		makeBox("ftyp", nil),
		makeBox("moov", makeBox("trak", makeBox("mdia", nil))),
	}, nil)
	boxes, _ := Parse(data)

	if Find(boxes, "mdia") == nil {
		t.Fatal("mdia should be found nested")
	}
	if Find(boxes, "nope") != nil {
		t.Fatal("missing type should return nil")
	}
}

func TestPruneEncBoxes(t *testing.T) {
	// 构造一个含 senc 的 moov。
	payload := bytes.Join([][]byte{
		makeBox("stsd", nil),
		makeBox("senc", []byte("encrypted")),
	}, nil)
	data := bytes.Join([][]byte{makeBox("ftyp", nil), makeBox("moov", payload)}, nil)
	boxes, _ := Parse(data)

	pruned := PruneEncBoxes(boxes)
	if hasSampleEncryption(pruned) {
		t.Fatal("senc should be pruned")
	}
	if Find(pruned, "stsd") == nil {
		t.Fatal("stsd should be kept")
	}
}

func TestHasSampleEncryption(t *testing.T) {
	enc := bytes.Join([][]byte{makeBox("ftyp", nil), makeBox("moov", makeBox("senc", nil))}, nil)
	plain := bytes.Join([][]byte{makeBox("ftyp", nil), makeBox("moov", nil)}, nil)

	encBoxes, _ := Parse(enc)
	if !hasSampleEncryption(encBoxes) {
		t.Fatal("should detect encryption")
	}
	plainBoxes, _ := Parse(plain)
	if hasSampleEncryption(plainBoxes) {
		t.Fatal("should not detect encryption")
	}
}

func TestRewriteMoov(t *testing.T) {
	// moov > trak > stbl > stsd（含 encv + sinf）
	sinf := makeBox("sinf", makeBox("frma", []byte("avc1")))
	encv := makeBox("encv", sinf)
	stsdPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdPayload[4:8], 1) // entry count = 1
	stsd := makeBox("stsd", append(stsdPayload, encv...))
	stbl := makeBox("stbl", stsd)
	trak := makeBox("trak", stbl)
	moovPayload := bytes.Join([][]byte{trak, makeBox("senc", nil)}, nil)
	data := bytes.Join([][]byte{makeBox("ftyp", nil), makeBox("moov", moovPayload)}, nil)

	boxes, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	moov := Find(boxes, "moov")
	if moov == nil {
		t.Fatal("moov not found")
	}

	out, err := RewriteMoov(moov)
	if err != nil {
		t.Fatalf("RewriteMoov: %v", err)
	}

	reparsed, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if Find(reparsed, "senc") != nil {
		t.Fatal("senc should be removed after rewrite")
	}
}

func TestRewriteMoovRejectsNonMoov(t *testing.T) {
	other := makeBox("ftyp", nil)
	boxes, _ := Parse(other)
	_, err := RewriteMoov(&boxes[0])
	if err == nil {
		t.Fatal("expected error for non-moov box")
	}
}
