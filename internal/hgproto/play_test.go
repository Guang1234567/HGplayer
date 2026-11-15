package hgproto

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hgplayer/internal/sign"
)

// 测试向量由源实现（app-source.js）生成，用于逐位对拍：
//
//	spadeAInput  → 内容密钥 spadeAKey
//	spadeCipher  → key_seed=spadeSeed 时解出 spadePlainURL
const (
	spadeAInput  = "EzATTFs3EkxZNRJMXzQRZUErDmVfKCZlXS4mZVsvJWWlI/h9oST6eKQ="
	spadeAKey    = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	spadeBInput  = "IjAid1s_IXVdPyFzXz4XcV0_FG9fFxNtXRcTa0MWEmlBLxxxRSgedEA="
	spadeBKey    = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	spadeSeed    = "ASNFZ4mrze8="
	spadeCipher  = "qH8BAOae6GzNvz0KWjRKfJH2A9ItFUTy2Pl2/kq980hRu1R+X3Yo/EvDFY5/nvMGHfTa6w=="
	spadeCipherA = "qBEBAEWYW92Oag0pZwy3gOu6cEQA52rjbnAcdSFFMfb2zcka23pScrzZez0Exdb057-5-fXkiwI7Y03g_m7YY4NscNI"
	spadePlain   = "https://qznovelvod.com/video_30002.mp4"
	spadePlainB  = "https://douyinvod.com/91d56f7e-c027-4ce9-9a3c-229d59a13c3b"
)

// newPlayClientFor 把测试服务器装配成注入真实播放器签名器的客户端。
//
// 三个端点全部指向本地服务器，保证测试完全离线。
func newPlayClientFor(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := NewClient(sign.PlayerSigner{}, DefaultDevice("1624456208323876", "1624456208327972", "91d56f7e-c027-4ce9-9a3c-229d59a13c3b"), WithGuard(nil))
	c.videoAPI = srv.URL + "/player"
	c.videoAPIFallback = srv.URL + "/player-fallback"
	c.shortPlayAPI = srv.URL + "/shortplay"
	return c
}

// newPlayClient 创建本地测试服务器并装配客户端。
func newPlayClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return newPlayClientFor(t, srv)
}

// playModelJSON 构造内联 video_model 文本。
func playModelJSON(t *testing.T, model map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("序列化测试模型失败: %v", err)
	}
	return string(raw)
}

// playResponse 构造 data[vid].video_model 形式的取址响应。
func playResponse(t *testing.T, vid, modelJSON string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"code": 0,
		"data": map[string]any{vid: map[string]any{"video_model": modelJSON}},
	})
	if err != nil {
		t.Fatalf("序列化测试响应失败: %v", err)
	}
	return body
}

// mockPlayItem 是源单测 mockEntry 里的片源条目形状。
func mockPlayItem(vid string) map[string]any {
	return map[string]any{
		"definition": "1080p",
		"quality":    "1080p",
		"vtype":      "mp4",
		"codec_type": "bytevc1",
		"main_url":   "https://qznovelvod.com/video_" + vid + ".mp4",
		"size":       1000,
		"vwidth":     1080,
		"vheight":    1920,
		"encrypt_info": map[string]any{
			"encrypt": false,
		},
	}
}

// gunzip 解压响应/请求体。
func gunzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("响应体不是 gzip: %v", err)
	}
	defer reader.Close()
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	return plain
}

