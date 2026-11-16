// Package media 承载媒体代理：把上游片源以 HTTP Range 供给浏览器内核。
//
// 未加密片源走标准反向代理，Range 原样透传；CENC 加密片源走窗口式解密：
// 先用头部窗口（moov）建立「明文 = 上游 + Shift」的布局，再把浏览器请求的区间
// 映射成上游字节区间，只取需要的部分、只解密覆盖到的样本，并按固定窗口缓存结果
// （见 window.go）。布局不可用（moov 不在头部、上游不支持 Range）时退回
// 「整段下载 → 解密 → 按 Range 回流」。
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"hgplayer/internal/cenc"
)

// ErrEncrypted 表示片源加密但缺少可用的解密能力（例如没有内容密钥）。
var ErrEncrypted = errors.New("加密片源缺少解密能力")

// ErrResponseStarted 表示响应已写出，调用方不应再改状态码或改写错误响应。
var ErrResponseStarted = errors.New("响应已开始")

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

// Proxy 是单资源 HTTP 反代。
type Proxy struct {
	resolve Resolver
	client  *http.Client
	// decrypt 是 CENC 解密实现；测试可替换。
	decrypt func(data, key []byte) ([]byte, error)
	// cache 缓存解密后的整段媒体。
	cache *plainCache
	// allowHost 校验片源域名；测试可替换。
	allowHost func(string) bool
	// plans 缓存窗口式解密的布局。
	plans *planCache
	// windows 缓存已解密的明文窗口。
	windows *windowCache
	// fetchMu 串行化窗口抓取，避免并发请求重复下载同一区间。
	fetchMu sync.Mutex
}

// errNoContentKey 表示加密片源没有可用的内容密钥。
var errNoContentKey = errors.New("加密片源缺少内容密钥")

