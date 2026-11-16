package media

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestGetUpstreamAnyRaceWinnerReadable 竞速取先到者，且胜出响应体可正常读完。
func TestGetUpstreamAnyRaceWinnerReadable(t *testing.T) {
	var fastHits, slowHits atomic.Int32
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fastHits.Add(1)
		body := []byte("FAST")
		w.Header().Set("Content-Range", "bytes 0-3/4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	}))
	t.Cleanup(fast.Close)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slowHits.Add(1)
		time.Sleep(300 * time.Millisecond)
		body := []byte("SLOW")
		w.Header().Set("Content-Range", "bytes 0-3/4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	}))
	t.Cleanup(slow.Close)

	proxy := NewProxy(stubResolver{})
	proxy.allowHost = func(string) bool { return true }

	src := StreamSource{URL: slow.URL + "/m.mp4", Alternatives: []string{fast.URL + "/m.mp4"}}
	resp, err := proxy.getUpstreamAny(context.Background(), src, "bytes=0-3")
	if err != nil {
		t.Fatalf("getUpstreamAny: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取胜出响应体失败: %v", err)
	}
	if string(body) != "FAST" {
		t.Fatalf("响应体 = %q, want FAST（应取先到的节点）", body)
	}
	// 慢节点在被取消前可能还没进到 handler（连接被提前关闭），因此只要求
	// 快节点被命中；竞速本身由「必须取到 FAST 而不是 SLOW」来保证。
	if fastHits.Load() == 0 {
		t.Fatal("快节点未被命中")
	}
	_ = slowHits.Load()
}

// TestFetchRangeRotatesOnUpstreamError 上游失败时换候选重试。
func TestFetchRangeRotatesOnUpstreamError(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(broken.Close)
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := []byte("0123456789")
		w.Header().Set("Content-Range", "bytes 0-9/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	}))
	t.Cleanup(healthy.Close)

	proxy := NewProxy(stubResolver{})
	proxy.allowHost = func(string) bool { return true }
	src := StreamSource{URL: broken.URL + "/m.mp4", Alternatives: []string{healthy.URL + "/m.mp4"}}

	got, err := proxy.fetchRange(context.Background(), "123", src, 0, 10)
	if err != nil {
		t.Fatalf("fetchRange: %v", err)
	}
	if string(got) != "0123456789" {
		t.Fatalf("内容 = %q", got)
	}
}

// flakyResolver 首次解析失败，之后成功；用于验证换设备重取。
type flakyResolver struct {
	mu       sync.Mutex
	failures int
	calls    int
	src      StreamSource
}

// ResolveStream 实现 Resolver（并发安全：媒体请求会并发取址）。
func (r *flakyResolver) ResolveStream(context.Context, string) (StreamSource, error) {
	r.mu.Lock()
	r.calls++
	calls := r.calls
	r.mu.Unlock()
	if calls <= r.failures {
		return StreamSource{}, errors.New("App 取址不可用：上游返回空响应")
	}
	return r.src, nil
}

// callCount 返回取址次数。
func (r *flakyResolver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestServeResolvesAgainAfterRefresh 取址失败时应刷新设备并重取一次。
func TestServeResolvesAgainAfterRefresh(t *testing.T) {
	plain := []byte("plain-body-0123456789")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(plain)))
		_, _ = w.Write(plain)
	}))
	t.Cleanup(server.Close)

	resolver := &flakyResolver{failures: 1, src: StreamSource{URL: server.URL + "/m.mp4"}}
	refreshed := 0
	proxy := NewProxy(resolver, WithRefresher(func(context.Context) error {
		refreshed++
		return nil
	}))
	proxy.allowHost = func(string) bool { return true }

	rec := httptest.NewRecorder()
	if err := proxy.Serve(rec, httptest.NewRequest(http.MethodGet, "/media/1", nil), "1"); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != string(plain) {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if refreshed != 1 {
		t.Fatalf("刷新次数 = %d, want 1", refreshed)
	}
	if resolver.callCount() != 2 {
		t.Fatalf("取址次数 = %d, want 2（失败后重取）", resolver.callCount())
	}
}

// TestServeRefreshFailurePropagates 重取仍失败时返回错误（不再无限重试）。
func TestServeRefreshFailurePropagates(t *testing.T) {
	resolver := &flakyResolver{failures: 5, src: StreamSource{URL: "https://example.com/m.mp4"}}
	refreshed := 0
	proxy := NewProxy(resolver, WithRefresher(func(context.Context) error {
		refreshed++
		return nil
	}))
	proxy.allowHost = func(string) bool { return true }

	rec := httptest.NewRecorder()
	err := proxy.Serve(rec, httptest.NewRequest(http.MethodGet, "/media/1", nil), "1")
	if err == nil {
		t.Fatal("重取仍失败时应返回错误")
	}
	if refreshed != 1 {
		t.Fatalf("刷新次数 = %d, want 1（只重取一次）", refreshed)
	}
}

