package hgproto

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"hgplayer/internal/sign"
)

// newTestClient 创建一个指向本地测试服务器的协议客户端。
//
// 测试服务器域名不在 guard 白名单内，因此直接构造一个绕过 guard 的
// 客户端，仅验证协议层的请求构造与响应解析。
func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	s := sign.SignerFunc(func(sign.Signature) sign.Headers {
		return sign.Headers{"X-Test": "1"}
	})
	c := NewClient(s, DefaultDevice("1624456208323876", "1624456208327972", "cdid-1"), WithGuard(nil))
	// 用测试服务器地址覆盖端点。
	c.videoAPI = srv.URL + "/player"
	c.searchAPI = srv.URL + "/search"
	c.seriesAPI = srv.URL + "/bookinfo"
	c.mdHost = srv.URL
	return c
}

func TestBrowseListParsesCards(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"video_data": []any{
					map[string]any{
						"series_id":   "12345",
						"title":       "示例短剧",
						"cover":       "https://fqnovelpic.com/novel-pic/abc~x.heic",
						"episode_cnt": 30.0,
						"sub_title_list": []any{
							map[string]any{"content": "甜宠"},
							map[string]any{"content": "逆袭"},
						},
					},
				},
				"has_more": true,
			},
		})
	}))

	res, err := c.BrowseList(context.Background(), categoryRealDrama, "", 1)
	if err != nil {
		t.Fatalf("BrowseList: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("cards = %d, want 1", len(res.Results))
	}
	card := res.Results[0]
	if card.SeriesID != "12345" || card.SeriesTitle != "示例短剧" {
		t.Fatalf("card = %+v", card)
	}
	if card.EpisodeCount != 30 {
		t.Fatalf("episode_count = %d", card.EpisodeCount)
	}
	if len(card.Tags) != 2 {
		t.Fatalf("tags = %v", card.Tags)
	}
	// HEIC 封面应转为 JPEG。
	if card.Cover != "https://p3-novel.byteimg.com/novel-pic/abc~tplv-shrink:640:0.image" {
		t.Fatalf("cover = %s", card.Cover)
	}
	if !res.HasMore {
		t.Fatal("has_more should be true")
	}
}

func TestBrowseListRejectsInvalidCategory(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if _, err := c.BrowseList(context.Background(), "bogus", "", 1); err == nil {
		t.Fatal("expected error for invalid category")
	}
}

func TestBrowseListRejectsInvalidPage(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if _, err := c.BrowseList(context.Background(), categoryRealDrama, "", 0); err == nil {
		t.Fatal("expected error for page 0")
	}
	if _, err := c.BrowseList(context.Background(), categoryRealDrama, "", 51); err == nil {
		t.Fatal("expected error for page 51")
	}
}

func TestSearchParsesResults(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"book_list": []any{
					map[string]any{"series_id": "999", "title": "搜索结果一"},
					map[string]any{"series_id": "998", "title": "搜索结果二"},
				},
			},
		})
	}))

	res, err := c.Search(context.Background(), "短剧", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(res.Results))
	}
}

func TestSearchRejectsEmptyKeyword(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if _, err := c.Search(context.Background(), "  ", 1); err == nil {
		t.Fatal("expected error for empty keyword")
	}
}

func TestSeriesInjectsLocalProgress(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"title": "剧集A",
				"episode_list": []any{
					map[string]any{"vid": "v1", "title": "第1集"},
					map[string]any{"vid": "v2", "title": "第2集"},
				},
			},
		})
	}))

	detail, err := c.Series(context.Background(), "12345", &SavedProgress{EpisodeIndex: 2, PositionSec: 42, DurationSec: 100}, true)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if detail.SeriesTitle != "剧集A" {
		t.Fatalf("title = %s", detail.SeriesTitle)
	}
	if len(detail.Episodes) != 2 {
		t.Fatalf("episodes = %d", len(detail.Episodes))
	}
	if !detail.IsFavorite {
		t.Fatal("is_favorite should be true")
	}
	if detail.SavedProgress == nil || detail.SavedProgress.EpisodeIndex != 2 {
		t.Fatalf("saved_progress = %+v", detail.SavedProgress)
	}
	// 进度应对齐到第 2 集。
	if detail.Episodes[1].PositionSec != 42 {
		t.Fatalf("episode 2 position = %d", detail.Episodes[1].PositionSec)
	}
}

