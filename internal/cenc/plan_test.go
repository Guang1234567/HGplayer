package cenc

import (
	"bytes"
	"errors"
	"testing"

	"hgplayer/internal/cenc/cenctest"
)

// planFixture 构造夹具并返回头部窗口（到 mdat header 结束）。
func planFixture(t *testing.T, samples int, sampleSize int) (cenctest.Fixture, []byte, *Plan) {
	t.Helper()
	key := bytes.Repeat([]byte{0x22}, 16)
	plaintexts := make([][]byte, samples)
	for i := range plaintexts {
		plaintexts[i] = cenctest.Pattern(byte(i), sampleSize)
	}
	fixture := cenctest.Build(plaintexts, key)

	boxes, err := Parse(fixture.File)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	moov := findBoxByType(boxes, "moov")
	if moov == nil {
		t.Fatal("夹具缺少 moov")
	}
	headEnd := moov.Start + moov.Size + 8 // 含 mdat header
	head := fixture.File[:headEnd]

	plan, err := BuildPlan(head, int64(len(fixture.File)))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return fixture, head, plan
}

// TestBuildPlanMapsLayout 计划应把上游偏移映射成明文偏移。
func TestBuildPlanMapsLayout(t *testing.T) {
	fixture, _, plan := planFixture(t, 20, 4096)

	want, err := Decrypt(fixture.File, fixture.Key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plan.PlainSize() != int64(len(want)) {
		t.Fatalf("PlainSize = %d, want %d", plan.PlainSize(), len(want))
	}
	if plan.Shift >= 0 {
		t.Fatalf("剔除加密辅助 box 后明文应更短，Shift = %d", plan.Shift)
	}
	if len(plan.Samples) != 20 {
		t.Fatalf("样本数 = %d, want 20", len(plan.Samples))
	}
	// 前缀应正好到载荷内容起点：即明文文件里 mdat 内容的位置。
	mdatContent := bytes.Index(want, want[plan.PrefixSize():plan.PrefixSize()+256])
	if int64(mdatContent) != plan.PrefixSize() {
		t.Fatalf("前缀长度 = %d, 明文 mdat 内容位置 = %d", plan.PrefixSize(), mdatContent)
	}
	if !bytes.Equal(plan.Prefix, want[:plan.PrefixSize()]) {
		t.Fatal("前缀与整段解密结果不一致")
	}
}

// TestBuildPlanNeedsMoreHead 头部窗口不足时应提示扩大窗口。
func TestBuildPlanNeedsMoreHead(t *testing.T) {
	fixture, head, _ := planFixture(t, 4, 512)
	// 只给 ftyp + 半个 moov。
	if _, err := BuildPlan(head[:len(head)/3], int64(len(fixture.File))); !errors.Is(err, ErrNeedMoreHead) {
		t.Fatalf("err = %v, want ErrNeedMoreHead", err)
	}
}

// TestReadWindowMatchesWholeFile 任意窗口切法拼出的明文必须与整段解密逐字节一致。
func TestReadWindowMatchesWholeFile(t *testing.T) {
	fixture, _, plan := planFixture(t, 23, 3000)
	want, err := Decrypt(fixture.File, fixture.Key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	fetch := func(u0, u1 int64) ([]byte, error) {
		if u0 < 0 || u1 > int64(len(fixture.File)) || u1 < u0 {
			t.Fatalf("上游区间越界: %d-%d", u0, u1)
		}
		return append([]byte(nil), fixture.File[u0:u1]...), nil
	}

	for _, window := range []int64{7919, 4096, 1000, 16, 1} {
		got := make([]byte, 0, len(want))
		for start := int64(0); start < plan.PlainSize(); start += window {
			end := min(start+window, plan.PlainSize())
			part, err := plan.ReadWindow(fixture.Key, start, end, fetch)
			if err != nil {
				t.Fatalf("窗口 %d-%d 读取失败: %v", start, end, err)
			}
			if int64(len(part)) != end-start {
				t.Fatalf("窗口 %d-%d 长度 = %d", start, end, len(part))
			}
			got = append(got, part...)
		}
		if !bytes.Equal(got, want) {
			idx := 0
			for idx < len(got) && idx < len(want) && got[idx] == want[idx] {
				idx++
			}
			t.Fatalf("窗口大小 %d 拼接结果与整段解密不一致（首个差异 @%d）", window, idx)
		}
	}
}

// TestReadWindowSingleByteSamples 极短窗口（切在样本中间）也必须正确。
func TestReadWindowSingleByteSamples(t *testing.T) {
	fixture, _, plan := planFixture(t, 6, 33)
	want, err := Decrypt(fixture.File, fixture.Key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	fetch := func(u0, u1 int64) ([]byte, error) {
		return append([]byte(nil), fixture.File[u0:u1]...), nil
	}
	for at := int64(0); at < plan.PlainSize(); at++ {
		part, err := plan.ReadWindow(fixture.Key, at, at+1, fetch)
		if err != nil {
			t.Fatalf("读取 @%d 失败: %v", at, err)
		}
		if part[0] != want[at] {
			t.Fatalf("明文 @%d = %02x, want %02x", at, part[0], want[at])
		}
	}
}

// TestReadWindowRejectsOutOfRange 越界窗口应报错。
func TestReadWindowRejectsOutOfRange(t *testing.T) {
	fixture, _, plan := planFixture(t, 2, 256)
	fetch := func(u0, u1 int64) ([]byte, error) { return make([]byte, u1-u0), nil }
	if _, err := plan.ReadWindow(fixture.Key, 0, plan.PlainSize()+1, fetch); err == nil {
		t.Fatal("越界窗口应报错")
	}
	_ = fixture
}