// TestPlaySendsSignedGzipPost 锁定取址请求形状（对齐源实现 playbackRequest）。
func TestPlaySendsSignedGzipPost(t *testing.T) {
	const vid = "30002"

	var (
		gotMethod   string
		gotPath     string
		gotHeaders  http.Header
		gotQuery    url.Values
		gotRawQuery string
		gotBody     []byte
	)
	model := playModelJSON(t, map[string]any{"video_list": []any{mockPlayItem(vid)}})
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		gotQuery = r.URL.Query()
		gotRawQuery = r.URL.RawQuery
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(playResponse(t, vid, model))
	}))

	info, err := c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/player" {
		t.Fatalf("request = %s %s, want POST /player", gotMethod, gotPath)
	}
	for key, want := range map[string]string{
		"Accept":               "application/json; charset=utf-8,application/x-protobuf",
		"Content-Type":         "application/json; charset=UTF-8",
		"Content-Encoding":     "gzip",
		"sdk-version":          "2",
		"passport-sdk-version": "5051452",
		"x-xs-from-web":        "0",
		"x-ss-dp":              "8662",
	} {
		if got := gotHeaders.Get(key); got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
	// 签名必须作用于实际发送的 gzip 字节。
	if stub := gotHeaders.Get("x-ss-stub"); stub != sign.MD5Upper(gotBody) {
		t.Fatalf("x-ss-stub = %s, want %s", stub, sign.MD5Upper(gotBody))
	}
	for _, header := range []string{"x-khronos", "x-ladon", "x-argus", "x-ss-req-ticket"} {
		if gotHeaders.Get(header) == "" {
			t.Errorf("缺少签名头 %s", header)
		}
	}
	if ua := gotHeaders.Get("User-Agent"); !strings.Contains(ua, "74332") {
		t.Errorf("user-agent = %q, want 74332", ua)
	}
	if cookie := gotHeaders.Get("Cookie"); cookie != "store-region=cn-bj; store-region-src=did; install_id=1624456208327972" {
		t.Errorf("cookie = %q", cookie)
	}

	// query 的参数与顺序都必须与源实现的 PLAYBACK_DEVICE 展开一致（签名对 query 字节敏感）。
	const wantQueryPrefix = "ac=wifi&channel=update_64&aid=8662&app_name=novelread&version_code=74332&" +
		"version_name=7.4.3.32&device_platform=android&os=android&ssmix=a&device_type=25053RT47C&" +
		"device_brand=Redmi&language=zh&os_api=36&os_version=16&manifest_version_code=74332&" +
		"resolution=1280*2772&dpi=520&update_version_code=74332&host_abi=arm64-v8a&" +
		"dragon_device_type=phone&pv_player=74332&compliance_status=0&need_personal_recommend=1&" +
		"player_so_load=1&is_android_pad_screen=0&device_id=1624456208323876&" +
		"iid=1624456208327972&cdid=91d56f7e-c027-4ce9-9a3c-229d59a13c3b&_rticket="
	if !strings.HasPrefix(gotRawQuery, wantQueryPrefix) {
		t.Fatalf("query = %s\nwant 前缀 %s", gotRawQuery, wantQueryPrefix)
	}
	if ticket := gotQuery.Get("_rticket"); len(ticket) != 13 {
		t.Errorf("_rticket = %q, want 毫秒时间戳", ticket)
	}

	// 请求体：gzip 后是源实现约定的 JSON 形状。
	var payload map[string]any
	if err := json.Unmarshal(gunzip(t, gotBody), &payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if payload["dr_scene"] != "player" {
		t.Errorf("dr_scene = %v", payload["dr_scene"])
	}
	biz, _ := payload["biz_param"].(map[string]any)
	if biz["video_platform"] != float64(1024) || biz["need_all_video_definition"] != true {
		t.Errorf("biz_param = %v", biz)
	}
	ids, _ := payload["mixed_video_id_map"].(map[string]any)
	batch, _ := ids["1004"].([]any)
	if len(batch) != 1 || batch[0] != vid {
		t.Errorf("mixed_video_id_map[1004] = %v, want [%s]", batch, vid)
	}

	// 解析结果：bytevc1 → hevc，明文片源不带 content_key。
	if info.Codec != "hevc" {
		t.Errorf("codec = %q, want hevc", info.Codec)
	}
	if info.Encrypted {
		t.Error("encrypted = true, want false")
	}
	if info.ContentKey != "" {
		t.Errorf("content_key = %q, want omitted", info.ContentKey)
	}
	if info.URL != "https://qznovelvod.com/video_30002.mp4" {
		t.Errorf("url = %q", info.URL)
	}
	if info.Definition != "1080p" {
		t.Errorf("definition = %q", info.Definition)
	}
	if info.VideoMeta == nil || info.VideoMeta.VHeight != 1920 || info.VideoMeta.CodecType != "bytevc1" {
		t.Errorf("video_meta = %+v", info.VideoMeta)
	}
}

// TestPlayEncryptionInfoRules 对齐源单测：encrypt_info 缺失可用，
// 声明加密但没有 spade_a 时视为无效响应。
func TestPlayEncryptionInfoRules(t *testing.T) {
	const vid = "30002"

	cases := []struct {
		name       string
		encrypt    any
		present    bool
		wantErr    bool
		errMessage string
	}{
		{name: "缺少 encrypt_info", present: false},
		{name: "显式未加密", present: true, encrypt: false},
		{name: "声明加密但无密钥", present: true, encrypt: true, wantErr: true},
		{name: "空 encrypt_info", present: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := mockPlayItem(vid)
			if tc.present {
				item["encrypt_info"] = map[string]any{"encrypt": tc.encrypt}
			} else {
				delete(item, "encrypt_info")
			}
			model := playModelJSON(t, map[string]any{"video_list": []any{item}})
			c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(playResponse(t, vid, model))
			}))

			info, err := c.Play(context.Background(), vid)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", info)
				}
				return
			}
			if err != nil {
				t.Fatalf("Play: %v", err)
			}
			if info.Encrypted || info.ContentKey != "" {
				t.Fatalf("encrypted = %v, content_key = %q", info.Encrypted, info.ContentKey)
			}
		})
	}
}

