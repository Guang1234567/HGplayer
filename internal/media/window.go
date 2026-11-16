package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"hgplayer/internal/cenc"
)

// 窗口式解密供给：把浏览器请求的明文区间映射成上游字节区间，
// 只取需要的部分、只解密覆盖到的样本，并按固定窗口缓存解密结果。
//
// 与整段下载相比，首帧只需要「头部窗口（moov）+ 前几个窗口」，
// 拖动 seek 也只重取缺失窗口。
const (
	// planProbeWindow 是首次探测头部窗口的字节数。
	planProbeWindow = 64 << 10
	// planProbeMax 是头部窗口的增长上限。
	planProbeMax = 4 << 20
	// planCacheLimit 是布局缓存（含明文前缀）的条目上限。
	planCacheLimit = 4
	// plainWindowBytes 是明文窗口大小，也是解密与缓存的最小单位。
	//
	// 取值偏小是为了首帧更快：首播第一帧只需前缀（moov）与第一个窗口。
	plainWindowBytes = 256 << 10
	// windowCacheMaxBytes 是明文窗口缓存的总字节上限。
	windowCacheMaxBytes = 64 << 20
	// plainWindowAhead 是在途窗口数：上游往返与解密靠它重叠。
	plainWindowAhead = 6
)

// planCache 按 vid 缓存解密布局（含明文前缀）。
type planCache struct {
	mu      sync.Mutex
	entries map[string]*planEntry
}

// planEntry 是缓存的一条布局。
type planEntry struct {
	fingerprint string
	plan        *cenc.Plan
}

// newPlanCache 创建空缓存。
func newPlanCache() *planCache { return &planCache{entries: make(map[string]*planEntry)} }

// get 取布局；指纹不一致视为未命中。
func (c *planCache) get(vid, fingerprint string) (*cenc.Plan, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[vid]
	if !ok || entry.fingerprint != fingerprint {
		return nil, false
	}
	return entry.plan, true
}

// put 写入布局（超过条目上限时整体清空，布局重建成本可控）。
func (c *planCache) put(vid, fingerprint string, plan *cenc.Plan) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= planCacheLimit {
		c.entries = make(map[string]*planEntry)
	}
	c.entries[vid] = &planEntry{fingerprint: fingerprint, plan: plan}
}

// windowCache 按 vid 缓存已解密的明文窗口。
type windowCache struct {
	mu       sync.Mutex
	maxBytes int
	bytes    int
	entries  map[string]*windowEntry
	order    []string
}

// windowEntry 是某个片源已缓存的窗口集合。
type windowEntry struct {
	fingerprint string
	windows     map[int64][]byte
	order       []int64
	bytes       int
}

// newWindowCache 创建空缓存。
func newWindowCache(maxBytes int) *windowCache {
	return &windowCache{maxBytes: maxBytes, entries: make(map[string]*windowEntry)}
}

// get 取明文窗口；指纹不一致视为未命中。
func (c *windowCache) get(vid, fingerprint string, index int64) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[vid]
	if !ok || entry.fingerprint != fingerprint {
		return nil, false
	}
	data, ok := entry.windows[index]
	return data, ok
}

// put 写入明文窗口，并按总字节上限淘汰最旧的片源条目。
func (c *windowCache) put(vid, fingerprint string, index int64, data []byte) {
	if c == nil || len(data) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[vid]
	if !ok || entry.fingerprint != fingerprint {
		if ok {
			c.bytes -= entry.bytes
			c.removeLocked(vid)
		}
		entry = &windowEntry{fingerprint: fingerprint, windows: make(map[int64][]byte)}
		c.entries[vid] = entry
		c.order = append(c.order, vid)
	}
	if _, exists := entry.windows[index]; exists {
		return
	}
	entry.windows[index] = data
	entry.order = append(entry.order, index)
	entry.bytes += len(data)
	c.bytes += len(data)

	for c.bytes > c.maxBytes && len(c.order) > 0 {
		oldest := c.order[0]
		victim := c.entries[oldest]
		if victim == nil {
			c.order = c.order[1:]
			continue
		}
		if len(victim.order) <= 1 {
			c.bytes -= victim.bytes
			c.removeLocked(oldest)
			continue
		}
		drop := victim.order[0]
		victim.order = victim.order[1:]
		if dropped, ok := victim.windows[drop]; ok {
			victim.bytes -= len(dropped)
			c.bytes -= len(dropped)
			delete(victim.windows, drop)
		}
	}
}

