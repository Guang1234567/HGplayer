package hgproto

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"hgplayer/internal/sign"
)

// errInvalidCategory 表示目录分类不受支持。
var errInvalidCategory = errors.New("分类无效")

// metadataHost 是目录 / 详情类接口的域名。
const metadataHost = "https://api5-normal-sinfonlineb.fqnovel.com"

// metadataVersionName 是目录请求声明的客户端版本（同时用于 x-medusa 设备指纹）。
const metadataVersionName = "7.3.5.32"

// 目录分类的 req_scene 与 genre（与源实现一致）。
var categories = map[string][2]string{
	"real-drama":  {"default", "short_play"},
	"comic-drama": {"comic_series", "comic_series"},
	"ai-drama":    {"ai_series", "ai_series"},
}

// 榜单的 selected_items 与 sub_selected_items。
var rankings = map[string][2]string{
	"hot-drama":       {"all", "ranklist_hot_play_sc"},
	"hot-real-drama":  {"human", "human_hot_play"},
	"hot-comic-drama": {"comic_series_rank", "comic_series_hot_play"},
	"hot-ai-drama":    {"ai_playlet", "ai_playlet_hot_play"},
}

// kv 是有序键值对。上游签名对 query 的顺序敏感，必须保持插入序。
type kv struct{ k, v string }

// pythonEncode 对齐源实现的 pythonUrlEncode：
// 保留 A-Za-z0-9-_.~*，其余按 UTF-8 字节百分号编码（大写十六进制）。
func pythonEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '*':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0F])
		}
	}
	return b.String()
}

// encodeOrderedQuery 按插入序编码 query。
func encodeOrderedQuery(pairs []kv) string {
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(pythonEncode(p.k))
		b.WriteByte('=')
		b.WriteString(pythonEncode(p.v))
	}
	return b.String()
}

// metadataDeviceOrdered 返回目录请求的设备参数，顺序与源实现一致。
func (c *Client) metadataDeviceOrdered() []kv {
	return []kv{
		{"ac", "wifi"},
		{"channel", "update_64"},
		{"aid", "8662"},
		{"app_name", "novelread"},
		{"version_code", "73532"},
		{"version_name", metadataVersionName},
		{"device_platform", "android"},
		{"os", "android"},
		{"ssmix", "a"},
		{"device_type", "25053RT47C"},
		{"device_brand", "Redmi"},
		{"language", "zh"},
		{"os_api", "36"},
		{"os_version", "16"},
		{"manifest_version_code", "73532"},
		{"resolution", "1280*2772"},
		{"dpi", "520"},
		{"update_version_code", "73532"},
		{"host_abi", "arm64-v8a"},
		{"dragon_device_type", "phone"},
		{"pv_player", "73532"},
		{"compliance_status", "0"},
		{"need_personal_recommend", "1"},
		{"player_so_load", "1"},
		{"is_android_pad_screen", "0"},
		{"iid", c.device.IID},
		{"device_id", c.device.DeviceID},
		{"cdid", c.device.CDID},
	}
}

// metadataSigned 构造一次元数据请求：URL、请求头、请求体。
//
// 通过递增 _rticket 重试，直到 sign.BranchOf 为 0（与源实现一致）。
func (c *Client) metadataSigned(apiPath string, values []kv, payload []byte) (string, map[string]string, []byte) {
	khronos := uint32(timestamp())
	ticketBase := time.Now().UnixMilli()
	device := c.metadataDeviceOrdered()

	target := ""
	ticket := ""
	for n := 0; n < 128; n++ {
		ticket = strconv.FormatInt(ticketBase+int64(n), 10)

		pairs := make([]kv, 0, len(values)+len(device)+2)
		pairs = append(pairs, values...)
		pairs = append(pairs, device...)
		pairs = append(pairs, kv{"ts", strconv.FormatUint(uint64(khronos), 10)})
		pairs = append(pairs, kv{"_rticket", ticket})

		target = c.mdHost + apiPath + "?" + encodeOrderedQuery(pairs)
		if sign.BranchOf(target, payload, khronos) == 0 {
			break
		}
	}

	headers := map[string]string{
		"User-Agent":            strings.Replace(videoUA, "71332", "73532", 1),
		"Accept":                "application/json",
		"Accept-Encoding":       "identity",
		"x-xs-from-web":         "0",
		"sdk-version":           "2",
		"x-ss-dp":               "8662",
		"x-tt-store-region":     "cn",
		"x-tt-store-region-src": "did",
		"x-ss-req-ticket":       ticket,
		"x-khronos":             strconv.FormatUint(uint64(khronos), 10),
		"x-gorgon":              sign.Gorgon(queryOf(target), payload, khronos, sign.RandomUint16()),
		"x-ladon":               sign.Base64BE32(khronos),
		"x-argus":               sign.Base64LE32(khronos),
		"x-helios":              sign.Helios(khronos),
		"x-tt-dt":               "",
	}
	if medusa, err := sign.Medusa(target, payload, khronos, sign.MedusaDevice{
		DeviceID:    c.device.DeviceID,
		VersionName: metadataVersionName,
	}, sign.DefaultMedusaOptions()); err == nil {
		headers["x-medusa"] = string(medusa)
	}

	if len(payload) > 0 {
		headers["Content-Type"] = "application/json; charset=utf-8"
		headers["x-ss-stub"] = sign.MD5Upper(payload)
	}
	return target, headers, payload
}

// —— 目录请求体 ——

type selectItems struct {
	Genre            []string `json:"genre"`
	Sort             []string `json:"sort"`
	CategoryDimTheme []string `json:"category_dim_theme"`
	CategoryDimRole  []string `json:"category_dim_role"`
	CategoryDimEpoch []string `json:"category_dim_epoch"`
	Gender           []string `json:"gender"`
	OnlineTime       []string `json:"online_time"`
	CreationStatus   []string `json:"creation_status"`
}

type catalogBody struct {
	FilterIDs         string      `json:"filter_ids"`
	ReqScene          string      `json:"req_scene"`
	Offset            int         `json:"offset"`
	NeedSelectorPanel bool        `json:"need_selector_panel"`
	Limit             int         `json:"limit"`
	SelectItems       selectItems `json:"select_items"`
	SessionID         string      `json:"session_id"`
	ReqType           string      `json:"req_type"`
	ClientReqType     int         `json:"client_req_type"`
}

// buildCatalogBody 构造分类页请求体（字段顺序与源实现一致）。
func buildCatalogBody(category, genre string, offset int, sessionID string, page int) ([]byte, error) {
	cat, ok := categories[category]
	if !ok {
		return nil, errInvalidCategory
	}
	theme := []string{}
	if genre != "" {
		theme = []string{genre}
	}
	body := catalogBody{
		FilterIDs:         "",
		ReqScene:          cat[0],
		Offset:            offset,
		NeedSelectorPanel: page == 1,
		Limit:             15,
		SelectItems: selectItems{
			Genre:            []string{cat[1]},
			Sort:             []string{"hot_score"},
			CategoryDimTheme: theme,
			CategoryDimRole:  []string{},
			CategoryDimEpoch: []string{},
			Gender:           []string{},
			OnlineTime:       []string{},
			CreationStatus:   []string{},
		},
		SessionID:     sessionID,
		ReqType:       reqType(page),
		ClientReqType: 3,
	}
	return json.Marshal(body)
}

// reqType 返回分页请求类型。
func reqType(page int) string {
	if page == 1 {
		return "default"
	}
	return "only_content"
}