// TestPlayReportsByteVC2OnlyWithoutCompatibleSource 对齐源单测：
// 只有 ByteVC2 片源时报告不支持，同模型存在兼容片源时不得报该错。
func TestPlayReportsByteVC2OnlyWithoutCompatibleSource(t *testing.T) {
	const vid = "30001"

	bytevc2 := func() map[string]any {
		item := mockPlayItem(vid)
		delete(item, "codec_type")
		item["video_meta"] = map[string]any{"codec_type": "bytevc2"}
		return item
	}

	t.Run("只有 ByteVC2", func(t *testing.T) {
		model := playModelJSON(t, map[string]any{"video_list": []any{bytevc2()}})
		c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(playResponse(t, vid, model))
		}))
		_, err := c.Play(context.Background(), vid)
		if err == nil || !strings.Contains(err.Error(), "ByteVC2") {
			t.Fatalf("err = %v, want ByteVC2", err)
		}
	})

	t.Run("存在兼容片源", func(t *testing.T) {
		compatible := mockPlayItem(vid)
		compatible["vwidth"] = 640
		compatible["vheight"] = 360
		compatible["definition"] = "360p"
		model := playModelJSON(t, map[string]any{"video_list": []any{bytevc2(), compatible}})
		c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(playResponse(t, vid, model))
		}))
		info, err := c.Play(context.Background(), vid)
		if err != nil {
			t.Fatalf("Play: %v", err)
		}
		if info.Codec != "hevc" {
			t.Fatalf("codec = %q, want hevc (兼容片源)", info.Codec)
		}
	})
}