// NewProxy 创建媒体代理。
func NewProxy(resolve Resolver) *Proxy {
	return &Proxy{
		resolve:   resolve,
		client:    &http.Client{Timeout: 60 * time.Second},
		decrypt:   cenc.Decrypt,
		cache:     newPlainCache(plainCacheLimit, plainCacheMaxBytes),
		allowHost: isAllowedMediaURL,
		plans:     newPlanCache(),
		windows:   newWindowCache(windowCacheMaxBytes),
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
	if !p.allow(src.URL) {
		return errors.New("片源地址未通过安全检查")
	}
	if src.Encrypted {
		return p.serveEncrypted(w, r, vid, src)
	}

	resp, err := p.getUpstream(ctx, src.URL, r.Header.Get("Range"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	copyMediaHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return nil
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

// allow 校验片源域名（默认使用白名单；测试可替换）。
func (p *Proxy) allow(raw string) bool {
	if p.allowHost != nil {
		return p.allowHost(raw)
	}
	return isAllowedMediaURL(raw)
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

// —— CENC 加密片源 ——

const (
	// plainCacheLimit 是解密结果缓存的条目数上限。
	plainCacheLimit = 2
	// plainCacheMaxBytes 是解密结果缓存的总字节上限（约两集 1080p）。
	plainCacheMaxBytes = 96 << 20
)

// serveEncrypted 供给加密片源。
//
// 优先窗口式：探测头部窗口建立布局后，按浏览器请求的区间只取上游对应字节、
// 只解密覆盖到的样本。布局不可用（moov 不在头部、上游不支持 Range 等）时
// 退回「整段下载 → 解密 → 按 Range 回流」的老路径。
func (p *Proxy) serveEncrypted(w http.ResponseWriter, r *http.Request, vid string, src StreamSource) error {
	key, err := decodeContentKey(src.ContentKey)
	if err != nil {
		return err
	}

	plan, prefetched, _ := p.planFor(r.Context(), vid, src)
	if plan != nil {
		return p.serveWindows(w, r, vid, src, plan, key)
	}
	if ctxErr := r.Context().Err(); ctxErr != nil {
		return ctxErr
	}
	// 布局不可用（moov 不在头部、上游不支持 Range 等）时静默退回整段路径。
	return p.serveEncryptedWhole(w, r, vid, src, key, prefetched)
}

// serveEncryptedWhole 整段下载并解密后按 Range 回流（兜底路径）。
func (p *Proxy) serveEncryptedWhole(w http.ResponseWriter, r *http.Request, vid string, src StreamSource, key []byte, prefetched []byte) error {
	plain, err := p.plainMedia(r.Context(), vid, src, key, prefetched)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, vid+".mp4", time.Time{}, bytes.NewReader(plain))
	return nil
}

// plainMedia 返回解密后的整段媒体（命中缓存时不再请求上游）。
//
// prefetched 非空表示调用方已取得整段加密数据（例如上游忽略了 Range 探测）。
func (p *Proxy) plainMedia(ctx context.Context, vid string, src StreamSource, key []byte, prefetched []byte) ([]byte, error) {
	fingerprint := mediaFingerprint(src.URL, src.ContentKey)
	if cached, ok := p.cache.get(vid, fingerprint); ok {
		return cached, nil
	}

	cipher := prefetched
	if cipher == nil {
		var err error
		cipher, err = p.fetchAll(ctx, src.URL)
		if err != nil {
			return nil, err
		}
	}
	plain, err := p.decrypt(cipher, key)
	if err != nil {
		return nil, fmt.Errorf("媒体解密失败: %w", err)
	}
	p.cache.put(vid, fingerprint, plain)
	return plain, nil
}

// fetchAll 下载整段加密媒体（不使用 Range）。
func (p *Proxy) fetchAll(ctx context.Context, target string) ([]byte, error) {
	resp, err := p.getUpstream(ctx, target, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游媒体返回 HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// decodeContentKey 解析内容密钥（十六进制，16 字节）。
func decodeContentKey(value string) ([]byte, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil, errNoContentKey
	}
	key, err := hex.DecodeString(text)
	if err != nil || len(key) != 16 {
		return nil, errNoContentKey
	}
	return key, nil
}

// mediaFingerprint 标识一份片源，用于缓存失效判断。
//
// 上游地址每次取址都会带新的签名与过期时间，因此只用其中的路径
// （稳定标识分集文件）与内容密钥；密钥或文件变了才算另一份片源。
func mediaFingerprint(rawURL, key string) string {
	stable := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		// 主机与路径前段都是每次取址轮换的 CDN/令牌，最后一段才是文件标识。
		segments := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i := len(segments) - 1; i >= 0; i-- {
			if segments[i] != "" {
				stable = segments[i]
				break
			}
		}
	}
	sum := sha256.Sum256([]byte(stable + "\x00" + key))
	return hex.EncodeToString(sum[:8])
}

// plainCache 是解密结果的按 vid 缓存（带指纹校验与总字节上限）。
type plainCache struct {
	mu        sync.Mutex
	limit     int
	maxBytes  int
	totalSize int
	entries   map[string]*plainEntry
	order     []string
}

// plainEntry 是缓存中的一条解密结果。
type plainEntry struct {
	fingerprint string
	data        []byte
}

// newPlainCache 创建缓存。
func newPlainCache(limit, maxBytes int) *plainCache {
	return &plainCache{
		limit:    limit,
		maxBytes: maxBytes,
		entries:  make(map[string]*plainEntry),
	}
}

// get 取缓存；指纹不一致视为未命中。
func (c *plainCache) get(vid, fingerprint string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[vid]
	if !ok || entry.fingerprint != fingerprint {
		return nil, false
	}
	return entry.data, true
}

// put 写入缓存并按上限淘汰最旧条目。
func (c *plainCache) put(vid, fingerprint string, data []byte) {
	if c == nil || len(data) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[vid]; ok {
		c.totalSize -= len(existing.data)
		delete(c.entries, vid)
		for i, key := range c.order {
			if key == vid {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
	c.entries[vid] = &plainEntry{fingerprint: fingerprint, data: data}
	c.order = append(c.order, vid)
	c.totalSize += len(data)

	for (len(c.order) > c.limit || c.totalSize > c.maxBytes) && len(c.order) > 1 {
		oldest := c.order[0]
		c.order = c.order[1:]
		if entry, ok := c.entries[oldest]; ok {
			c.totalSize -= len(entry.data)
			delete(c.entries, oldest)
		}
	}
}
