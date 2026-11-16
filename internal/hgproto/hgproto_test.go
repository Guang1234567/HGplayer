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
	// 用测试服务器地址覆盖端点（含兜底链，确保测试不会访问真实上游）。
	c.videoAPI = srv.URL + "/player"
	c.videoAPIFallback = srv.URL + "/player-fallback"
	c.shortPlayAPI = srv.URL + "/shortplay"
	c.searchAPI = srv.URL + "/search"
	c.siteOrigin = srv.URL
	c.mdHost = srv.URL
	c.websiteAPI = srv.URL + "/api/search/result"
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

func TestSearchParsesWebsiteResults(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"searchList": []any{
				map[string]any{
					"name": "搜索结果一",
					"video_data": map[string]any{
						"series_id": "1000999", "series_title": "搜索结果一",
						"series_intro": "都市短剧", "episode_cnt": 30.0,
						"tags": []any{"都市", "甜宠"},
					},
				},
				// 关键词不匹配的条目应被过滤掉（上游会塞热门剧目）。
				map[string]any{
					"name": "无关剧目",
					"video_data": map[string]any{
						"series_id": "1000998", "series_title": "无关剧目", "series_intro": "古装",
					},
				},
			},
		})
	}))

	res, err := c.Search(context.Background(), "都市", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(res.Results))
	}
	card := res.Results[0]
	if card.SeriesID != "1000999" || card.SeriesTitle != "搜索结果一" || card.EpisodeCount != 30 {
		t.Fatalf("card = %+v", card)
	}
	if len(card.Tags) != 2 {
		t.Fatalf("tags = %v", card.Tags)
	}
}

// 官网链路失败时必须回退到 App 端签名搜索。
func TestSearchFallsBackToApp(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/search/result") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
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
	payload := seriesRouterPayload("12345", "剧集A", []any{"111", "222"})
	page := seriesPageHTML(t, payload)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
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
	if detail.Episodes[0].Vid != "111" || detail.Episodes[1].EpisodeIndex != 2 {
		t.Fatalf("episodes = %+v", detail.Episodes)
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

// TestPlayRejectsEmptyVid 校验分集编号校验（源实现要求 1..30 位数字）。
func TestPlayRejectsEmptyVid(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, vid := range []string{"", "abc", "vid-1", strings.Repeat("9", 31)} {
		if _, err := c.Play(context.Background(), vid); err == nil {
			t.Fatalf("expected error for vid %q", vid)
		}
	}
}

// 榜单把卡片包在 cell_view.cell_data[].video_data 中，单对象与数组都要解析。
func TestBrowseListParsesRankingShape(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reading/bookapi/bookmall/cell/change/v" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"cell_view":{"has_more":true,"cell_data":[
			{"video_data":[{"series_id":"1001","title":"榜首短剧","episode_cnt":12}]},
			{"video_data":{"series_id":"1002","title":"第二部"}},
			{"ugc_user_data":[]}
		]}}}`))
	}))

	res, err := c.BrowseList(context.Background(), categoryHot, "", 1)
	if err != nil {
		t.Fatalf("BrowseList: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("cards = %d, want 2", len(res.Results))
	}
	if res.Results[0].SeriesID != "1001" || res.Results[0].SeriesTitle != "榜首短剧" {
		t.Fatalf("card0 = %+v", res.Results[0])
	}
	if res.Results[1].SeriesID != "1002" {
		t.Fatalf("card1 = %+v", res.Results[1])
	}
	if !res.HasMore {
		t.Fatal("has_more 应取自 cell_view")
	}
}

// TestBrowseListUsesSharedCategoryPath 锁定分类页路径：上游只有统一入口，
// 类别只体现在 req_scene。
//
// 拼出分类专用路径（如 /landpage/ai/v/）会被上游返回 404。
func TestBrowseListUsesSharedCategoryPath(t *testing.T) {
	cases := []struct {
		category string
		reqScene string
		genre    string
	}{
		{categoryRealDrama, "default", "short_play"},
		{categoryComic, "comic_series", "comic_series"},
		{categoryAI, "ai_series", "ai_series"},
	}
	for _, tc := range cases {
		t.Run(tc.category, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any

			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":0,"data":{"video_data":[],"has_more":false}}`))
			}))

			if _, err := c.BrowseList(context.Background(), tc.category, "", 1); err != nil {
				t.Fatalf("BrowseList(%s): %v", tc.category, err)
			}
			if gotPath != "/reading/distribution/category/landpage/v/" {
				t.Errorf("path = %s, want 共用分类入口", gotPath)
			}
			if gotBody["req_scene"] != tc.reqScene {
				t.Errorf("req_scene = %v, want %s", gotBody["req_scene"], tc.reqScene)
			}
			items, _ := gotBody["select_items"].(map[string]any)
			genres, _ := items["genre"].([]any)
			if len(genres) != 1 || genres[0] != tc.genre {
				t.Errorf("select_items.genre = %v, want [%s]", items["genre"], tc.genre)
			}
		})
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