func TestSeriesRejectsInvalidID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if _, err := c.Series(context.Background(), "abc", nil, false); err == nil {
		t.Fatal("expected error for non-numeric series id")
	}
}

func TestPlayParsesVideoModel(t *testing.T) {
	videoModel := `{"main_url":"https://cdn.example.com/a.mp4","backup_url":"https://cdn2.example.com/a.mp4",
	  "video_meta":{"definition":"1080p","vwidth":1080,"vheight":1920,"bitrate":2000000,"codec_type":"h265"},
	  "encrypt_info":{"encrypt":true,"spade_a":"deadbeef"}}`

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"video_model": videoModel},
		})
	}))

	info, err := c.Play(context.Background(), "vid-1")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if info.URL != "https://cdn.example.com/a.mp4" {
		t.Fatalf("url = %s", info.URL)
	}
	if !info.Encrypted {
		t.Fatal("encrypted should be true")
	}
	if info.ContentKey != "deadbeef" {
		t.Fatalf("content_key = %s", info.ContentKey)
	}
	if info.Codec != "h265" {
		t.Fatalf("codec = %s", info.Codec)
	}
	if info.VideoMeta == nil || info.VideoMeta.VHeight != 1920 {
		t.Fatalf("video_meta = %+v", info.VideoMeta)
	}
}

func TestPlayRejectsEmptyVid(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if _, err := c.Play(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty vid")
	}
}

// TestBrowseListSendsSignedPOST 锁定目录请求形状：
// POST + 元数据签名头 + JSON body，且 query 的签名分支为 0。
func TestBrowseListSendsSignedPOST(t *testing.T) {
	var gotMethod, gotTarget string
	var gotHeaders http.Header
	var gotBody []byte

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotTarget = r.URL.String()
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"video_data":[],"has_more":false}}`))
	}))

	if _, err := c.BrowseList(context.Background(), "real-drama", "", 1); err != nil {
		t.Fatalf("BrowseList: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	for _, h := range []string{"x-gorgon", "x-helios", "x-ladon", "x-argus", "x-khronos", "x-ss-req-ticket", "x-ss-stub"} {
		if gotHeaders.Get(h) == "" {
			t.Errorf("缺少签名头 %s", h)
		}
	}

	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("body 不是合法 JSON: %v", err)
	}
	if body["req_scene"] != "default" {
		t.Errorf("req_scene = %v, want default", body["req_scene"])
	}
	if body["limit"] != float64(15) {
		t.Errorf("limit = %v, want 15", body["limit"])
	}
	items, ok := body["select_items"].(map[string]any)
	if !ok {
		t.Fatal("缺少 select_items")
	}
	if genre, _ := items["genre"].([]any); len(genre) != 1 || genre[0] != "short_play" {
		t.Errorf("select_items.genre = %v, want [short_play]", items["genre"])
	}

	// query 必须携带 ts 与 _rticket，且签名分支为 0。
	if !strings.Contains(gotTarget, "ts=") || !strings.Contains(gotTarget, "_rticket=") {
		t.Errorf("query 缺少 ts/_rticket: %s", gotTarget)
	}
	khronos, err := strconv.ParseUint(gotHeaders.Get("x-khronos"), 10, 32)
	if err != nil {
		t.Fatalf("x-khronos 非法: %v", err)
	}
	if br := sign.BranchOf(gotTarget, gotBody, uint32(khronos)); br != 0 {
		t.Errorf("branchOf = %d, want 0", br)
	}
}
