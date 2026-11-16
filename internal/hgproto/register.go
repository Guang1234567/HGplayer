package hgproto

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"hgplayer/internal/sign"
	"hgplayer/internal/upstream"
)

// 匿名设备注册。
//
// 平台按设备限制播放取址（用尽的设备会收到 HTTP 200 + 空响应体），
// 因此在役客户端会在取址失败时注册一台新设备并重试。这里复刻同一流程：
// 构造 ss_app_log 信封 → gzip → TT-Encrypt → POST /service/2/device_register/。
//
// 移植自 beilunyang/openhongguo（Apache-2.0）。

const (
	// registerHost 是设备注册域名（与在役客户端一致）。
	registerHost = "https://log5-applog-lq.fqnovel.com"
	// registerPath 是注册路径。
	registerPath = "/service/2/device_register/"
)

// 注册声明的机型档案（与取址请求使用的档案必须一致）。
const (
	profileModel      = "23127PN0CC"
	profileBrand      = "Xiaomi"
	profileOSVersion  = "14"
	profileOSAPI      = "34"
	profileResolution = "1200*2670"
	profileDPI        = "460"
	profileChannel    = "xiaomi_8662_64"
	profileBuild      = "UKQ1.230804.001"
)

// bootstrapHeaderOrder 是注册信封里 header 的字段顺序（签名/校验敏感）。
var bootstrapHeaderOrder = []string{
	"display_name", "update_version_code", "manifest_version_code", "app_version_minor",
	"aid", "channel", "package", "app_version", "version_code", "os", "os_version", "os_api",
	"device_model", "device_brand", "device_manufacturer", "cpu_abi", "language", "timezone",
	"access", "region", "tz_name", "tz_offset", "cdid", "openudid", "clientudid", "ipv6_list",
	"sim_serial_number", "oaid", "oaid_may_support", "req_id", "device_platform", "guest_mode",
	"is_system_app", "sdk_flavor", "device_id", "install_id",
}

// RegisterDevice 注册一台新匿名设备。
//
// 返回的设备带有注册下发的 device_id / install_id / ttreq 与机型档案；
// 调用方应把它持久化，后续所有请求都用同一份身份。
func (c *Client) RegisterDevice(ctx context.Context) (Device, error) {
	profile, err := newDeviceProfile()
	if err != nil {
		return Device{}, err
	}
	body, err := buildRegisterBody(profile, time.Now())
	if err != nil {
		return Device{}, err
	}

	query := url.Values{}
	query.Set("req_id", profile.reqID)
	query.Set("tt_data", "a")
	target := c.registerAPI + "?" + query.Encode()

	headers := map[string]string{
		"User-Agent":                profile.userAgent(),
		"Content-Type":              "application/octet-stream;tt-data=a",
		"log-encode-type":           "gzip",
		"sdk-version":               "2",
		"passport-sdk-version":      "5051452",
		"x-vc-bdturing-sdk-version": "4.0.3.cn",
		"x-ss-dp":                   playbackAID,
		"X-SS-STUB":                 sign.MD5Upper(body),
	}

	// 注册走不带播放签名的通道。
	resp, err := c.rawUpstream().Do(ctx, upstream.Request{
		Method: http.MethodPost, URL: target, Headers: headers, Body: body,
	})
	if err != nil {
		return Device{}, fmt.Errorf("设备注册失败：%w", err)
	}
	if resp.Status != http.StatusOK {
		return Device{}, fmt.Errorf("设备注册失败：上游返回 HTTP %d", resp.Status)
	}

	var parsed struct {
		DeviceID  json.RawMessage `json:"device_id_str"`
		InstallID json.RawMessage `json:"install_id_str"`
	}
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return Device{}, fmt.Errorf("设备注册响应无法解析：%w", err)
	}
	deviceID := rawIDString(parsed.DeviceID)
	installID := rawIDString(parsed.InstallID)
	if deviceID == "" || deviceID == "0" || installID == "" || installID == "0" {
		return Device{}, fmt.Errorf("设备注册未返回可用身份")
	}

	device := Device{
		DeviceID:    deviceID,
		IID:         installID,
		CDID:        profile.cdid,
		OpenUDID:    profile.openUDID,
		StoreRegion: regionOf(resp.Header.Get("Set-Cookie")),
		TTReq:       cookieValue(resp.Header.Get("Set-Cookie"), "ttreq"),
		Model:       profileModel, Brand: profileBrand, OSVersion: profileOSVersion, OSAPI: profileOSAPI,
		Resolution: profileResolution, DPI: profileDPI, Channel: profileChannel, Build: profileBuild,
		VersionCode: playbackVersionCode, VersionName: playbackVersionName,
		ManifestVersion: playbackVersionCode, UpdateVersion: playbackVersionCode, PVPlayer: playbackVersionCode,
	}
	return device, nil
}

// deviceProfile 是注册时生成的本机指纹（不含账号凭据）。
type deviceProfile struct {
	cdid       string
	openUDID   string
	clientUDID string
	oaid       string
	reqID      string
}

