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
