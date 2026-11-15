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
// 取址端点指向本地服务器，保证测试完全离线。
func newPlayClientFor(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := NewClient(sign.PlayerSigner{}, DefaultDevice("1624456208323876", "1624456208327972", "91d56f7e-c027-4ce9-9a3c-229d59a13c3b"), WithGuard(nil))
	c.playbackAPI = srv.URL + "/novel/player/video_model/v1/"
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

// playResponse 构造 data.video_model 形式的取址响应（在役客户端形状）。
func playResponse(t *testing.T, _ string, modelJSON string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"code": 0,
		"data": map[string]any{"video_model": modelJSON},
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
// TestPlaySendsSignedPlaybackRequest 锁定取址请求形状：POST 到 video_model 路径、
// 真实 Argus 551 / Ladon 信封、不带 x-medusa、body 为 {video_id, biz_param}。
func TestPlaySendsSignedPlaybackRequest(t *testing.T) {
	const vid = "30002"

	var (
		gotMethod  string
		gotPath    string
		gotHeaders http.Header
		gotRaw     string
		gotBody    []byte
	)
	model := playModelJSON(t, map[string]any{"video_list": []any{mockPlayItem(vid)}})
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotHeaders = r.Header.Clone()
		gotRaw = r.URL.RawQuery
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(playResponse(t, vid, model))
	}))

	info, err := c.Play(context.Background(), vid)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/novel/player/video_model/v1/" {
		t.Fatalf("request = %s %s, want POST /novel/player/video_model/v1/", gotMethod, gotPath)
	}
	for key, want := range map[string]string{
		"Accept":                    "application/json; charset=utf-8,application/x-protobuf",
		"Content-Type":              "application/json; charset=UTF-8",
		"x-xs-from-web":             "0",
		"x-tt-request-tag":          "t=0;n=0",
		"sdk-version":               "2",
		"passport-sdk-version":      "50561",
		"x-vc-bdturing-sdk-version": "3.7.2.cn",
		"x-ss-dp":                   "8662",
		"lc":                        "101",
		"x-tt-dt":                   "",
	} {
		if got := gotHeaders.Get(key); got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
	// 取址必须用真实 551 信封，且不得携带 x-medusa。
	if _, ok := gotHeaders["x-medusa"]; ok {
		t.Fatal("取址请求不得携带 x-medusa")
	}
	if argus := gotHeaders.Get("x-argus"); len(argus) < 64 || !strings.HasPrefix(argus, "8o") {
		t.Fatalf("x-argus 不是真实信封: %q", argus)
	}
	if ladon := gotHeaders.Get("x-ladon"); len(ladon) < 40 {
		t.Fatalf("x-ladon 过短: %q", ladon)
	}
	if stub := gotHeaders.Get("x-ss-stub"); stub != sign.MD5Upper(gotBody) {
		t.Fatalf("x-ss-stub = %s, want %s", stub, sign.MD5Upper(gotBody))
	}
	for _, header := range []string{"x-khronos", "x-gorgon", "x-helios", "x-ss-req-ticket", "x-reading-request"} {
		if gotHeaders.Get(header) == "" {
			t.Errorf("缺少头 %s", header)
		}
	}
	if ua := gotHeaders.Get("User-Agent"); !strings.Contains(ua, "73932") {
		t.Errorf("user-agent = %q, want 73932", ua)
	}

	// query 顺序即签名输入，必须与在役客户端一致。
	const wantQueryPrefix = "iid=1624456208327972&device_id=1624456208323876&ac=wifi&channel=update_64&" +
		"aid=8662&app_name=novelread&cdid=91d56f7e-c027-4ce9-9a3c-229d59a13c3b&" +
		"version_code=73932&version_name=7.3.9.32&device_platform=android&os=android&ssmix=a&" +
		"device_type=25053RT47C&device_brand=Redmi&language=zh&os_api=36&os_version=16&" +
		"manifest_version_code=73932&resolution=1280*2772&dpi=520&update_version_code=73932&" +
		"host_abi=arm64-v8a&dragon_device_type=phone&pv_player=73932&compliance_status=0&" +
		"need_personal_recommend=1&player_so_load=1&is_android_pad_screen=0&openudid=&ts="
	if !strings.HasPrefix(gotRaw, wantQueryPrefix) {
		t.Fatalf("query = %s\nwant 前缀 %s", gotRaw, wantQueryPrefix)
	}

	// 请求体：{video_id, biz_param}，不压缩。
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if payload["video_id"] != vid {
		t.Errorf("video_id = %v", payload["video_id"])
	}
	biz, _ := payload["biz_param"].(map[string]any)
	if biz["video_platform"] != float64(3) || biz["need_all_video_definition"] != true {
		t.Errorf("biz_param = %v", biz)
	}

	// 解析结果：bytevc1 → hevc，明文片源不带 content_key。
	if info.Codec != "hevc" || info.Encrypted || info.ContentKey != "" {
		t.Errorf("info = %+v", info)
	}
	if info.URL != "https://qznovelvod.com/video_30002.mp4" || info.Definition != "1080p" {
		t.Errorf("info = %+v", info)
	}
}

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
		case "/novel/player/video_model/v1/":
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
// TestPlayRejectsMissingModel 响应缺 data.video_model 时报错。
func TestPlayRejectsMissingModel(t *testing.T) {
	c := newPlayClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	_, err := c.Play(context.Background(), "30009")
	if err == nil || !strings.Contains(err.Error(), "未返回本集片源模型") {
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
