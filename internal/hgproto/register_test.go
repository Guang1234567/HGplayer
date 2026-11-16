package hgproto

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"hgplayer/internal/sign"
)

// TestRegisterDeviceBuildsEnvelope 锁定注册请求形状与响应解析。
func TestRegisterDeviceBuildsEnvelope(t *testing.T) {
	var (
		gotPath  string
		gotQuery string
		gotHead  http.Header
		gotBody  []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotHead = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Set-Cookie", "store-region=cn-hb; Path=/; ttreq=1$abc123")
		_, _ = w.Write([]byte(`{"device_id_str":"7000000000000000001","install_id_str":"7000000000000000002"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(sign.PlayerSigner{}, Device{}, WithGuard(nil))
	c.registerAPI = srv.URL + "/service/2/device_register/"

	device, err := c.RegisterDevice(context.Background())
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	if gotPath != "/service/2/device_register/" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "tt_data=a") || !strings.Contains(gotQuery, "req_id=") {
		t.Fatalf("query = %q", gotQuery)
	}
	for key, want := range map[string]string{
		"Content-Type":              "application/octet-stream;tt-data=a",
		"log-encode-type":           "gzip",
		"sdk-version":               "2",
		"passport-sdk-version":      "5051452",
		"x-vc-bdturing-sdk-version": "4.0.3.cn",
		"x-ss-dp":                   "8662",
	} {
		if got := gotHead.Get(key); got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
	if stub := gotHead.Get("X-SS-STUB"); stub != sign.MD5Upper(gotBody) {
		t.Errorf("X-SS-STUB = %s, want %s", stub, sign.MD5Upper(gotBody))
	}
	// 注册体是 tt-data 信封（魔数 74 63 05 10 00 00）。
	if len(gotBody) < 6 || gotBody[0] != 0x74 || gotBody[1] != 0x63 || gotBody[2] != 0x05 {
		t.Fatalf("注册体不是 tt-data 信封: % x", gotBody[:min(len(gotBody), 8)])
	}

	if device.DeviceID != "7000000000000000001" || device.IID != "7000000000000000002" {
		t.Fatalf("device = %+v", device)
	}
	if device.TTReq != "1$abc123" || device.StoreRegion != "cn-hb" {
		t.Fatalf("cookie 未解析: %+v", device)
	}
	// 注册声明的机型档案必须带上，供后续取址请求使用。
	if device.Model == "" || device.Channel == "" || device.OpenUDID == "" {
		t.Fatalf("档案不完整: %+v", device)
	}
}

// TestRegisterDeviceRejectsNoGuest 响应缺身份时报错。
func TestRegisterDeviceRejectsNoGuest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_id_str":"0","install_id_str":"0"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(sign.PlayerSigner{}, Device{}, WithGuard(nil))
	c.registerAPI = srv.URL + "/service/2/device_register/"
	if _, err := c.RegisterDevice(context.Background()); err == nil {
		t.Fatal("缺少身份时应报错")
	}
}

// TestPlayRegistersFreshDeviceOnEmptyResponse 取址被按设备限流（200 + 空体）时，
// 换一台新注册的设备重试。
func TestPlayRegistersFreshDeviceOnEmptyResponse(t *testing.T) {
	const vid = "30002"
	model := playModelJSON(t, map[string]any{"video_list": []any{mockPlayItem(vid)}})

	attempts := 0
	var usedDevices []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usedDevices = append(usedDevices, r.URL.Query().Get("device_id"))
		attempts++
		if attempts == 1 {
			// 首次：被限流（200 空体）。
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(playResponse(t, vid, model))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(sign.PlayerSigner{}, Device{DeviceID: "old-device", IID: "old-iid"}, WithGuard(nil))
	c.playbackAPI = srv.URL + "/novel/player/video_model/v1/"
	registered := 0
	c.registrar = func(context.Context) (Device, error) {
		registered++
		return Device{DeviceID: "new-device", IID: "new-iid"}, nil
	}

	info, err := c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if registered != 1 {
		t.Fatalf("注册次数 = %d, want 1", registered)
	}
	if len(usedDevices) != 2 || usedDevices[0] != "old-device" || usedDevices[1] != "new-device" {
		t.Fatalf("使用的设备序列 = %v", usedDevices)
	}
	if info.URL != "https://qznovelvod.com/video_30002.mp4" {
		t.Fatalf("url = %q", info.URL)
	}
	// 注册器返回的新设备必须成为当前设备。
	if c.device.DeviceID != "new-device" {
		t.Fatalf("device = %+v", c.device)
	}
}

// TestSetDeviceConcurrentWithReads 并发换设备与读取身份必须安全（无撕裂值）。
//
// 媒体窗口并发抓取时会在取址失败后换设备，读取路径同时在签名时读身份；
// 这里用并发读写 + 值一致性断言覆盖该场景（机器无 gcc 时无法跑 -race）。
func TestSetDeviceConcurrentWithReads(t *testing.T) {
	c := NewClient(sign.PlayerSigner{}, Device{DeviceID: "d0", IID: "i0"}, WithGuard(nil))

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读取方：反复读取身份，断言 device_id 与 iid 始终来自同一份设备。
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				dev := c.currentDevice()
				if want := "i" + strings.TrimPrefix(dev.DeviceID, "d"); dev.IID != want {
					t.Errorf("身份撕裂: device_id=%q iid=%q", dev.DeviceID, dev.IID)
					return
				}
			}
		}()
	}

	// 写入方：反复换设备。
	for i := 1; i <= 200; i++ {
		c.SetDevice(Device{DeviceID: fmt.Sprintf("d%d", i), IID: fmt.Sprintf("i%d", i)})
	}
	close(stop)
	wg.Wait()

	if got := c.currentDevice().DeviceID; got != "d200" {
		t.Fatalf("最终设备 = %q, want d200", got)
	}
}

// recordingSigner 记录签名时收到的设备身份。
type recordingSigner struct {
	mu  sync.Mutex
	ids []string
}

// Sign 实现 sign.Signer。
func (r *recordingSigner) Sign(sig sign.Signature) sign.Headers {
	r.mu.Lock()
	r.ids = append(r.ids, sig.DeviceID+"/"+sig.IID)
	r.mu.Unlock()
	return sign.Headers{}
}

// last 返回最近一次签名使用的身份。
func (r *recordingSigner) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ids) == 0 {
		return ""
	}
	return r.ids[len(r.ids)-1]
}

// TestSetDeviceRebuildsUpstreamChannels 换设备后签名必须使用新身份。
//
// upstream.Client 在构造时就固定了 DeviceID/IID 参与签名；换设备若只改
// Client.device 而不重建通道，后续请求的签名仍用旧身份（取址会一直被限流）。
func TestSetDeviceRebuildsUpstreamChannels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	t.Cleanup(srv.Close)

	signer := &recordingSigner{}
	c := NewClient(signer, Device{DeviceID: "111", IID: "222"}, WithGuard(nil))
	c.SetDevice(Device{DeviceID: "333", IID: "444"})

	if _, err := c.doJSON(context.Background(), http.MethodGet, srv.URL+"/x", nil, nil); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if got := signer.last(); got != "333/444" {
		t.Fatalf("签名使用的身份 = %q, want 333/444（换设备后必须重建通道）", got)
	}
}