// TestPlayFallsBackToSecondaryThenShortPlay 校验兜底顺序与空响应处理。
func TestPlayFallsBackToSecondaryThenShortPlay(t *testing.T) {
	const vid = "30003"

	var paths []string
	model := playModelJSON(t, map[string]any{"video_list": []any{mockPlayItem(vid)}})
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/shortplay":
			// 短剧接口的响应形状同样是 data[vid]。
			_, _ = w.Write(playResponse(t, vid, model))
		default:
			// HTTP 200 + 空响应体：必须视为失败并继续兜底。
			w.WriteHeader(http.StatusOK)
		}
	}))

	info, err := c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if info.URL != "https://qznovelvod.com/video_30003.mp4" {
		t.Fatalf("url = %q", info.URL)
	}
	want := []string{"/player", "/player-fallback", "/shortplay"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i, path := range want {
		if paths[i] != path {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

// TestPlayReportsUnavailableWhenAllChannelsFail 三条通道全失败时报「取址不可用」。
func TestPlayReportsUnavailableWhenAllChannelsFail(t *testing.T) {
	var paths []string
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))

	_, err := c.Play(context.Background(), "30004")
	if err == nil || !strings.Contains(err.Error(), "取址不可用") {
		t.Fatalf("err = %v, want 取址不可用", err)
	}
	if len(paths) != 3 {
		t.Fatalf("paths = %v, want 3 次请求", paths)
	}
}

// TestPlayUsesModelFallbackAPI 模型没有内联 video_list 时改用 fallback_api。
func TestPlayUsesModelFallbackAPI(t *testing.T) {
	const vid = "30005"

	var (
		base    string
		gotPath string
		gotUA   string
	)
	model := playModelJSON(t, map[string]any{"fallback_api": ""})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/player":
			_, _ = w.Write(playResponse(t, vid, model))
		case "/secondary-model":
			gotPath = r.URL.Path
			gotUA = r.Header.Get("User-Agent")
			body, _ := json.Marshal(map[string]any{
				"code":       0,
				"video_info": map[string]any{"data": map[string]any{"video_list": []any{mockPlayItem(vid)}}},
			})
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	// fallback_api 必须在模型返回时已知，因此这里回填带地址的模型。
	model = playModelJSON(t, map[string]any{"fallback_api": base + "/secondary-model"})

	c := newPlayClientFor(t, srv)
	info, err := c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if gotPath != "/secondary-model" {
		t.Fatalf("fallback path = %q", gotPath)
	}
	if !strings.Contains(gotUA, "71332") {
		t.Fatalf("fallback UA = %q, want videoUA", gotUA)
	}
	if info.URL != "https://qznovelvod.com/video_30005.mp4" {
		t.Fatalf("url = %q", info.URL)
	}
}

// TestPlayAcceptsShortplayModelShape 短剧接口直接返回 video_model 时也可用。
func TestPlayAcceptsShortplayModelShape(t *testing.T) {
	const vid = "30011"

	model := playModelJSON(t, map[string]any{"video_list": []any{mockPlayItem(vid)}})
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shortplay" {
			body, err := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"video_model": model}})
			if err != nil {
				t.Errorf("序列化短剧响应失败: %v", err)
			}
			_, _ = w.Write(body)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	info, err := c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if info.URL != "https://qznovelvod.com/video_30011.mp4" {
		t.Fatalf("url = %q", info.URL)
	}
}

// TestShortplayChannelRequestShape 锁定短剧通道的请求形状（对齐源实现 shortplayRequest）。
func TestShortplayChannelRequestShape(t *testing.T) {
	const vid = "30012"

	var (
		gotPath     string
		gotHeaders  http.Header
		gotQuery    url.Values
		gotRawQuery string
		gotBody     []byte
	)
	model := playModelJSON(t, map[string]any{"video_list": []any{mockPlayItem(vid)}})
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shortplay" {
			gotPath = r.URL.Path
			gotHeaders = r.Header.Clone()
			gotQuery = r.URL.Query()
			gotRawQuery = r.URL.RawQuery
			gotBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write(playResponse(t, vid, model))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	if _, err := c.Play(context.Background(), vid); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if gotPath != "/shortplay" {
		t.Fatalf("path = %q", gotPath)
	}
	if ua := gotHeaders.Get("User-Agent"); ua != "com.dragon.read" {
		t.Errorf("user-agent = %q", ua)
	}
	if ct := gotHeaders.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	// 短剧请求体不压缩，签名直接作用于 JSON 字节。
	if gotHeaders.Get("Content-Encoding") != "" {
		t.Errorf("content-encoding = %q, want 空", gotHeaders.Get("Content-Encoding"))
	}
	if stub := gotHeaders.Get("x-ss-stub"); stub != sign.MD5Upper(gotBody) {
		t.Errorf("x-ss-stub = %s, want %s", stub, sign.MD5Upper(gotBody))
	}

	const wantQueryPrefix = "iid=1624456208327972&device_id=1624456208323876&aid=1967&app_name=novelapp&" +
		"version_code=72132&version_name=7.2.1.32&device_platform=android&device_brand=Xiaomi&" +
		"os_version=13&cdid=91d56f7e-c027-4ce9-9a3c-229d59a13c3b"
	if gotRawQuery != wantQueryPrefix {
		t.Fatalf("query = %s\nwant %s", gotRawQuery, wantQueryPrefix)
	}
	if gotQuery.Get("aid") != "1967" {
		t.Errorf("aid = %q", gotQuery.Get("aid"))
	}

	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if payload["video_id"] != vid || payload["content_type"] != float64(1004) {
		t.Errorf("payload = %v", payload)
	}
	biz, _ := payload["biz_param"].(map[string]any)
	if biz["source"] != float64(4) || biz["video_platform"] != float64(3) {
		t.Errorf("biz_param = %v", biz)
	}
}

