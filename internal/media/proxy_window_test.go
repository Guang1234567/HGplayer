package media

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"hgplayer/internal/cenc"
	"hgplayer/internal/cenc/cenctest"
)

// rangeUpstream 是支持 Range 的上游，并统计请求次数与字节数。
type rangeUpstream struct {
	file []byte

	mu       sync.Mutex
	requests int
	served   int
	maxBody  int
	unranged int
}

// handler 处理上游请求，按 Range 返回 206。
func (u *rangeUpstream) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.requests++
		u.mu.Unlock()

		spec := r.Header.Get("Range")
		if !strings.HasPrefix(spec, "bytes=") {
			u.mu.Lock()
			u.unranged++
			u.served += len(u.file)
			u.mu.Unlock()
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Content-Length", strconv.Itoa(len(u.file)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(u.file)
			return
		}
		bounds := strings.SplitN(strings.TrimPrefix(spec, "bytes="), "-", 2)
		start, _ := strconv.ParseInt(bounds[0], 10, 64)
		end := int64(len(u.file)) - 1
		if len(bounds) == 2 && bounds[1] != "" {
			if parsed, err := strconv.ParseInt(bounds[1], 10, 64); err == nil {
				end = parsed
			}
		}
		if start >= int64(len(u.file)) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end = min(end, int64(len(u.file))-1)
		body := u.file[start : end+1]

		u.mu.Lock()
		u.served += len(body)
		u.maxBody = max(u.maxBody, len(body))
		u.mu.Unlock()

		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(u.file)))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	}
}

// stats 返回统计快照。
func (u *rangeUpstream) stats() (requests, served, maxBody, unranged int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.requests, u.served, u.maxBody, u.unranged
}

// newWindowFixture 构造跨多个明文窗口的加密夹具（约 2MB）。
func newWindowFixture(t *testing.T) (cenctest.Fixture, *rangeUpstream, *Proxy, string) {
	t.Helper()
	key := bytes.Repeat([]byte{0x7a}, 16)
	plaintexts := make([][]byte, 32)
	for i := range plaintexts {
		plaintexts[i] = cenctest.Pattern(byte(i*7), 64<<10)
	}
	fixture := cenctest.Build(plaintexts, key)

	upstream := &rangeUpstream{file: fixture.File}
	server := httptest.NewServer(upstream.handler())
	t.Cleanup(server.Close)

	proxy := NewProxy(stubResolver{src: StreamSource{
		URL:        server.URL + "/media.mp4",
		Encrypted:  true,
		ContentKey: hex.EncodeToString(key),
	}})
	proxy.allowHost = func(string) bool { return true }
	return fixture, upstream, proxy, server.URL
}

// TestWindowedServeOnlyFetchesRequestedRange 小范围请求不应拉整段。
func TestWindowedServeOnlyFetchesRequestedRange(t *testing.T) {
	fixture, upstream, proxy, _ := newWindowFixture(t)
	want, err := cenc.Decrypt(fixture.File, fixture.Key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/123", nil)
	req.Header.Set("Range", "bytes=100000-100999")
	if err := proxy.Serve(rec, req, "123"); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), want[100000:101000]) {
		t.Fatal("Range 响应内容与整段解密不一致")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q", ct)
	}

	requests, served, maxBody, unranged := upstream.stats()
	if unranged != 0 {
		t.Fatalf("上游应只收到 Range 请求，未带 Range 的请求 = %d", unranged)
	}
	// 头部窗口（64KB）+ 一个窗口（512KB）足够；绝不应拉整段（约 2MB）。
	if served > len(fixture.File)/2 {
		t.Fatalf("上游字节数 = %d，远超所需（文件 %d）", served, len(fixture.File))
	}
	if maxBody > plainWindowBytes+planProbeWindow {
		t.Fatalf("单次上游响应 = %d，超过窗口大小", maxBody)
	}
	if requests > 4 {
		t.Fatalf("上游请求次数 = %d，过多", requests)
	}
}

// TestWindowedServeWholeFileMatchesDecrypt 逐窗口取整份，必须与整段解密逐字节一致。
func TestWindowedServeWholeFileMatchesDecrypt(t *testing.T) {
	fixture, _, proxy, _ := newWindowFixture(t)
	want, err := cenc.Decrypt(fixture.File, fixture.Key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	// 按不跨窗口边界对齐的步长逐段取；同时覆盖未对齐的起点。
	var got []byte
	const step = 300 << 10
	for start := 0; start < len(want); start += step {
		end := min(start+step, len(want)) - 1
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/media/123", nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		if err := proxy.Serve(rec, req, "123"); err != nil {
			t.Fatalf("Serve(%d-%d): %v", start, end, err)
		}
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("status = %d, want 206", rec.Code)
		}
		if wantCR := fmt.Sprintf("bytes %d-%d/%d", start, end, len(want)); rec.Header().Get("Content-Range") != wantCR {
			t.Fatalf("Content-Range = %q, want %q", rec.Header().Get("Content-Range"), wantCR)
		}
		got = append(got, rec.Body.Bytes()...)
	}
	if !bytes.Equal(got, want) {
		idx := 0
		for idx < len(got) && idx < len(want) && got[idx] == want[idx] {
			idx++
		}
		t.Fatalf("拼接结果与整段解密不一致（首个差异 @%d）", idx)
	}
}

