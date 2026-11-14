// Package hgproto 实现红果短剧的上游协议层：目录、搜索、剧集详情与单集取址。
//
// 所有请求统一经 upstream 客户端发出，由 sign.Signer 注入鉴权头，
// guard 校验域名白名单。响应字段与源实现（hongguo-open-player）
// 的 JSON 结构对齐，以便前端直接复用。
package hgproto

// 端点常量。
const (
	// VideoAPI 多视频模型取址接口（主）。
	VideoAPI = "https://reading.snssdk.com/novel/player/multi_video_model/player/v1"
	// VideoAPIFallback 备用取址接口。
	VideoAPIFallback = "https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/player/v1"
	// ShortPlayAPI 单视频模型取址接口。
	ShortPlayAPI = "https://api3-normal-sinfonlinea.fqnovel.com/novel/player/video_model/v1/"
	// SearchAPI 搜索接口。
	SearchAPI = "https://api5-normal-sinfonlinea.fqnovel.com/reading/bookapi/search/tab/v"

	// 允许访问的上游域名（与源实现一致）。
	allowedDomains = "fqnovel.com,snssdk.com"
)

// 客户端默认 UA。
const (
	videoUA    = "com.phoenix.read/71332 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"
	playbackUA = "com.phoenix.read/74332 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"
	searchUA   = "com.phoenix.read/72232 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3)"
)

// Device 是上游请求携带的匿名设备身份与版本信息。
type Device struct {
	DeviceID        string
	IID             string
	CDID            string
	VersionCode     string
	VersionName     string
	ManifestVersion string
	UpdateVersion   string
	PVPlayer        string
}

// DefaultDevice 返回与源实现一致的默认设备身份。
func DefaultDevice(deviceID, iid, cdid string) Device {
	return Device{
		DeviceID:        deviceID,
		IID:             iid,
		CDID:            cdid,
		VersionCode:     "74332",
		VersionName:     "7.4.3.32",
		ManifestVersion: "74332",
		UpdateVersion:   "74332",
		PVPlayer:        "74332",
	}
}
