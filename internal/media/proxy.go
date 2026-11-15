// Package media 承载媒体代理：把上游片源以 HTTP Range 透传给浏览器内核。
//
// 本包只做标准 HTTP 反向代理：解析 Range、转发到上游、回填 206/Content-Range。
// 加密片源（CENC）需要解密管线，本包不实现，遇到时明确返回错误。
package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrEncrypted 表示片源加密，需要解密管线。
var ErrEncrypted = errors.New("加密片源需要解密管线")

// StreamSource 是解析后的片源。
type StreamSource struct {
	// URL 是上游可直连的媒体地址。
	URL string
	// Encrypted 表示片源受 CENC 保护。
	Encrypted bool
	// ContentKey 是解密所需的内容密钥（未实现解密时仅用于诊断）。
	ContentKey string
}

// Resolver 解析分集标识到片源。
type Resolver interface {
	ResolveStream(ctx context.Context, vid string) (StreamSource, error)
}

// Proxy 是单资源 HTTP 反向代理。
type Proxy struct {
	resolve Resolver
	client  *http.Client
}

// NewProxy 创建媒体代理。
func NewProxy(resolve Resolver) *Proxy {
	return &Proxy{
		resolve: resolve,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// maxRedirects 限制重定向跳数，避免代理被用作开放跳板。
const maxRedirects = 4

// Serve 处理一次媒体请求，把上游响应（含 Range 语义）透传给客户端。
//
// 返回的 error 仅在解析阶段失败时非 nil；响应写出失败已在内部处理。
func (p *Proxy) Serve(w http.ResponseWriter, r *http.Request, vid string) error {
	ctx := r.Context()

	src, err := p.resolve.ResolveStream(ctx, vid)
	if err != nil {
		return err
	}
	if src.Encrypted {
		return ErrEncrypted
	}
	if !isAllowedMediaURL(src.URL) {
		return errors.New("片源地址未通过安全检查")
	}

	target := src.URL
	for hop := 0; hop <= maxRedirects; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return fmt.Errorf("构造上游请求失败: %w", err)
		}
		// 透传 Range，支持拖拽 seek。
		if rg := r.Header.Get("Range"); rg != "" {
			req.Header.Set("Range", rg)
		}
		req.Header.Set("Accept-Encoding", "identity")

		resp, err := p.client.Do(req)
		if err != nil {
			return fmt.Errorf("上游媒体请求失败: %w", err)
		}

		// 跟随重定向（不把 Range 部分响应误判为最终结果）。
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			next, err := url.Parse(loc)
			if err != nil {
				return errors.New("上游重定向地址无效")
			}
			base, _ := url.Parse(target)
			target = base.ResolveReference(next).String()
			if !isAllowedMediaURL(target) {
				return errors.New("片源地址未通过安全检查")
			}
			continue
		}

		defer resp.Body.Close()
		copyMediaHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return nil
	}
	return errors.New("上游媒体重定向次数过多")
}

// copyMediaHeaders 只透传媒体相关响应头。
func copyMediaHeaders(dst, src http.Header) {
	for _, key := range []string{
		"Content-Type", "Content-Length", "Content-Range",
		"Accept-Ranges", "Last-Modified", "ETag", "Cache-Control",
	} {
		if v := src.Get(key); v != "" {
			dst.Set(key, v)
		}
	}
	if dst.Get("Accept-Ranges") == "" {
		dst.Set("Accept-Ranges", "bytes")
	}
}

// allowedMediaHosts 是可代理的媒体域名后缀。
//
// 与协议层一致：开发环境通过域名重定向接入，因此按域名校验即可。
// 需要接入其他 CDN 时在此追加。
var allowedMediaHosts = []string{
	"hongguoduanju.com",
	"novelquickapp.com",
	"fqnovel.com",
	"snssdk.com",
	"fqnovelpic.com",
	"qznovelvod.com",
	"fqnovelvod.com",
	"douyinvod.com",
	"bytecdn.cn",
	"byteimg.com",
	"volces.com",
	"ibytedtos.com",
	"bytedance.com",
}

// isAllowedMediaURL 校验媒体地址：https、无凭证、主机命中媒体白名单。
func isAllowedMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "https" {
		return false
	}
	if u.User != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || strings.Contains(host, "..") {
		return false
	}
	for _, d := range allowedMediaHosts {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