// TestPlayMapsBackupURL 同一条目的 main_url / backup_url 映射为 url / backup_url。
func TestPlayMapsBackupURL(t *testing.T) {
	const vid = "30013"

	t.Run("主备地址不同", func(t *testing.T) {
		item := mockPlayItem(vid)
		item["backup_url"] = "https://fqnovelvod.com/video_backup.mp4"
		model := playModelJSON(t, map[string]any{"video_list": []any{item}})
		c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(playResponse(t, vid, model))
		}))

		info, err := c.Play(context.Background(), vid)
		if err != nil {
			t.Fatalf("Play: %v", err)
		}
		if info.URL != "https://qznovelvod.com/video_30013.mp4" {
			t.Errorf("url = %q", info.URL)
		}
		if info.BackupURL != "https://fqnovelvod.com/video_backup.mp4" {
			t.Errorf("backup_url = %q", info.BackupURL)
		}
	})

	t.Run("主备地址相同则省略", func(t *testing.T) {
		item := mockPlayItem(vid)
		item["backup_url"] = item["main_url"]
		model := playModelJSON(t, map[string]any{"video_list": []any{item}})
		c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(playResponse(t, vid, model))
		}))

		info, err := c.Play(context.Background(), vid)
		if err != nil {
			t.Fatalf("Play: %v", err)
		}
		if info.BackupURL != "" {
			t.Errorf("backup_url = %q, want 省略", info.BackupURL)
		}
	})
}

// TestPlaySelectsHighestResolution 多档分辨率时取最高档，同档按编码等级升序。
func TestPlaySelectsHighestResolution(t *testing.T) {
	const vid = "30006"

	low := mockPlayItem(vid)
	low["definition"] = "720p"
	low["vwidth"] = 720
	low["vheight"] = 1280
	low["main_url"] = "https://qznovelvod.com/video_low.mp4"

	high := mockPlayItem(vid)
	high["main_url"] = "https://qznovelvod.com/video_high.mp4"

	model := playModelJSON(t, map[string]any{"video_list": []any{low, high}})
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(playResponse(t, vid, model))
	}))

	info, err := c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if info.URL != "https://qznovelvod.com/video_high.mp4" {
		t.Fatalf("url = %q, want 1080p", info.URL)
	}
	if info.Definition != "1080p" {
		t.Fatalf("definition = %q", info.Definition)
	}
}

// TestPlayRejectsUntrustedSourceURLs 片源地址白名单校验失败时丢弃候选。
func TestPlayRejectsUntrustedSourceURLs(t *testing.T) {
	const vid = "30007"

	t.Run("站外域名", func(t *testing.T) {
		item := mockPlayItem(vid)
		item["main_url"] = "https://evil.example.com/video.mp4"
		model := playModelJSON(t, map[string]any{"video_list": []any{item}})
		c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(playResponse(t, vid, model))
		}))
		if _, err := c.Play(context.Background(), vid); err == nil {
			t.Fatal("期望拒绝站外片源地址")
		}
	})

	t.Run("非 https", func(t *testing.T) {
		item := mockPlayItem(vid)
		item["main_url"] = "http://qznovelvod.com/video.mp4"
		model := playModelJSON(t, map[string]any{"video_list": []any{item}})
		c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(playResponse(t, vid, model))
		}))
		if _, err := c.Play(context.Background(), vid); err == nil {
			t.Fatal("期望拒绝非 https 片源地址")
		}
	})

	t.Run("主地址被拒时回落到备用地址", func(t *testing.T) {
		item := mockPlayItem(vid)
		item["main_url"] = "https://evil.example.com/video.mp4"
		item["backup_url"] = "https://fqnovelvod.com/video_backup.mp4"
		model := playModelJSON(t, map[string]any{"video_list": []any{item}})
		c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(playResponse(t, vid, model))
		}))
		info, err := c.Play(context.Background(), vid)
		if err != nil {
			t.Fatalf("Play: %v", err)
		}
		if info.URL != "https://fqnovelvod.com/video_backup.mp4" {
			t.Fatalf("url = %q, want 备用地址", info.URL)
		}
	})

	t.Run("来源为子域也允许", func(t *testing.T) {
		item := mockPlayItem(vid)
		item["main_url"] = "https://v3.qznovelvod.com/video.mp4"
		model := playModelJSON(t, map[string]any{"video_list": []any{item}})
		c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(playResponse(t, vid, model))
		}))
		if _, err := c.Play(context.Background(), vid); err != nil {
			t.Fatalf("Play: %v", err)
		}
	})
}

