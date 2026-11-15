package media

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// stubResolver 返回固定片源。
type stubResolver struct {
	src StreamSource
	err error
}

// ResolveStream 实现 Resolver。
func (s stubResolver) ResolveStream(context.Context, string) (StreamSource, error) {
	return s.src, s.err
}

// TestServeEncryptedDecryptsAndServesRange 加密片源应解密后按 Range 回流。
func TestServeEncryptedDecryptsAndServesRange(t *testing.T) {
	cipher := []byte("cipher-bytes-0123456789")
	var fetches atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		_, _ = w.Write(cipher)
	}))
	t.Cleanup(upstream.Close)

	// 用「逐字节取反」模拟解密，便于断言服务出去的是解密结果。
	plain := make([]byte, len(cipher))
	for i, b := range cipher {
		plain[i] = ^b
	}
	p := NewProxy(stubResolver{src: StreamSource{
		URL:        upstream.URL + "/episode.mp4",
		Encrypted:  true,
		ContentKey: "00112233445566778899aabbccddeeff",
	}})
	p.allowHost = func(string) bool { return true }
	p.decrypt = func(data, key []byte) ([]byte, error) {
		if len(key) != 16 {
			t.Fatalf("密钥长度 = %d, want 16", len(key))
		}
		out := make([]byte, len(data))
		for i, b := range data {
			out[i] = ^b
		}
		return out, nil
	}

	// 整段请求。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/123", nil)
	if err := p.Serve(rec, req, "123"); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Body.String(); got != string(plain) {
		t.Fatalf("响应体 = %q, want %q", got, plain)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q", ct)
	}

	// Range 请求：应从解密结果里取片段，且不重新拉上游。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/media/123", nil)
	req.Header.Set("Range", "bytes=2-5")
	if err := p.Serve(rec, req, "123"); err != nil {
		t.Fatalf("Serve(Range): %v", err)
	}
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if got := rec.Body.String(); got != string(plain[2:6]) {
		t.Fatalf("Range 响应体 = %q, want %q", got, string(plain[2:6]))
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("上游请求次数 = %d, want 1（应命中解密缓存）", n)
	}
}

// TestServeEncryptedRequiresKey 缺内容密钥时返回 ErrEncrypted。
func TestServeEncryptedRequiresKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "cipher")
	}))
	t.Cleanup(upstream.Close)

	p := NewProxy(stubResolver{src: StreamSource{URL: upstream.URL + "/x.mp4", Encrypted: true}})
	p.allowHost = func(string) bool { return true }
	rec := httptest.NewRecorder()
	err := p.Serve(rec, httptest.NewRequest(http.MethodGet, "/media/1", nil), "1")
	if err == nil || err != errNoContentKey {
		t.Fatalf("err = %v, want errNoContentKey", err)
	}
}

func TestPlainCacheEvictsOldest(t *testing.T) {
	cache := newPlainCache(2, 1<<20)
	cache.put("a", "f1", []byte("aaa"))
	cache.put("b", "f2", []byte("bbb"))
	if _, ok := cache.get("a", "f1"); !ok {
		t.Fatal("a 应命中")
	}
	cache.put("c", "f3", []byte("ccc"))
	if _, ok := cache.get("a", "f1"); ok {
		t.Fatal("a 应被淘汰（容量 2）")
	}
	if _, ok := cache.get("b", "f2"); !ok {
		t.Fatal("b 应保留")
	}
	// 指纹变化视为不同片源。
	if _, ok := cache.get("b", "other"); ok {
		t.Fatal("指纹不一致不应命中")
	}
}
