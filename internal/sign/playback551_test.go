package sign

import (
	"testing"
)

// 取址签名（Argus 551 / Ladon）的逐位对拍向量。
//
// 这些向量取自 beilunyang/openhongguo（Apache-2.0）的 playback_test.go：
// 它记录了同一线路协议的公开黄金向量，来源为本项目上游
// zfdang/hongguo-open-player PR #92（test/unit/argus.test.js）与
// aston314/hongguo-tv（testdata/playback-sign-vectors.json）。

// testArgus551 复刻取址调用方的取值方式（aid/device_id/iid 从 query 读取）。
func testArgus551(query string, body []byte, ts, nonce uint32) string {
	aid := queryValue(query, "aid")
	if aid == "" {
		aid = "1967"
	}
	return Argus551(body, query, ts, nonce, aid, queryValue(query, "device_id"), queryValue(query, "iid"))
}

func TestLadon551GoldenVectors(t *testing.T) {
	const nonce uint32 = 0x04030201
	cases := []struct {
		ts        uint32
		aid, want string
	}{
		{1_700_000_000, "1967", "AQIDBKM7Y7bWnOIUXmTybD6/MpGbuge4sBwUJsyHVivhtMsH"},
		{1_800_000_000, "8662", "AQIDBPhgjvKhYEWp2lodHDSBnITF8APHUjBLe2ja98bNrRn+"},
	}
	for _, c := range cases {
		if got := Ladon551(c.ts, nonce, c.aid); got != c.want {
			t.Fatalf("Ladon551 ts=%d aid=%s = %s, want %s", c.ts, c.aid, got, c.want)
		}
	}
}

func TestArgus551GoldenVectors(t *testing.T) {
	cases := []struct {
		query, body string
		ts, nonce   uint32
		want        string
	}{
		{
			query: "aid=1967&device_id=test-device&iid=test-install",
			ts:    1_700_000_000, nonce: 659662259,
			want: "8oGwZRLe2V38m+1gfqIeLa2MoV9ZBWosW5Y0NXKHAB2aEYRF86eNA7rLWm6xUCiwYMBDI/yq5ajsCmDF+Zws9KOfZO0mqezSYjF9/D5C1EliRxXJBBuZBaQZJgT6gakwiRnJkm+8F1ZVFIFO9GxkdwLxdxYidY2sLQjAsG8q4241ayJ/0zOxsVJNlqzPs7PfJb83zY6J1eIUaugTB6QnbXOKEQUgLhiYC0Uszr1vBP2Vj1cgtWu0IgouGM/ie3CohEAkY2oOkN/W1leWheBszijv",
		},
		{
			query: "aid=8662&device_id=7000000000000000001&iid=7000000000000000002&version_name=7.1.3.32&_rticket=1800000000999&ts=1800000000",
			body:  `{"video_id":"123","biz_param":{"video_platform":3}}`,
			ts:    1_800_000_000, nonce: 4294967295,
			want: "8oGzD48Lx0I+DCXDwk+l2r54SBRLSexXn2DlWQdGZE1UFZXj1GWup1ztpbF3eGeKcJAItaddhQwgYm7odM2byEYHtJ0pNvNUAoumWtQPqducerNIGe0Hs3z5V7oJDVgEySh6Kqc69jI8T+zU13SblzavSSZGSegC030jul66JPvjsBGzZoerKy88GDYDbumRNPCtqnfztLNNlGwmnA+0Tv+dnBC1xJ9rIEBThybfhq8wL5YqxxstLwuLGffOIlbxoQdheycfZBDpJG5kiK4eX7b3gr+PVkmCZBkJleZIYDl22UQS7nQGJKyu/a0U2MC7oWo=",
		},
		{
			query: "",
			ts:    1_700_000_000, nonce: 0,
			want: "8oHyrVYEy1xDCDlx64ko40qC6JC9VYY/ZkjJzEl50iSFDw8Yfph1hupLxfKrA9GswSEz0Yi4goA7OQ4u+H8ECdOaHRbHCQV+QFNgNz4TTRpioctLgrN97GbIKIQgZJjNRNiwbGqa+jIbLonncbTbsGIMv2WphwNMhXazKSewxik7/ecf0/SUOIiVd/3LhLMGeCIy+/ngOQr9VXnvmCb8HyzUbr01+pgwc1OKSfU29+FP4vYSq2WdmLCpvopUNQZhB8U=",
		},
	}
	for i, c := range cases {
		if got := testArgus551(c.query, []byte(c.body), c.ts, c.nonce); got != c.want {
			t.Fatalf("argus551[%d] 不匹配\n got %s\nwant %s", i, got, c.want)
		}
	}
}

// TestArgus551ExplicitFields 覆盖 device_id/iid 由调用方显式传入的情形。
func TestArgus551ExplicitFields(t *testing.T) {
	const query = "aid=1967&device_id=dev_aaaaaaaa&iid=111"
	const want = "8oHyrVYEy1xDCDlx64ko40qC6JC9VYY/ZkjJzEl50iSFDw8Yfph1hupLxfKrA9GswSEz0Yi4goA7OQ4u+H8ECdOaQDL4GduQ3LULSq9Q4iSEGbS3L6G24GXChtnmPr6n46M2PLlKpXvF7uE0lhH4yD+X69FpdM41gQwx5Y5U3NdwXFaxlqYFszyp7dlUE6BEBWqwDsiAH8Jk9TGKr/jfHt4fVmMc9zRyNRdGeV0x6aP//x7aV+8Po8a/9KK3UNG5KEpmIgzRT6NUttkBP9gRNUh3"

	got := Argus551(nil, query, 1_700_000_000, 659662259, "1967", "dev_aaaaaaaa", "")
	if got != want {
		t.Fatalf("argus551 不匹配\n got %s\nwant %s", got, want)
	}
	if len(got) < 64 || got[:2] != "8o" {
		t.Fatalf("x-argus 不是 551 信封（应以 8o 开头）: %s", got)
	}
}

// TestArgus551BindsEveryInput 每个输入都必须影响输出。
func TestArgus551BindsEveryInput(t *testing.T) {
	const query = "aid=8662&device_id=7000000000000000001&iid=7000000000000000002&version_name=7.3.9.32&ts=1700000000"
	body := []byte(`{"video_id":"7543196048185887806"}`)
	base := testArgus551(query, body, 1_700_000_000, 7)
	variants := map[string]string{
		"query":     testArgus551(query+"&extra=1", body, 1_700_000_000, 7),
		"body":      testArgus551(query, append(body, ' '), 1_700_000_000, 7),
		"timestamp": testArgus551(query, body, 1_700_000_001, 7),
		"nonce":     testArgus551(query, body, 1_700_000_000, 8),
	}
	for name, got := range variants {
		if got == base {
			t.Fatalf("argus551 忽略了 %s", name)
		}
	}
}

// TestRandomUint32 随机字段必须非退化。
func TestRandomUint32(t *testing.T) {
	seen := map[uint32]bool{}
	for i := 0; i < 8; i++ {
		seen[RandomUint32()] = true
	}
	if len(seen) < 2 {
		t.Fatal("RandomUint32 返回了常量")
	}
}
