package hgproto

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"hgplayer/internal/sign"
)

// searchVersionName 是搜索请求声明的客户端版本（hongguoTV 7.2.2.32 档案）。
const searchVersionName = "7.2.2.32"

// searchVersionCode 是搜索请求声明的版本号。
const searchVersionCode = "72232"

// searchDeviceOrdered 返回搜索请求的设备参数，顺序与源实现一致。
func (c *Client) searchDeviceOrdered() []kv {
	return []kv{
		{"ac", "wifi"},
		{"channel", "update_64"},
		{"aid", "8662"},
		{"app_name", "novelread"},
		{"version_code", searchVersionCode},
		{"version_name", searchVersionName},
		{"device_platform", "android"},
		{"os", "android"},
		{"ssmix", "a"},
		{"device_type", "25053RT47C"},
		{"device_brand", "Redmi"},
		{"language", "zh"},
		{"os_api", "36"},
		{"os_version", "16"},
		{"manifest_version_code", searchVersionCode},
		{"resolution", "1280*2772"},
		{"dpi", "520"},
		{"update_version_code", searchVersionCode},
		{"host_abi", "arm64-v8a"},
		{"dragon_device_type", "phone"},
		{"pv_player", searchVersionCode},
		{"compliance_status", "0"},
		{"need_personal_recommend", "1"},
		{"player_so_load", "1"},
		{"is_android_pad_screen", "0"},
		{"iid", c.device.IID},
		{"device_id", c.device.DeviceID},
		{"cdid", c.device.CDID},
	}
}

// searchApp 通过 App 端签名接口搜索（官网链路失败后的兜底）。
func (c *Client) searchApp(ctx context.Context, keyword string, page int) ([]SeriesCard, error) {
	target, headers, err := c.searchSigned(keyword, page)
	if err != nil {
		return nil, err
	}

	payload, err := c.doJSONRaw(ctx, http.MethodGet, target, nil, headers)
	if err != nil {
		return nil, err
	}
	return parseSearchPayload(payload)
}

// searchSigned 构造一次搜索请求：URL 与鉴权头。
//
// 搜索与目录一样需要 x-gorgon / x-helios / x-medusa 等完整签名头，
// 并通过递增 _rticket 重试直到 sign.BranchOf 为 0（与源实现一致）。
func (c *Client) searchSigned(keyword string, page int) (string, map[string]string, error) {
	khronos := uint32(timestamp())
	ticketBase := time.Now().UnixMilli()
	device := c.searchDeviceOrdered()
	offset := strconv.Itoa((page - 1) * 20)

	target := ""
	rawQuery := ""
	ticket := ""
	settled := false
	for n := 0; n < 128; n++ {
		ticket = strconv.FormatInt(ticketBase+int64(n), 10)

		pairs := make([]kv, 0, len(device)+9)
		pairs = append(pairs, device...)
		pairs = append(pairs,
			kv{"query", keyword},
			kv{"count", "20"},
			kv{"tab_type", "11"},
			kv{"bookshelf_search_plan", "4"},
			kv{"use_correct", "true"},
			kv{"user_is_login", "0"},
			kv{"offset", offset},
			kv{"ts", strconv.FormatUint(uint64(khronos), 10)},
			kv{"_rticket", ticket},
		)

		rawQuery = encodeOrderedQuery(pairs)
		target = c.searchAPI + "?" + rawQuery
		if sign.BranchOf(target, nil, khronos) == 0 {
			settled = true
			break
		}
	}
	if !settled {
		return "", nil, fmt.Errorf("无法生成搜索请求签名")
	}

	headers := map[string]string{
		"User-Agent":           searchUA,
		"Accept":               "application/json; charset=utf-8,application/x-protobuf",
		"x-ss-req-ticket":      ticket,
		"x-tt-request-tag":     "t=0;n=0",
		"sdk-version":          "2",
		"passport-sdk-version": "50561",
		"x-xs-from-web":        "0",
		"x-khronos":            strconv.FormatUint(uint64(khronos), 10),
		"x-ladon":              sign.Base64BE32(khronos),
		"x-argus":              sign.Base64LE32(khronos),
		"x-gorgon":             sign.Gorgon(rawQuery, nil, khronos, sign.RandomUint16()),
		"x-helios":             sign.Helios(khronos),
		"x-tt-dt":              "",
	}
	if medusa, err := sign.Medusa(target, nil, khronos, sign.MedusaDevice{
		DeviceID:    c.device.DeviceID,
		VersionName: searchVersionName,
	}, sign.DefaultMedusaOptions()); err == nil {
		headers["x-medusa"] = string(medusa)
	}
	return target, headers, nil
}
