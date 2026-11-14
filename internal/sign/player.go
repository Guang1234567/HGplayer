package sign

import (
	"net/url"
	"time"
)

// PlayerSigner 是基于播放签名的真实 Signer 实现。
//
// 所有上游请求统一经此注入 x-khronos / x-ss-req-ticket / x-ladon /
// x-argus / x-ss-stub 鉴权头。设备身份从签名输入的 query 中读取，
// 与源实现（argus.js PlayerSigner.headers）行为一致。
type PlayerSigner struct{}

// Sign 实现 Signer 接口。
func (PlayerSigner) Sign(sig Signature) Headers {
	ts := time.Now().Unix()

	// 源实现的签名输入是 query 字符串；设备身份需在 query 中携带。
	// 若调用方未把 device_id/iid 放进 query，则从 URL 提取后补齐。
	query := sig.Query
	if query == "" {
		query = extractQuery(sig.URL)
	}
	query = ensureIdentity(query, sig.DeviceID, sig.IID)

	headers, err := playerSignerHeaders(query, sig.Body, ts)
	if err != nil {
		return Headers{}
	}
	return Headers(headers)
}

// extractQuery 从 URL 中提取查询串。
func extractQuery(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.RawQuery
}

// ensureIdentity 确保 query 中携带 device_id 与 iid。
func ensureIdentity(query, deviceID, iid string) string {
	if deviceID == "" && iid == "" {
		return query
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		values = url.Values{}
	}
	if deviceID != "" {
		values.Set("device_id", deviceID)
	}
	if iid != "" {
		values.Set("iid", iid)
	}
	return values.Encode()
}

// 编译期确认 PlayerSigner 满足 Signer 接口。
var _ Signer = PlayerSigner{}
