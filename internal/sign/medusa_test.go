package sign

import (
	"encoding/json"
	"os"
	"testing"
)

// medusaVectorInput 是生成向量时使用的固定输入。
const (
	medusaTestURL    = "https://api5-normal-sinfonlineb.fqnovel.com/reading/distribution/category/landpage/v/?aid=8662&ts=1700000000"
	medusaTestBody   = `{"limit":15,"req_scene":"default"}`
	medusaTestNonce  = "AAAAAAAAAAAAAAAAAAAAAAAA"
	medusaTestKronos = uint32(1700000000)
)

func medusaTestOptions() MedusaOptions {
	return MedusaOptions{
		Nonce:       medusaTestNonce,
		StartedMs:   1700000000000,
		MessageRand: 0x11223344,
		EnvLaunch:   5,
		EnvPID:      6789,
		NowSec:      1700000000,
		Random:      0x55667788,
		XmRand:      0x99aabbcc,
	}
}

func medusaTestDevice() MedusaDevice {
	return MedusaDevice{DeviceID: "1624456208323876", VersionName: "7.3.5.32"}
}

// TestMedusaGoldenVector 用 JS 参考实现（testdata/medusa_ref.js）产出的向量
// 逐字节比对 x-medusa 输出。
func TestMedusaGoldenVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/medusa_vector.json")
	if err != nil {
		t.Fatalf("读取向量失败: %v", err)
	}
	var vec struct {
		Medusa string `json:"medusa"`
	}
	if err := json.Unmarshal(raw, &vec); err != nil {
		t.Fatalf("解析向量失败: %v", err)
	}
	if vec.Medusa == "" {
		t.Fatal("向量为空")
	}

	got, err := Medusa(medusaTestURL, []byte(medusaTestBody), medusaTestKronos, medusaTestDevice(), medusaTestOptions())
	if err != nil {
		t.Fatalf("Medusa: %v", err)
	}

	if string(got) != vec.Medusa {
		// 只报告差异位置，避免打印整段 base64。
		n := len(got)
		if len(vec.Medusa) < n {
			n = len(vec.Medusa)
		}
		diff := -1
		for i := 0; i < n; i++ {
			if got[i] != vec.Medusa[i] {
				diff = i
				break
			}
		}
		t.Fatalf("x-medusa 不匹配：长度 got=%d want=%d，首个差异位置=%d", len(got), len(vec.Medusa), diff)
	}
}

// TestMedusaSoHandlesNoBody 验证无 body 时不 panic。
func TestMedusaNoBody(t *testing.T) {
	if _, err := Medusa(medusaTestURL, nil, medusaTestKronos, medusaTestDevice(), medusaTestOptions()); err != nil {
		t.Fatalf("Medusa(nil body): %v", err)
	}
}

// TestMedusaDeterministic 相同输入必须产生相同输出。
func TestMedusaDeterministic(t *testing.T) {
	a, err := Medusa(medusaTestURL, []byte(medusaTestBody), medusaTestKronos, medusaTestDevice(), medusaTestOptions())
	if err != nil {
		t.Fatalf("Medusa: %v", err)
	}
	b, err := Medusa(medusaTestURL, []byte(medusaTestBody), medusaTestKronos, medusaTestDevice(), medusaTestOptions())
	if err != nil {
		t.Fatalf("Medusa: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("相同输入产生了不同输出")
	}
}

// TestHashF13Branches 覆盖 f13 的三个分支（0 / 2 / 其他）。
func TestHashF13Branches(t *testing.T) {
	seen := map[int]bool{}
	for k := uint32(0); k < 400 && len(seen) < 3; k++ {
		q := sm3Digest([]byte("q" + string(rune('a'+k%26))))
		b := md5Digest([]byte("b" + string(rune('a'+k%26))))
		ts := le32(k)
		sum := hashF13(q, b, ts, k)
		if len(sum) == 0 {
			t.Fatalf("hashF13 返回空，k=%d", k)
		}
		iv := getIV(getIV(getIV(0x20230928, q), b), ts)
		low := int(iv & 15)
		ivV0 := (low * 171) >> 9
		seen[low-ivV0*3] = true
	}
	if len(seen) < 3 {
		t.Fatalf("未覆盖到全部分支，仅 %v", seen)
	}
}

// TestAesV3RoundTripShape 验证 AesV3 输出长度与输入相关且非平凡。
func TestAesV3RoundTripShape(t *testing.T) {
	data := make([]byte, 300)
	for i := range data {
		data[i] = byte(i)
	}
	out := newAesV3(medusaAESKey, 1700000000).encrypt(data, medusaAESIV)
	if len(out) != len(data)+1 {
		t.Fatalf("AesV3 输出长度 = %d, want %d", len(out), len(data)+1)
	}
	same := true
	for i := 0; i < 32; i++ {
		if out[i] != data[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("AesV3 输出与明文前 32 字节相同，疑似未加密")
	}
}
