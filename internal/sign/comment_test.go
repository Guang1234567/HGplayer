package sign

import "testing"

// 弹幕签名黄金向量：由 hongguo-open-player 的 danmaku-sign.js（GPL-3.0-only）
// 对同一组固定输入生成，用于逐位比对 Go 实现。
const (
	vectorQuery    = "aid=8662&device_id=1234567890123456&iid=9876543210987654&app_name=novelread&version_name=7.3.5.32"
	vectorStamp    = uint64(1700000000)
	vectorNonceHex = "0a141e28323c4450646e78" // 11 字节 nonce
)

func TestCommentXGorgonGoldenVector(t *testing.T) {
	nonce := mustHex(vectorNonceHex)
	got := commentXGorgon(vectorQuery, vectorStamp, nonce)
	want := "840480e40000f7465ed106ce6c68cdff223125a111c8c94b655d"
	if got != want {
		t.Fatalf("commentXGorgon = %s\nwant        %s", got, want)
	}
}

func TestCommentArgusGoldenVector(t *testing.T) {
	nonce := mustHex(vectorNonceHex)
	got := commentArgus(vectorQuery, vectorStamp, nonce)
	want := "PMxhgUKIXtho/6stSub0VASKJDoVY13IeJ5USEZ0fLHWQf4HmtrBlXFk/0m3SoPTM/xCJF0PBaKYdjKCe4ZIw1Pfw1mom55J46zie4neCKeWyIKUPt/CGM8nRj6/6jSdyStIo157SpXH7tAJBgOrLpSupx/6MmIZzNbSuY1uRv8rS9X/FIww8ZDAkBrPlUKeiKXPBtlK/2mH86uLvWQNp85IcIAFw1l7+dgKZC7HZDeGCA=="
	if got != want {
		t.Fatalf("commentArgus = %s\nwant        %s", got, want)
	}
}

func TestCommentLadonGoldenVector(t *testing.T) {
	nonce := mustHex(vectorNonceHex)
	got := commentLadon("8662", vectorStamp, nonce[2:6])
	want := "HigyPId1EgUt5Wh303H1LTt6P6+24x6LcxZblQMm+5Kv0+ba"
	if got != want {
		t.Fatalf("commentLadon = %s\nwant        %s", got, want)
	}
}

func TestCommentHeadersGoldenVector(t *testing.T) {
	nonce := mustHex(vectorNonceHex)
	headers := commentHeaders(vectorQuery, vectorStamp, nonce, "8662")

	if headers["X-Gorgon"] != "840480e40000f7465ed106ce6c68cdff223125a111c8c94b655d" {
		t.Errorf("X-Gorgon = %s", headers["X-Gorgon"])
	}
	if headers["X-Argus"] != "PMxhgUKIXtho/6stSub0VASKJDoVY13IeJ5USEZ0fLHWQf4HmtrBlXFk/0m3SoPTM/xCJF0PBaKYdjKCe4ZIw1Pfw1mom55J46zie4neCKeWyIKUPt/CGM8nRj6/6jSdyStIo157SpXH7tAJBgOrLpSupx/6MmIZzNbSuY1uRv8rS9X/FIww8ZDAkBrPlUKeiKXPBtlK/2mH86uLvWQNp85IcIAFw1l7+dgKZC7HZDeGCA==" {
		t.Errorf("X-Argus = %s", headers["X-Argus"])
	}
	if headers["X-Ladon"] != "HigyPId1EgUt5Wh303H1LTt6P6+24x6LcxZblQMm+5Kv0+ba" {
		t.Errorf("X-Ladon = %s", headers["X-Ladon"])
	}
	if headers["X-Khronos"] != "1700000000" {
		t.Errorf("X-Khronos = %s", headers["X-Khronos"])
	}
	if headers["X-SS-Req-Ticket"] != "1700000000000" {
		t.Errorf("X-SS-Req-Ticket = %s", headers["X-SS-Req-Ticket"])
	}
}

func TestBuildQuery(t *testing.T) {
	q := buildQuery(1700000000000, "1234567890123456", "9876543210987654")
	if q == "" {
		t.Fatal("buildQuery returned empty")
	}
	// 必须包含关键参数且按键字典序排列。
	if !containsStr(q, "aid=8662") {
		t.Errorf("query missing aid: %s", q)
	}
	if !containsStr(q, "device_id=1234567890123456") {
		t.Errorf("query missing device_id: %s", q)
	}
	if !containsStr(q, "_rticket=1700000000000") {
		t.Errorf("query missing _rticket: %s", q)
	}
}

func TestNewDeviceID(t *testing.T) {
	id := newDeviceID(mustHex("0102030405060708"))
	if len(id) != 19 {
		t.Fatalf("device id len = %d, want 19", len(id))
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			t.Fatalf("device id not numeric: %s", id)
		}
	}
}

func TestRandomNonceLength(t *testing.T) {
	n := randomNonce()
	if len(n) != nonceBytes {
		t.Fatalf("nonce len = %d, want %d", len(n), nonceBytes)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
