package sign

import (
	"strings"
	"testing"
)

// PlayerSigner 产出的鉴权头应与源实现结构一致。
func TestPlayerSignerProducesAuthHeaders(t *testing.T) {
	s := PlayerSigner{}
	sig := Signature{
		Method:   "GET",
		URL:      "https://reading.snssdk.com/novel/player/multi_video_model/player/v1",
		DeviceID: "1624456208323876",
		IID:      "1624456208327972",
	}
	headers := s.Sign(sig)

	for _, key := range []string{"x-khronos", "x-ss-req-ticket", "x-ladon", "x-argus"} {
		if headers[key] == "" {
			t.Errorf("missing header %s", key)
		}
	}
	if headers["x-ss-req-ticket"] != strings.Repeat("", 0) {
		// ticket 必须是毫秒时间戳字符串。
		if len(headers["x-ss-req-ticket"]) < 10 {
			t.Errorf("ticket looks invalid: %q", headers["x-ss-req-ticket"])
		}
	}
}

// ensureIdentity 应把设备身份注入 query。
func TestEnsureIdentity(t *testing.T) {
	got := ensureIdentity("aid=8662&version_name=7.1.3.32", "1624456208323876", "1624456208327972")
	if !strings.Contains(got, "device_id=1624456208323876") {
		t.Errorf("query missing device_id: %s", got)
	}
	if !strings.Contains(got, "iid=1624456208327972") {
		t.Errorf("query missing iid: %s", got)
	}

	// 已有身份时不重复注入。
	got2 := ensureIdentity("aid=8662&device_id=111&iid=222", "111", "222")
	if strings.Count(got2, "device_id=") != 1 {
		t.Errorf("device_id duplicated: %s", got2)
	}
}

// 签名头值应是稳定的十六进制/base64 字符串（不含随机噪声导致长度异常）。
func TestSignerHeaderValueSanity(t *testing.T) {
	s := PlayerSigner{}
	headers := s.Sign(Signature{URL: "https://x.snssdk.com/y", DeviceID: "1", IID: "2"})
	if len(headers["x-argus"]) < 20 {
		t.Errorf("argus too short: %q", headers["x-argus"])
	}
	if len(headers["x-ladon"]) < 10 {
		t.Errorf("ladon too short: %q", headers["x-ladon"])
	}
}