// removeLocked 从缓存中移除一个片源条目（调用方持锁）。
func (c *windowCache) removeLocked(vid string) {
	delete(c.entries, vid)
	for i, key := range c.order {
		if key == vid {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// logf 返回日志器；未注入时使用空实现。
func (p *Proxy) logf() Logger {
	if p.logger != nil {
		return p.logger
	}
	return nopLogger{}
}

// nopLogger 丢弃全部日志。
type nopLogger struct{}

// Info 实现 Logger。
func (nopLogger) Info(string, ...any) {}

// Warn 实现 Logger。
func (nopLogger) Warn(string, ...any) {}

// planFor 解析片源的解密布局（必要时探测头部窗口）。
//
// 返回的 whole 非空表示上游不支持 Range，调用方应改用整段解密。
// 无法窗口化的片源会缓存 nil 布局，避免每个请求都重新探测。
func (p *Proxy) planFor(ctx context.Context, vid string, src StreamSource) (*cenc.Plan, []byte, error) {
	fingerprint := mediaFingerprint(src.fingerprintSeed(), src.ContentKey)
	if plan, ok := p.plans.get(vid, fingerprint); ok {
		return plan, nil, nil
	}

	window := int64(planProbeWindow)
	for {
		head, total, ranged, whole, err := p.probe(ctx, src, window)
		if err != nil {
			return nil, nil, err
		}
		if !ranged {
			p.plans.put(vid, fingerprint, nil)
			p.logf().Warn("媒体退回整段解密", "vid", vid, "原因", "上游不支持 Range")
			return nil, whole, nil
		}
		plan, err := cenc.BuildPlan(head, total)
		if err == nil {
			p.plans.put(vid, fingerprint, plan)
			p.logf().Info("媒体窗口式解密就绪",
				"vid", vid, "总字节", total, "前缀", len(plan.Prefix),
				"样本数", len(plan.Samples), "探测窗口", window)
			return plan, nil, nil
		}
		if !errors.Is(err, cenc.ErrNeedMoreHead) || window >= total || window >= planProbeMax {
			p.plans.put(vid, fingerprint, nil)
			p.logf().Warn("媒体退回整段解密", "vid", vid, "原因", err.Error())
			return nil, nil, err
		}
		window *= 2
	}
}

// probe 取头部窗口；返回明文布局所需的头部字节与文件总长。
//
// 上游忽略 Range（返回 200）时 ranged 为 false，并返回整段内容。
func (p *Proxy) probe(ctx context.Context, src StreamSource, size int64) (head []byte, total int64, ranged bool, whole []byte, err error) {
	resp, err := p.getUpstreamAny(ctx, src, fmt.Sprintf("bytes=0-%d", size-1))
	if err != nil {
		return nil, 0, false, nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, false, nil, err
		}
		parsed, err := parseContentRangeTotal(resp.Header.Get("Content-Range"))
		if err != nil {
			return nil, 0, false, nil, err
		}
		return body, parsed, true, nil, nil
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, false, nil, err
		}
		return nil, int64(len(body)), false, body, nil
	default:
		return nil, 0, false, nil, fmt.Errorf("上游媒体返回 HTTP %d", resp.StatusCode)
	}
}

// parseContentRangeTotal 解析 "bytes 0-65535/11365469" 中的总长度。
func parseContentRangeTotal(value string) (int64, error) {
	slash := -1
	for i := len(value) - 1; i >= 0; i-- {
		if value[i] == '/' {
			slash = i
			break
		}
	}
	if slash < 0 {
		return 0, errors.New("上游 Content-Range 缺失")
	}
	total, err := strconv.ParseInt(value[slash+1:], 10, 64)
	if err != nil || total <= 0 {
		return 0, errors.New("上游 Content-Range 总长度非法")
	}
	return total, nil
}

// openUpstream 发起一次上游请求（自动跟随有限次重定向并校验收敛后的域名）。
//
// openCtx 只约束「拿到响应头」这一步；拿到后响应体的读取改由 readCtx 约束，
// 这样竞速时可以安全地取消落败分支，而胜出分支继续按请求生命周期读取。
func (p *Proxy) openUpstream(readCtx, openCtx context.Context, target string, rangeHeader string) (*http.Response, error) {
	for hop := 0; hop <= maxRedirects; hop++ {
		req, err := http.NewRequestWithContext(openCtx, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("构造上游请求失败: %w", err)
		}
		req.Header.Set("Accept-Encoding", "identity")
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("上游媒体请求失败: %w", err)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
			next, err := resolveLocation(target, resp.Header.Get("Location"))
			resp.Body.Close()
			if err != nil {
				return nil, err
			}
			if !p.allow(next) {
				return nil, errors.New("片源地址未通过安全检查")
			}
			target = next
			continue
		}
		// 响应体改由调用方 ctx 约束。
		resp.Body = &contextBody{ReadCloser: resp.Body, ctx: readCtx}
		return resp, nil
	}
	return nil, errors.New("上游媒体重定向次数过多")
}