// newDeviceProfile 生成一份新的设备指纹。
func newDeviceProfile() (deviceProfile, error) {
	cdid, err := randomUUIDv4()
	if err != nil {
		return deviceProfile{}, err
	}
	openUDID, err := randomHex(8)
	if err != nil {
		return deviceProfile{}, err
	}
	clientUDID, err := randomUUIDv4()
	if err != nil {
		return deviceProfile{}, err
	}
	oaid, err := randomUUIDv4()
	if err != nil {
		return deviceProfile{}, err
	}
	reqID, err := randomUUIDv4()
	if err != nil {
		return deviceProfile{}, err
	}
	return deviceProfile{cdid: cdid, openUDID: openUDID, clientUDID: clientUDID, oaid: oaid, reqID: reqID}, nil
}

// userAgent 返回注册请求的 UA（与取址请求同一档案）。
func (p deviceProfile) userAgent() string {
	return playbackUserAgent(profileModel, profileOSVersion, profileBuild)
}

// buildRegisterBody 构造注册请求体：ss_app_log 信封 → gzip → TT-Encrypt。
func buildRegisterBody(profile deviceProfile, generatedAt time.Time) ([]byte, error) {
	// header 的键顺序是校验敏感字段，按固定顺序序列化。
	ordered, err := marshalOrdered(profile.header(generatedAt), bootstrapHeaderOrder)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(`{"magic_tag":"ss_app_log","header":`)
	buf.Write(ordered)
	buf.WriteString(`,"_gen_time":`)
	buf.WriteString(strconv.FormatInt(generatedAt.UnixMilli(), 10))
	buf.WriteByte('}')

	var gz bytes.Buffer
	writer, err := gzip.NewWriterLevel(&gz, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	writer.Header.ModTime = generatedAt.UTC()
	writer.Header.OS = 255
	if _, err := writer.Write(buf.Bytes()); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return sign.TTEncrypt(gz.Bytes()), nil
}

// header 返回注册信封的 header 字段。
func (p deviceProfile) header(now time.Time) map[string]any {
	return map[string]any{
		"display_name":          "红果免费短剧",
		"update_version_code":   73932,
		"manifest_version_code": 73932,
		"app_version_minor":     playbackVersionName,
		"aid":                   8662,
		"channel":               profileChannel,
		"package":               "com.phoenix.read",
		"app_version":           playbackVersionName,
		"version_code":          73932,
		"os":                    "Android",
		"os_version":            profileOSVersion,
		"os_api":                34,
		"device_model":          profileModel,
		"device_brand":          profileBrand,
		"device_manufacturer":   profileBrand,
		"cpu_abi":               "arm64-v8a",
		"language":              "zh",
		"timezone":              8,
		"access":                "wifi",
		"region":                "CN",
		"tz_name":               "Asia/Shanghai",
		"tz_offset":             28800,
		"cdid":                  p.cdid,
		"openudid":              p.openUDID,
		"clientudid":            p.clientUDID,
		"ipv6_list":             []any{},
		"sim_serial_number":     []any{},
		"oaid":                  p.bootstrapOAID(now.Add(-200 * time.Millisecond)),
		"oaid_may_support":      true,
		"req_id":                p.reqID,
		"device_platform":       "android",
		"guest_mode":            0,
		"is_system_app":         0,
		"sdk_flavor":            "china",
		"device_id":             "0",
		"install_id":            "0",
	}
}

// bootstrapOAID 是对象形式的 oaid 字段。
func (p deviceProfile) bootstrapOAID(issuedAt time.Time) map[string]any {
	return map[string]any{
		"req_id":             p.oaid,
		"hw_id_version_code": "0",
		"take_ms":            "6",
		"is_track_limited":   "false",
		"query_times":        "1",
		"id":                 p.oaid,
		"time":               strconv.FormatInt(issuedAt.UnixMilli(), 10),
	}
}

// marshalOrdered 按给定键序序列化 map，其余键按字典序补在后面。
func marshalOrdered(values map[string]any, preferred []string) ([]byte, error) {
	keys := make([]string, 0, len(values))
	seen := make(map[string]bool, len(preferred))
	for _, key := range preferred {
		if _, ok := values[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	rest := make([]string, 0)
	for key := range values {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)

	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		out.Write(name)
		out.WriteByte(':')
		value, err := json.Marshal(values[key])
		if err != nil {
			return nil, err
		}
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// rawIDString 把响应里的 id 字段转成字符串（可能是数字或字符串）。
func rawIDString(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	text = strings.Trim(text, `"`)
	if text == "null" {
		return ""
	}
	return text
}

// cookieValue 从 Set-Cookie 头中取一个 cookie 的值。
func cookieValue(header, name string) string {
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, name+"=") {
			return strings.TrimPrefix(part, name+"=")
		}
	}
	return ""
}

// regionOf 取注册响应里的 store-region，缺省 cn-hb。
func regionOf(header string) string {
	if region := cookieValue(header, "store-region"); region != "" {
		return region
	}
	return "cn-hb"
}

// randomUUIDv4 生成 v4 UUID。
// randomBytes 生成 n 字节随机数。
func randomBytes(n int) ([]byte, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func randomUUIDv4() (string, error) {
	raw, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	h := hex.EncodeToString(raw)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// randomHex 生成 n 字节的十六进制串。
func randomHex(n int) (string, error) {
	raw, err := randomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
