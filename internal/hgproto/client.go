package hgproto

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"hgplayer/internal/guard"
	"hgplayer/internal/sign"
	"hgplayer/internal/upstream"
)

// Client 是红果协议客户端，封装上游请求并注入签名与设备身份。
type Client struct {
	// up 会自动注入播放器签名头（x-ladon/x-argus 等）。
	up *upstream.Client
	// upRaw 不注入签名头，用于自建头部（元数据 / 搜索）的请求。
	upRaw  *upstream.Client
	device Device
	signer sign.Signer
	guard  *guard.Guard

	// 端点（可覆盖，便于测试）。
	videoAPI         string
	videoAPIFallback string
	shortPlayAPI     string
	searchAPI        string
	// mdHost 是目录 / 详情类接口的域名。
	mdHost string
	// websiteAPI 是官网搜索接口。
	websiteAPI string
	// siteOrigin 是官网站点根地址（页面与接口同源）。
	siteOrigin string
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
	c := &Client{guard: g, device: device, signer: signer}
	c.rebuildClients(g)
	c.videoAPI = VideoAPI
	c.videoAPIFallback = VideoAPIFallback
	c.shortPlayAPI = ShortPlayAPI
	c.searchAPI = SearchAPI
	c.mdHost = metadataHost
	c.websiteAPI = websiteSearchAPI
	c.siteOrigin = websiteOrigin

	for _, opt := range opts {
		opt(c)
	}
	if c.guard == nil {
		c.rebuildClients(nil)
	}
	return c
}

// rebuildClients 按给定守卫重建两条上游通道。
func (c *Client) rebuildClients(g *guard.Guard) {
	c.up = upstream.New(upstream.Options{
		Guard: g, Signer: c.signer, DeviceID: c.device.DeviceID, IID: c.device.IID, Timeout: 15 * time.Second,
	})
	c.upRaw = upstream.New(upstream.Options{
		Guard: g, DeviceID: c.device.DeviceID, IID: c.device.IID, Timeout: 15 * time.Second,
	})
}

// doJSON 发起上游请求（自动注入播放器签名头）并解析 JSON 响应。
func (c *Client) doJSON(ctx context.Context, method, target string, body []byte, headers map[string]string) (map[string]any, error) {
	return c.doJSONWith(c.up, ctx, method, target, body, headers)
}

// doJSONRaw 发起上游请求（不注入签名头，由调用方自建头部）并解析 JSON。
func (c *Client) doJSONRaw(ctx context.Context, method, target string, body []byte, headers map[string]string) (map[string]any, error) {
	return c.doJSONWith(c.upRaw, ctx, method, target, body, headers)
}

// doRawBody 取原始响应体（不解析 JSON），用于官网服务端渲染页面。
func (c *Client) doRawBody(ctx context.Context, target string, headers map[string]string) ([]byte, error) {
	resp, err := upstream.DoWithRetry(ctx, func(ctx context.Context) (*upstream.Response, error) {
		return c.upRaw.Do(ctx, upstream.Request{
			Method:  http.MethodGet,
			URL:     target,
			Query:   queryOf(target),
			Headers: headers,
		})
	}, upstream.DefaultRetryPolicy())
	if err != nil {
		return nil, err
	}
	if resp.Status != 200 {
		return nil, fmt.Errorf("上游返回 HTTP %d", resp.Status)
	}
	if len(bytes.TrimSpace(resp.Body)) == 0 {
		return nil, fmt.Errorf("上游返回空响应")
	}
	return resp.Body, nil
}

// doJSONWith 是两条通道共用的请求执行逻辑。
func (c *Client) doJSONWith(client *upstream.Client, ctx context.Context, method, target string, body []byte, headers map[string]string) (map[string]any, error) {
	resp, err := upstream.DoWithRetry(ctx, func(ctx context.Context) (*upstream.Response, error) {
		return client.Do(ctx, upstream.Request{
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
	if len(bytes.TrimSpace(resp.Body)) == 0 {
		return nil, fmt.Errorf("上游返回空响应")
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

// timestamp 返回当前 Unix 秒。
func timestamp() int64 { return time.Now().Unix() }