// contextBody 让响应体的读取跟随给定 ctx（关闭时一并释放）。
type contextBody struct {
	io.ReadCloser
	ctx context.Context
}

// Read 实现 io.Reader。
func (b *contextBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.ReadCloser.Read(p)
}

// getUpstream 发起一次上游请求（打开与读取都用同一个 ctx）。
func (p *Proxy) getUpstream(ctx context.Context, target string, rangeHeader string) (*http.Response, error) {
	return p.openUpstream(ctx, ctx, target, rangeHeader)
}

// getUpstreamAny 在多个候选地址间竞速，返回最先成功（且未 4xx/5xx）的响应。
//
// 同一集通常有 2~3 个 CDN 节点，实测节点间带宽可差 2 倍以上，
// 因此不固定节点，而是让各候选同时起跑、取先到者。落败分支立即取消，
// 胜出分支的取消动作绑定到响应体 Close（避免 ctx 泄漏）。
func (p *Proxy) getUpstreamAny(ctx context.Context, src StreamSource, rangeHeader string) (*http.Response, error) {
	urls := src.urls()
	if len(urls) == 0 {
		return nil, errors.New("片源地址缺失")
	}
	if len(urls) == 1 {
		return p.getUpstream(ctx, urls[0], rangeHeader)
	}

	type candidate struct {
		resp *http.Response
		err  error
	}
	results := make(chan candidate, len(urls))
	for _, target := range urls {
		go func(target string) {
			// 每个候选各自派生 ctx，便于单独取消；正常完成时在 Close 里回收。
			candidateCtx, cancel := context.WithCancel(ctx)
			resp, err := p.openUpstream(candidateCtx, candidateCtx, target, rangeHeader)
			if err == nil && resp.StatusCode >= 400 {
				resp.Body.Close()
				err = fmt.Errorf("上游媒体返回 HTTP %d", resp.StatusCode)
			}
			if err != nil {
				cancel()
				results <- candidate{err: err}
				return
			}
			resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
			results <- candidate{resp: resp}
		}(target)
	}

	var lastErr error
	for i := 0; i < len(urls); i++ {
		got := <-results
		if got.err != nil {
			lastErr = got.err
			continue
		}
		return got.resp, nil
	}
	return nil, lastErr
}

// cancelBody 在关闭响应体时回收它所属候选的 ctx。
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close 关闭响应体并释放候选 ctx。
func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// fetchRange 取上游 [u0, u1) 的原始字节（不会返回超出该区间的数据）。
func (p *Proxy) fetchRange(ctx context.Context, vid string, src StreamSource, u0, u1 int64) ([]byte, error) {
	if u1 <= u0 {
		return nil, nil
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := p.getUpstreamAny(ctx, src, fmt.Sprintf("bytes=%d-%d", u0, u1-1))
		if err != nil {
			lastErr = err
		} else {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case readErr != nil:
				lastErr = readErr
			case resp.StatusCode == http.StatusPartialContent:
				if int64(len(body)) != u1-u0 {
					return nil, errors.New("上游返回长度与请求区间不一致")
				}
				return body, nil
			case resp.StatusCode == http.StatusOK:
				// 上游忽略 Range：从整段里截取所需区间。
				if int64(len(body)) < u1 {
					return nil, errors.New("上游返回长度与请求区间不一致")
				}
				return body[u0:u1], nil
			default:
				lastErr = fmt.Errorf("上游媒体返回 HTTP %d", resp.StatusCode)
			}
		}
		// 地址可能已过期：重新取址后重试一次。
		refreshed, err := p.resolve.ResolveStream(ctx, vid)
		if err != nil || !p.allow(refreshed.URL) {
			break
		}
		src = refreshed
	}
	return nil, lastErr
}

