// Package upstream 是受 guard 约束的上游 HTTP 客户端。
//
// 所有访问上游（短剧接口、CDN）的请求都统一经此处发出，
// 由 guard 校验域名白名单，并由 Signer 注入鉴权头。
// 上层协议代码不直接构造 http.Request，从而保证鉴权一致性与 SSRF 防护。
package upstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"hgplayer/internal/guard"
	"hgplayer/internal/sign"
)

// Options 是上游客户端配置。
type Options struct {
	// Guard 是域名白名单守卫，非空时校验每个请求。
	Guard *guard.Guard
	// Signer 是签名器，注入鉴权头。
	Signer sign.Signer
	// DeviceID / IID 是当前匿名设备身份，参与签名。
	DeviceID string
	IID      string
	// AccountToken 是可选的账号凭证；未登录时为空。
	AccountToken string
	// Timeout 是单次请求超时，默认 15s。
	Timeout time.Duration
}

// Client 是上游 HTTP 客户端。
type Client struct {
	opts Options
	http *http.Client
}

// New 创建客户端。
func New(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	return &Client{
		opts: opts,
		http: &http.Client{Timeout: opts.Timeout},
	}
}

// Request 是一次上游请求的描述。
type Request struct {
	Method string
	URL    string
	// Query 是参与签名的原始查询串（不含时间戳参数）。
	Query string
	Body  []byte
	// Headers 是调用方额外附加的头（如 User-Agent），
	// 鉴权头由 Signer 注入，不在此列。
	Headers map[string]string
}

// Response 是上游响应。
type Response struct {
	Status int
	Body   []byte
	Header http.Header
}

// Do 执行一次上游请求，返回响应。
//
// 流程：校验 URL → 签名注入鉴权头 → 发出请求 → 返回响应。
// 网络错误与被 guard 拒绝的错误以带 code 的错误返回，便于上层映射。
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	if c.opts.Guard != nil {
		if _, err := c.opts.Guard.CheckURL(req.URL); err != nil {
			return nil, fmt.Errorf("%w: %s", guard.ErrBlocked, req.URL)
		}
	}

	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return nil, fmt.Errorf("%w: 构造请求失败", ErrRequest)
	}

	// 注入调用方附加头。
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// 统一经 Signer 注入鉴权头。
	if c.opts.Signer != nil {
		for k, v := range c.opts.Signer.Sign(sign.Signature{
			Method:       req.Method,
			URL:          req.URL,
			Query:        req.Query,
			Body:         req.Body,
			DeviceID:     c.opts.DeviceID,
			IID:          c.opts.IID,
			AccountToken: c.opts.AccountToken,
		}) {
			httpReq.Header.Set(k, v)
		}
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: 请求超时或已取消", ErrTimeout)
		}
		return nil, fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: 读取响应失败", ErrNetwork)
	}

	return &Response{Status: resp.StatusCode, Body: raw, Header: resp.Header}, nil
}

// 上游错误码。
var (
	// ErrRequest 表示请求构造失败。
	ErrRequest = errors.New("上游请求无效")
	// ErrTimeout 表示请求超时或被取消。
	ErrTimeout = errors.New("上游请求超时")
	// ErrNetwork 表示网络层失败。
	ErrNetwork = errors.New("上游网络请求失败")
)