// TestPlayDerivesSpadeContentKey spade_a 存在时派生内容密钥并标记加密。
func TestPlayDerivesSpadeContentKey(t *testing.T) {
	const vid = "30008"

	for _, tc := range []struct{ name, input, key string }{
		{"标准 base64", spadeAInput, spadeAKey},
		{"base64url 字母表", spadeBInput, spadeBKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := mockPlayItem(vid)
			item["spade_a"] = tc.input
			model := playModelJSON(t, map[string]any{"video_list": []any{item}})
			c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(playResponse(t, vid, model))
			}))

			info, err := c.Play(context.Background(), vid)
			if err != nil {
				t.Fatalf("Play: %v", err)
			}
			if !info.Encrypted || info.ContentKey != tc.key {
				t.Fatalf("encrypted = %v, content_key = %q, want %q", info.Encrypted, info.ContentKey, tc.key)
			}
		})
	}
}

// TestPlayDecryptsSpadeURLWithKeySeed 模型带 key_seed 时片源地址需先解密。
func TestPlayDecryptsSpadeURLWithKeySeed(t *testing.T) {
	const vid = "30002"

	item := mockPlayItem(vid)
	item["main_url"] = spadeCipher
	model := playModelJSON(t, map[string]any{
		"video_list": []any{item},
		"key_seed":   spadeSeed,
	})
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(playResponse(t, vid, model))
	}))

	info, err := c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if info.URL != spadePlain {
		t.Fatalf("url = %q, want %q", info.URL, spadePlain)
	}

	// base64url 且省略填充的密文同样可用。
	item["main_url"] = spadeCipherA
	model = playModelJSON(t, map[string]any{"video_list": []any{item}, "key_seed": spadeSeed})
	c = newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(playResponse(t, vid, model))
	}))
	info, err = c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if info.URL != spadePlainB {
		t.Fatalf("url = %q, want %q", info.URL, spadePlainB)
	}
}

// TestPlayRejectsMissingEntry 响应缺少 data[vid] 时报错。
func TestPlayRejectsMissingEntry(t *testing.T) {
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"99999":{"video_model":"{}"}}}`))
	}))
	_, err := c.Play(context.Background(), "30009")
	if err == nil || !strings.Contains(err.Error(), "未返回所选分集") {
		t.Fatalf("err = %v", err)
	}
}

// TestPlayRejectsBusinessRejection 业务状态码非 0 时报错。
func TestPlayRejectsBusinessRejection(t *testing.T) {
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":10001,"message":"rejected"}`))
	}))
	_, err := c.Play(context.Background(), "30010")
	if err == nil || !strings.Contains(err.Error(), "暂未提供本集播放信息") {
		t.Fatalf("err = %v", err)
	}
}

// TestPlayKeySeedAcceptsBase64Variants key_seed 需兼容标准与 base64url 两种字母表。
func TestPlayKeySeedAcceptsBase64Variants(t *testing.T) {
	want, err := hex.DecodeString("fbffef0012345678")
	if err != nil {
		t.Fatalf("构造期望值失败: %v", err)
	}
	for _, input := range []string{"+//vABI0Vng=", "+//vABI0Vng", "-__vABI0Vng"} {
		got, err := playKeySeed(map[string]any{"key_seed": input})
		if err != nil {
			t.Fatalf("playKeySeed(%q): %v", input, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("playKeySeed(%q) = %x, want %x", input, got, want)
		}
	}

	if seed, err := playKeySeed(map[string]any{}); err != nil || seed != nil {
		t.Fatalf("缺失 key_seed 应返回 nil, got %x err=%v", seed, err)
	}
	if _, err := playKeySeed(map[string]any{"key_seed": "!!!not-base64!!!"}); err == nil {
		t.Fatal("非法 key_seed 应报错")
	}
}