// window 返回某个明文窗口的内容（命中缓存时不再请求上游）。
//
// 同一窗口的并发请求合并成一次抓取；不同窗口之间并行。
func (p *Proxy) window(ctx context.Context, vid string, src StreamSource, plan *cenc.Plan, key []byte, index int64) ([]byte, error) {
	fingerprint := mediaFingerprint(src.fingerprintSeed(), src.ContentKey)
	if data, ok := p.windows.get(vid, fingerprint, index); ok {
		return data, nil
	}

	p.inflightMu.Lock()
	if p.inflight == nil {
		p.inflight = make(map[windowKey]*windowFlight)
	}
	key2 := windowKey{vid: vid, index: index}
	if flight, ok := p.inflight[key2]; ok {
		p.inflightMu.Unlock()
		<-flight.done
		return flight.data, flight.err
	}
	flight := &windowFlight{done: make(chan struct{})}
	p.inflight[key2] = flight
	p.inflightMu.Unlock()

	finish := func(data []byte, err error) {
		flight.data, flight.err = data, err
		p.inflightMu.Lock()
		delete(p.inflight, key2)
		p.inflightMu.Unlock()
		close(flight.done)
	}

	start := index * plainWindowBytes
	end := min(start+plainWindowBytes, plan.PlainSize())
	if end <= start {
		finish(nil, errors.New("明文窗口越界"))
		return nil, flight.err
	}
	fetch := func(u0, u1 int64) ([]byte, error) {
		return p.fetchRange(ctx, vid, src, u0, u1)
	}
	started := time.Now()
	data, err := plan.ReadWindow(key, start, end, fetch)
	if err != nil {
		finish(nil, err)
		return nil, err
	}
	if index == 0 {
		p.logf().Info("媒体首窗口就绪",
			"vid", vid, "字节", len(data), "耗时", time.Since(started).Round(time.Millisecond).String())
	}
	p.windows.put(vid, fingerprint, index, data)
	finish(data, nil)
	return data, nil
}

// windowKey 标识一份片源的一个明文窗口。
type windowKey struct {
	vid   string
	index int64
}

// windowFlight 是一次在途的窗口抓取。
type windowFlight struct {
	data []byte
	err  error
	done chan struct{}
}

// serveWindows 按明文窗口把响应流式写出。
//
// 窗口抓取走流水线：先并发预取前几个窗口，之后每消费一个窗口就补一个，
// 让上游往返与解密重叠；每个窗口写出后立即 flush，让内核尽早拿到可播数据。
func (p *Proxy) serveWindows(w http.ResponseWriter, r *http.Request, vid string, src StreamSource, plan *cenc.Plan, key []byte) error {
	total := plan.PlainSize()
	start, end, status := int64(0), total-1, http.StatusOK
	if rng := ParseRange(r.Header.Get("Range"), total); rng.Valid {
		start, end, status = rng.Start, rng.End, http.StatusPartialContent
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Accept-Ranges", AcceptRanges)
	if status == http.StatusPartialContent {
		w.Header().Set("Content-Range", ContentRange(start, end, total))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.WriteHeader(status)

	ctx := r.Context()
	flusher, _ := w.(http.Flusher)

	first := start / plainWindowBytes
	last := end / plainWindowBytes
	pending := make(map[int64]*windowFetch, plainWindowAhead)
	order := make([]int64, 0, plainWindowAhead)

	// schedule 预取窗口 index（已调度过则跳过）。
	schedule := func(index int64) {
		if index > last || index < first {
			return
		}
		if _, exists := pending[index]; exists {
			return
		}
		fetch := &windowFetch{done: make(chan struct{})}
		pending[index] = fetch
		order = append(order, index)
		go func() {
			data, err := p.window(ctx, vid, src, plan, key, index)
			fetch.data, fetch.err = data, err
			close(fetch.done)
		}()
	}

	for index := first; index <= last && index < first+plainWindowAhead; index++ {
		schedule(index)
	}

	for at := start; at <= end; {
		index := at / plainWindowBytes
		fetch, ok := pending[index]
		if !ok {
			schedule(index)
			fetch = pending[index]
		}
		<-fetch.done
		delete(pending, index)
		for i, key := range order {
			if key == index {
				order = append(order[:i], order[i+1:]...)
				break
			}
		}
		// 每消费一个窗口就补一个，保持流水线满载。
		schedule(index + plainWindowAhead)

		if fetch.err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("%w: %v", ErrResponseStarted, fetch.err)
		}
		windowStart := index * plainWindowBytes
		from := at - windowStart
		to := min(end, windowStart+int64(len(fetch.data))-1) - windowStart + 1
		if from < 0 || to > int64(len(fetch.data)) || from >= to {
			return fmt.Errorf("%w: 明文窗口与请求区间不匹配", ErrResponseStarted)
		}
		if _, err := w.Write(fetch.data[from:to]); err != nil {
			return nil
		}
		if flusher != nil {
			flusher.Flush()
		}
		at = windowStart + to
	}
	return nil
}

// windowFetch 是一条在途的窗口抓取。
type windowFetch struct {
	data []byte
	err  error
	done chan struct{}
}

// resolveLocation 依据当前地址解析重定向目标。
func resolveLocation(current, location string) (string, error) {
	base, err := http.NewRequest(http.MethodGet, current, nil)
	if err != nil {
		return "", errors.New("上游重定向地址无效")
	}
	next, err := base.URL.Parse(location)
	if err != nil {
		return "", errors.New("上游重定向地址无效")
	}
	return next.String(), nil
}
