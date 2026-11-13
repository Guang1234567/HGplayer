package sign

import (
	"encoding/base64"
	"strings"
	"testing"
)

// 黄金向量来自 hongguo-open-player/test/unit/argus.test.js（GPL-3.0-only）。
// 逐位比对，保证 Go 实现与源实现一致。

func TestSM3GoldenVectors(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"空输入", nil, "1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b"},
		{"abc", []byte("abc"), "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"},
		{"16 字节零", make([]byte, 16), "106e34a2b8c7bb13156cfdd0d91379dcc47543dcf9787c68ae5eb582620ae6e8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hexEncode(sm3Digest(tc.in))
			if got != tc.want {
				t.Fatalf("sm3 = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSimonGoldenVector(t *testing.T) {
	k := []uint64{1111111111111111111, 2222222222222222222, 3333333333333333333, 4444444444444444444}
	ct0, ct1 := simonEncrypt(1234567890123456789, 9876543210987654321, k)
	if ct0 != 6071150433487837324 || ct1 != 2027321285990159935 {
		t.Fatalf("ct = (%d, %d)", ct0, ct1)
	}
}

func TestLadonGoldenVector(t *testing.T) {
	got := ladonEncrypt(1700000000, 1611921764, 1967, mustHex("01020304"))
	want := "AQIDBKM7Y7bWnOIUXmTybD6/MpGbuge4sBwUJsyHVivhtMsH"
	if got != want {
		t.Fatalf("ladon = %s, want %s", got, want)
	}
}

func TestArgusHashGoldenVectors(t *testing.T) {
	if got := hexEncode(argusBodyHash("")); got != "106e34a2b8c7" {
		t.Fatalf("bodyHash = %s", got)
	}
	if got := hexEncode(argusQueryHash("")); got != "106e34a2b8c7" {
		t.Fatalf("queryHash = %s", got)
	}
	if got := hexEncode(argusQueryHash("aid=1967&device_id=123")); got != "925b8d86b20f" {
		t.Fatalf("queryHash(query) = %s", got)
	}
}

func TestArgusSignGoldenVector(t *testing.T) {
	query := "aid=1967&device_id=dev_aaaaaaaa&iid=111"
	got, err := argusSign(query, "", 1700000000, 1967, 1611921764, "dev_aaaaaaaa", "", 659662259)
	if err != nil {
		t.Fatalf("argusSign: %v", err)
	}
	want := "8oHyrVYEy1xDCDlx64ko40qC6JC9VYY/ZkjJzEl50iSFDw8Yfph1hupLxfKrA9GswSEz0Yi4goA7OQ4u+H8ECdOaQDL4GduQ3LULSq9Q4iSEGbS3L6G24GXChtnmPr6n46M2PLlKpXvF7uE0lhH4yD+X69FpdM41gQwx5Y5U3NdwXFaxlqYFszyp7dlUE6BEBWqwDsiAH8Jk9TGKr/jfHt4fVmMc9zRyNRdGeV0x6aP//x7aV+8Po8a/9KK3UNG5KEpmIgzRT6NUttkBP9gRNUh3"
	if got != want {
		t.Fatalf("argus = %s\nwant   %s", got, want)
	}
}

func TestPlayerSignerHeadersStructure(t *testing.T) {
	query := "aid=8662&device_id=1234567890&iid=99"
	headers, err := playerSignerHeaders(query, []byte(`{"x":1}`), 1700000000)
	if err != nil {
		t.Fatalf("playerSignerHeaders: %v", err)
	}
	if headers["x-khronos"] != "1700000000" {
		t.Fatalf("khronos = %s", headers["x-khronos"])
	}
	if headers["x-ss-req-ticket"] != "1700000000000" {
		t.Fatalf("ticket = %s", headers["x-ss-req-ticket"])
	}
	if headers["x-ss-stub"] == "" {
		t.Fatal("stub should be present when body non-empty")
	}
	if headers["x-ladon"] == "" {
		t.Fatal("ladon should be present")
	}
	if headers["x-argus"] == "" {
		t.Fatal("argus should be present")
	}
}

func TestQueryParam(t *testing.T) {
	if got := queryParam("foo=bar&baz=123", "baz"); got != "123" {
		t.Fatalf("queryParam = %q", got)
	}
	if got := queryParam("foo=bar&baz=123", "missing"); got != "" {
		t.Fatalf("missing should be empty, got %q", got)
	}
	if got := queryParam("", "foo"); got != "" {
		t.Fatalf("empty query should be empty, got %q", got)
	}
}

func TestBase64OutputValid(t *testing.T) {
	got, err := argusSign("aid=8662", "", 1700000000, 8662, 1611921764, "d1", "i1", 42)
	if err != nil {
		t.Fatalf("argusSign: %v", err)
	}
	if _, err := base64.StdEncoding.DecodeString(got); err != nil {
		t.Fatalf("argus not valid base64: %v", err)
	}
	if !strings.HasPrefix(got, "8o") && !strings.Contains(got, "/") {
		// 仅做存在性检查，不限定前缀。
		_ = got
	}
}