// TestWindowedServeStreamsWholeFile 整段请求应按窗口流式写出。
func TestWindowedServeStreamsWholeFile(t *testing.T) {
	fixture, upstream, proxy, _ := newWindowFixture(t)
	want, err := cenc.Decrypt(fixture.File, fixture.Key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/123", nil)
	req.Header.Set("Range", "bytes=0-")
	if err := proxy.Serve(rec, req, "123"); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if got, wantLen := len(rec.Body.Bytes()), len(want); got != wantLen {
		t.Fatalf("响应长度 = %d, want %d", got, wantLen)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatal("整段响应与整段解密不一致")
	}
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(len(want)) {
		t.Fatalf("Content-Length = %q", cl)
	}
	// 每个上游响应都不应超过一个窗口（说明是流式而非一次拉整段）。
	_, _, maxBody, _ := upstream.stats()
	if maxBody > plainWindowBytes+planProbeWindow {
		t.Fatalf("单次上游响应 = %d，超过窗口大小", maxBody)
	}
}

// TestWindowedServeReusesCache 重复与回退请求不应重新拉上游。
func TestWindowedServeReusesCache(t *testing.T) {
	fixture, upstream, proxy, _ := newWindowFixture(t)
	want, err := cenc.Decrypt(fixture.File, fixture.Key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	serve := func(rng string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/media/123", nil)
		req.Header.Set("Range", rng)
		if err := proxy.Serve(rec, req, "123"); err != nil {
			t.Fatalf("Serve(%s): %v", rng, err)
		}
		return rec
	}

	rec := serve(fmt.Sprintf("bytes=%d-%d", plainWindowBytes-1000, plainWindowBytes+1000))
	if !bytes.Equal(rec.Body.Bytes(), want[plainWindowBytes-1000:plainWindowBytes+1001]) {
		t.Fatal("跨窗口响应与整段解密不一致")
	}
	_, _, _, _ = upstream.stats()
	requests, _, _, _ := upstream.stats()

	// 同一区间（含回退）再次请求：应全部命中窗口缓存。
	rec = serve(fmt.Sprintf("bytes=%d-%d", plainWindowBytes-500, plainWindowBytes+500))
	if !bytes.Equal(rec.Body.Bytes(), want[plainWindowBytes-500:plainWindowBytes+501]) {
		t.Fatal("缓存回放内容不一致")
	}
	rec = serve(fmt.Sprintf("bytes=%d-%d", plainWindowBytes-1000, plainWindowBytes+1000))
	if !bytes.Equal(rec.Body.Bytes(), want[plainWindowBytes-1000:plainWindowBytes+1001]) {
		t.Fatal("缓存回放内容不一致")
	}
	if again, _, _, _ := upstream.stats(); again != requests {
		t.Fatalf("上游请求次数从 %d 增到 %d，缓存未命中", requests, again)
	}
}

// TestWindowedServeFallsBackWhenNoRange 上游不支持 Range 时退回整段解密。
func TestWindowedServeFallsBackWhenNoRange(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, 16)
	fixture := cenctest.Build([][]byte{cenctest.Pattern(1, 4096), cenctest.Pattern(2, 4096)}, key)

	var requests int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK) // 忽略 Range
		_, _ = w.Write(fixture.File)
	}))
	t.Cleanup(server.Close)

	proxy := NewProxy(stubResolver{src: StreamSource{
		URL:        server.URL + "/media.mp4",
		Encrypted:  true,
		ContentKey: hex.EncodeToString(key),
	}})
	proxy.allowHost = func(string) bool { return true }

	want, err := cenc.Decrypt(fixture.File, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/media/123", nil)
		if err := proxy.Serve(rec, req, "123"); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), want) {
			t.Fatal("兜底路径响应与整段解密不一致")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("上游请求次数 = %d, want 1（探测已取回整段并缓存）", requests)
	}
}

// TestParseContentRangeTotal 解析 Content-Range 的总长度。
func TestParseContentRangeTotal(t *testing.T) {
	total, err := parseContentRangeTotal("bytes 0-65535/11365469")
	if err != nil || total != 11365469 {
		t.Fatalf("total = %d, err = %v", total, err)
	}
	if _, err := parseContentRangeTotal(""); err == nil {
		t.Fatal("缺失 Content-Range 应报错")
	}
	if _, err := parseContentRangeTotal("bytes 0-1/*"); err == nil {
		t.Fatal("通配总长度应报错")
	}
}

// TestFetchRangeRefreshesExpiredURL 上游 403 时应重新取址并重试。
func TestFetchRangeRefreshesExpiredURL(t *testing.T) {
	var mu sync.Mutex
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.URL.Query().Get("t"))
		mu.Unlock()
		if r.URL.Query().Get("t") != "fresh" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body := []byte("0123456789")
		w.Header().Set("Content-Range", "bytes 0-9/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	resolver := &countingResolver{base: server.URL}
	proxy := NewProxy(resolver)
	proxy.allowHost = func(string) bool { return true }

	src := StreamSource{URL: server.URL + "/m.mp4?t=stale"}
	got, err := proxy.fetchRange(context.Background(), "123", src, 0, 10)
	if err != nil {
		t.Fatalf("fetchRange: %v", err)
	}
	if string(got) != "0123456789" {
		t.Fatalf("内容 = %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 2 || tokens[0] != "stale" || tokens[1] != "fresh" {
		t.Fatalf("上游 token 序列 = %v, want [stale fresh]", tokens)
	}
}

// countingResolver 每次解析都返回新地址，用于验证过期重取。
type countingResolver struct {
	base  string
	count int
}

// ResolveStream 实现 Resolver。
func (c *countingResolver) ResolveStream(context.Context, string) (StreamSource, error) {
	c.count++
	return StreamSource{URL: c.base + "/m.mp4?t=fresh", Encrypted: true}, nil
}