// TestServeWithoutRefresherFailsFast 未注入刷新钩子时不重试。
func TestServeWithoutRefresherFailsFast(t *testing.T) {
	resolver := &flakyResolver{failures: 5, src: StreamSource{URL: "https://example.com/m.mp4"}}
	proxy := NewProxy(resolver)
	proxy.allowHost = func(string) bool { return true }

	if err := proxy.Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media/1", nil), "1"); err == nil {
		t.Fatal("应返回取址错误")
	}
	if resolver.callCount() != 1 {
		t.Fatalf("取址次数 = %d, want 1", resolver.callCount())
	}
}

// TestWindowedServeConcurrentRequests 并发请求同一片源（窗口流水线 + 竞速 + 去重）。
//
// 真实播放器会并发发起多个 Range 请求；这里在 -race 下压这段路径，
// 覆盖 inflight 去重、窗口缓存与多候选竞速的并发访问。
func TestWindowedServeConcurrentRequests(t *testing.T) {
	fixture, _, proxy, _ := newWindowFixture(t)
	want, err := cenc.Decrypt(fixture.File, fixture.Key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	var wg sync.WaitGroup
	failures := make(chan string, 32)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start := int64(i) * 40000
			end := min(start+300000, int64(len(want))-1)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/media/123", nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
			if err := proxy.Serve(rec, req, "123"); err != nil {
				failures <- fmt.Sprintf("Serve: %v", err)
				return
			}
			if rec.Code != http.StatusPartialContent {
				failures <- fmt.Sprintf("status = %d", rec.Code)
				return
			}
			if !bytes.Equal(rec.Body.Bytes(), want[start:end+1]) {
				failures <- fmt.Sprintf("区间 %d-%d 内容不一致", start, end)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Fatal(msg)
	}
}

// TestServeRefreshConcurrentWithPlayback 换设备与播放取数并发。
//
// 取址失败触发的设备刷新会重建上游通道，同时窗口 goroutine 正在读通道；
// 这段并发必须无竞争（-race 下验证）。
func TestServeRefreshConcurrentWithPlayback(t *testing.T) {
	key := bytes.Repeat([]byte{0x5c}, 16)
	fixture := cenctest.Build([][]byte{cenctest.Pattern(3, 200000), cenctest.Pattern(4, 200000)}, key)
	upstreamSrv := httptest.NewServer((&rangeUpstream{file: fixture.File}).handler())
	t.Cleanup(upstreamSrv.Close)

	want, err := cenc.Decrypt(fixture.File, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	resolver := &flakyResolver{
		failures: 1,
		src: StreamSource{
			URL:        upstreamSrv.URL + "/m.mp4",
			Encrypted:  true,
			ContentKey: hex.EncodeToString(key),
		},
	}
	proxy := NewProxy(resolver, WithRefresher(func(context.Context) error { return nil }))
	proxy.allowHost = func(string) bool { return true }

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			_ = proxy.Serve(rec, httptest.NewRequest(http.MethodGet, "/media/9", nil), "9")
		}()
	}
	wg.Wait()

	// 并发收敛后必须能正确供给。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/9", nil)
	req.Header.Set("Range", "bytes=0-1023")
	if err := proxy.Serve(rec, req, "9"); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), want[:1024]) {
		t.Fatalf("status=%d", rec.Code)
	}
}

// TestConcurrentResolveFailuresRefreshOnce 并发取址失败只注册一台新设备。
//
// 播放器会并发发起多个请求；若每个失败都各自换设备，会瞬间烧掉多台设备。
func TestConcurrentResolveFailuresRefreshOnce(t *testing.T) {
	key := bytes.Repeat([]byte{0x6d}, 16)
	fixture := cenctest.Build([][]byte{cenctest.Pattern(9, 100000)}, key)
	upstreamSrv := httptest.NewServer((&rangeUpstream{file: fixture.File}).handler())
	t.Cleanup(upstreamSrv.Close)

	resolver := &flakyResolver{
		failures: 8,
		src: StreamSource{
			URL:        upstreamSrv.URL + "/m.mp4",
			Encrypted:  true,
			ContentKey: hex.EncodeToString(key),
		},
	}
	var refreshes atomic.Int32
	proxy := NewProxy(resolver, WithRefresher(func(context.Context) error {
		refreshes.Add(1)
		time.Sleep(20 * time.Millisecond) // 拉长窗口，确保并发请求同时处于失败态
		return nil
	}))
	proxy.allowHost = func(string) bool { return true }

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = proxy.Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media/7", nil), "7")
		}()
	}
	wg.Wait()

	if got := refreshes.Load(); got != 1 {
		t.Fatalf("设备刷新次数 = %d, want 1（并发失败必须单飞）", got)
	}
}
