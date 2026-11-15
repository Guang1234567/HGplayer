package hgproto

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hgplayer/internal/guard"
	"hgplayer/internal/sign"
	"hgplayer/internal/upstream"
)

// Client 是红果协议客户端，封装上游请求并注入签名与设备身份。
type Client struct {
	up      *upstream.Client
	device  Device
	signer  sign.Signer
	guard   *guard.Guard

	videoAPI    string
	searchAPI   string
	seriesAPI   string
	categoryAPI string
}

// Option 是客户端的可选项。
type Option func(*Client)

// WithGuard 指定域名守卫；传入 nil 时跳过校验（用于测试）。
func WithGuard(g *guard.Guard) Option {
	return func(c *Client) { c.guard = g }
}

// NewClient 创建协议客户端。
func NewClient(signer sign.Signer, device Device, opts ...Option) *Client {
	g := guard.New(AllowedDomains...)
	c := &Client{
		guard:       g,
		up:          upstream.New(upstream.Options{Guard: g, Signer: signer, DeviceID: device.DeviceID, IID: device.IID, Timeout: 15 * time.Second}),
		device:      device,
		signer:      signer,
		videoAPI:    VideoAPI,
		searchAPI:   SearchAPI,
		seriesAPI:   "https://hongguoduanju.com/reading/bookapi/bookinfo/v/",
		categoryAPI: "https://hongguoduanju.com/reading/distribution/category/landpage/v/",
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.guard == nil {
		c.up = upstream.New(upstream.Options{Signer: signer, DeviceID: device.DeviceID, IID: device.IID, Timeout: 15 * time.Second})
	}
	return c
}


// doJSON 发起上游请求并解析 JSON 响应。
func (c *Client) doJSON(ctx context.Context, method, target string, body []byte, headers map[string]string) (map[string]any, error) {
	resp, err := upstream.DoWithRetry(ctx, func(ctx context.Context) (*upstream.Response, error) {
		return c.up.Do(ctx, upstream.Request{
			Method:  method,
			URL:     target,
			Query:   queryOf(target),
			Body:    body,
			Headers: headers,
		})
	}, upstream.DefaultRetryPolicy())
	if err != nil {
		return nil, err
	}
	if resp.Status != 200 {
		return nil, fmt.Errorf("上游返回 HTTP %d", resp.Status)
	}
	var parsed map[string]any
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return nil, fmt.Errorf("解析上游响应失败: %w", err)
	}
	return parsed, nil
}

// queryOf 从 URL 提取原始查询串。
func queryOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.RawQuery
}

// buildQuery 把参数按字典序编码为 query 串（与源实现一致）。
func buildQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, quotePlus(k)+"="+quotePlus(params[k]))
	}
	return strings.Join(pairs, "&")
}

// quotePlus 对齐 Python urlencode（quote_plus）。
func quotePlus(value string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0xF])
	}
	return strings.ReplaceAll(b.String(), "%20", "+")
}

// sortStrings 字典序排序。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// deviceParams 返回设备的 query 参数。
func (c *Client) deviceParams(extra map[string]string) map[string]string {
	params := map[string]string{
		"ac":                  "wifi",
		"channel":             "update_64",
		"aid":                 "8662",
		"app_name":            "novelread",
		"version_code":        c.device.VersionCode,
		"version_name":        c.device.VersionName,
		"device_platform":     "android",
		"os":                  "android",
		"ssmix":               "a",
		"device_type":         "25053RT47C",
		"device_brand":        "Redmi",
		"language":            "zh",
		"os_api":              "36",
		"os_version":          "16",
		"resolution":          "1280*2772",
		"dpi":                 "520",
		"manifest_version_code": c.device.ManifestVersion,
		"update_version_code":  c.device.UpdateVersion,
		"pv_player":           c.device.PVPlayer,
		"device_id":           c.device.DeviceID,
		"iid":                 c.device.IID,
		"cdid":                c.device.CDID,
	}
	for k, v := range extra {
		params[k] = v
	}
	return params
}

// timestamp 返回当前 Unix 秒。
func timestamp() int64 { return time.Now().Unix() }

// itoa 转十进制字符串。
func itoa(v int64) string { return strconv.FormatInt(v, 10) }
