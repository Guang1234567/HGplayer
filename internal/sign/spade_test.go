package sign

import (
	"encoding/base64"
	"strings"
	"testing"
)

// 以下向量全部由源实现（hongguo-open-player/src/server/native/app-source.js）
// 生成，用于 spade 算法逐位对拍。

// TestDeriveContentKeyMatchesSource 校验 spade_a → 内容密钥的派生结果。
func TestDeriveContentKeyMatchesSource(t *testing.T) {
	// base64url 字母表（- / _）与标准字母表应得到同一把密钥。
	urlAlphabet := strings.NewReplacer("+", "-", "/", "_").Replace(sourceSpadeAInput)

	cases := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "源向量 A", input: sourceSpadeAInput, want: sourceSpadeAKey},
		{name: "源向量 B（含 - 与 _）", input: sourceSpadeBInput, want: sourceSpadeBKey},
		{name: "base64url 变体", input: urlAlphabet, want: sourceSpadeAKey},
		{name: "去填充", input: strings.TrimRight(sourceSpadeAInput, "="), want: sourceSpadeAKey},
		{name: "空输入", input: "", wantErr: "spade_a 长度不足"},
		{name: "长度不足", input: "AAAA", wantErr: "spade_a 密钥长度无效"},
		{name: "派生结果非十六进制", input: strings.Repeat("A", 80), wantErr: "App 视频密钥格式无效"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := SpadeKeyDeriver{}.DeriveContentKey([]byte(tc.input))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DeriveContentKey: %v", err)
			}
			if got := hexEncode(key); got != tc.want {
				t.Fatalf("key = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestDeriveContentKeyViaInterface 确认实现可作为 KeyDeriver 使用。
func TestDeriveContentKeyViaInterface(t *testing.T) {
	var deriver KeyDeriver = SpadeKeyDeriver{}
	key, err := deriver.DeriveContentKey([]byte(sourceSpadeAInput))
	if err != nil {
		t.Fatalf("DeriveContentKey: %v", err)
	}
	if len(key) != 16 {
		t.Fatalf("key length = %d, want 16", len(key))
	}
}

// TestDecryptSpadeURLMatchesSource 校验 spade 地址解密结果。
func TestDecryptSpadeURLMatchesSource(t *testing.T) {
	seed := decodeSeed(t, sourceSpadeSeed)

	cases := []struct {
		name    string
		value   string
		seed    []byte
		want    string
		wantErr string
	}{
		{name: "源向量 C", value: sourceSpadeCipher, seed: seed, want: sourceSpadePlain},
		{name: "源向量 D（base64url 且无填充）", value: sourceSpadeCipherB, seed: seed, want: sourceSpadePlainB},
		{name: "源向量 E（尾部 0 填充）", value: sourceSpadeCipherZ, seed: seed, want: sourceSpadePlainZ},
		{name: "空密文", value: "", seed: seed, want: ""},
		{name: "密文长度不足", value: base64.StdEncoding.EncodeToString([]byte{0xA8, 0x00, 0x01, 0x00}), seed: seed, wantErr: "spade 地址密文长度不足"},
		{name: "头部格式错误", value: base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4, 5, 6}), seed: seed, wantErr: "spade 地址头部格式错误"},
		{name: "缺少完整分组", value: base64.StdEncoding.EncodeToString([]byte{0xA8, 0x00, 0x01, 0x00, 1, 2, 3}), seed: seed, wantErr: "spade 地址密文缺少完整分组"},
		{name: "种子不对时得到乱码", value: sourceSpadeCipher, seed: decodeSeed(t, "AAAAAAAAAAA="), want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecryptSpadeURL(tc.value, tc.seed)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecryptSpadeURL: %v", err)
			}
			if tc.name == "种子不对时得到乱码" {
				// 错误种子下明文不再等于原地址（此处只要求不抛错且不等于原文）。
				if got == sourceSpadePlain {
					t.Fatal("错误种子不应解出原地址")
				}
				return
			}
			if got != tc.want {
				t.Fatalf("plaintext = %q, want %q", got, tc.want)
			}
		})
	}
}

// decodeSeed 解析测试用的 key_seed。
func decodeSeed(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("解析种子失败: %v", err)
	}
	return raw
}

// 源实现生成的固定向量。
const (
	sourceSpadeAInput  = "EzATTFs3EkxZNRJMXzQRZUErDmVfKCZlXS4mZVsvJWWlI/h9oST6eKQ="
	sourceSpadeAKey    = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	sourceSpadeBInput  = "IjAid1s_IXVdPyFzXz4XcV0_FG9fFxNtXRcTa0MWEmlBLxxxRSgedEA="
	sourceSpadeBKey    = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	sourceSpadeSeed    = "ASNFZ4mrze8="
	sourceSpadeCipher  = "qH8BAOae6GzNvz0KWjRKfJH2A9ItFUTy2Pl2/kq980hRu1R+X3Yo/EvDFY5/nvMGHfTa6w=="
	sourceSpadePlain   = "https://qznovelvod.com/video_30002.mp4"
	sourceSpadeCipherB = "qBEBAEWYW92Oag0pZwy3gOu6cEQA52rjbnAcdSFFMfb2zcka23pScrzZez0Exdb057-5-fXkiwI7Y03g_m7YY4NscNI"
	sourceSpadePlainB  = "https://douyinvod.com/91d56f7e-c027-4ce9-9a3c-229d59a13c3b"
	sourceSpadeCipherZ = "qAIBACWb0A+w3OY629z09+iLhUuDPx+/P8g61CXa1R3lv7VS"
	sourceSpadePlainZ  = "https://fqnovelvod.com/a.mp4"
)
