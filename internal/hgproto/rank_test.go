package hgproto

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// rankSelectorPayload 构造分类选择器响应（形状与真实响应一致）。
func rankSelectorPayload() map[string]any {
	return map[string]any{
		"code": 0,
		"data": map[string]any{
			"cell_view": map[string]any{
				"cell_selector": map[string]any{
					"outer_row": map[string]any{
						"items": []any{
							map[string]any{
								"selector_item_id": "human",
								"show_name":        "真人剧",
								"sub_cell_selector": map[string]any{
									"outer_row": map[string]any{
										"items": []any{
											map[string]any{
												"selector_item_id": "human_hot_play",
												"show_name":        "热播榜",
												"sub_title":        "基于观看热度排序",
											},
											map[string]any{
												"selector_item_id": "human_followed",
												"show_name":        "收藏榜",
												"panel": map[string]any{
													"inner_rows": []any{
														map[string]any{
															"row_name": "题材",
															"items": []any{
																map[string]any{
																	"selector_item_id": "cate_130",
																	"show_name":        "古装",
																},
															},
														},
													},
												},
											},
										},
									},
								},
							},
							map[string]any{
								"selector_item_id": "ai_playlet",
								"show_name":        "AI剧",
							},
						},
					},
				},
			},
		},
	}
}

func TestRankPanelParsesCategories(t *testing.T) {
	categories, err := parseRankPanel(rankSelectorPayload())
	if err != nil {
		t.Fatalf("parseRankPanel: %v", err)
	}
	if len(categories) != 2 {
		t.Fatalf("categories = %d, want 2", len(categories))
	}
	if categories[0].ID != "human" || categories[0].Name != "真人剧" {
		t.Fatalf("category0 = %+v", categories[0])
	}
	if len(categories[0].Subs) != 2 {
		t.Fatalf("subs = %d, want 2", len(categories[0].Subs))
	}
	if got := categories[0].Subs[0]; got.ID != "human_hot_play" || got.Name != "热播榜" || got.Desc != "基于观看热度排序" {
		t.Fatalf("sub0 = %+v", got)
	}
	panel := categories[0].Subs[1].Panel
	if len(panel) != 1 || panel[0].Key != "题材" || len(panel[0].Options) != 1 {
		t.Fatalf("panel = %+v", panel)
	}
	if opt := panel[0].Options[0]; opt.ID != "cate_130" || opt.Name != "古装" {
		t.Fatalf("option = %+v", opt)
	}
	if categories[1].Subs == nil || len(categories[1].Subs) != 0 {
		t.Fatalf("无子榜单的分类应保留空列表: %+v", categories[1])
	}
}

func TestRankPanelRejectsMissingSelector(t *testing.T) {
	_, err := parseRankPanel(map[string]any{"code": 0, "data": map[string]any{"cell_view": map[string]any{}}})
	if err == nil {
		t.Fatal("缺少 cell_selector 应报错")
	}
}

func TestRankCursorRoundTrip(t *testing.T) {
	cur := rankCursor{Offset: 15, Session: "sess-1", Filter: []string{"1001", "1002"}}
	decoded := decodeRankCursor(cur.encode())
	if decoded.Offset != 15 || decoded.Session != "sess-1" || len(decoded.Filter) != 2 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if legacy := decodeRankCursor("30"); legacy.Offset != 30 {
		t.Fatalf("legacy offset = %d, want 30", legacy.Offset)
	}
	if empty := decodeRankCursor(""); empty.Offset != 0 || empty.Session != "" {
		t.Fatalf("empty cursor = %+v", empty)
	}
}

// rankListPayload 构造榜单页响应：两张剧卡 + 一个演员卡。
func rankListPayload() map[string]any {
	return map[string]any{
		"code": 0,
		"data": map[string]any{
			"has_more":    true,
			"next_offset": float64(15),
			"session_id":  "sess-2",
			"video_data":  []any{},
			"cell_view": map[string]any{
				"cell_data": []any{
					map[string]any{
						"video_data": []any{
							map[string]any{
								"series_id":         "7704532495357725721",
								"title":             "持械入宋：第十一季",
								"cover":             "https://p3-reading-sign.fqnovelpic.com/novel-pic/a~tplv-1-superreso-aifit:400:0.heic?x=1",
								"episode_cnt":       float64(113),
								"score":             "8.4",
								"sub_title":         "古装·全113集",
								"rec_text":          "10504万热度",
								"video_desc":        "来啦来啦",
								"tag_info":          map[string]any{"text": "新剧"},
								"sub_title_list":    []any{map[string]any{"content": "古装"}, map[string]any{"content": "全113集"}},
								"online_subscribed": true,
							},
						},
					},
					map[string]any{
						"video_data": map[string]any{
							"series_id":   "7705804887707110424",
							"title":       "反向拿捏第3季",
							"episode_cnt": float64(0),
							"score":       "",
							"rec_text":    "1580万期待",
						},
					},
					map[string]any{
						"ugc_user_data": []any{
							map[string]any{
								"user_info": map[string]any{
									"user_name":    "示例演员",
									"user_avatar":  "https://example.com/a.jpg",
									"actor_id_str": "9001",
								},
								"rec_text": "1234万人气",
								"intro":    "演员简介",
							},
						},
					},
				},
			},
		},
	}
}

func TestRankPageParsesItemsAndCursor(t *testing.T) {
	page, err := parseRankPage(rankListPayload(), rankCursor{})
	if err != nil {
		t.Fatalf("parseRankPage: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(page.Items))
	}

	item := page.Items[0]
	if item.ID != "7704532495357725721" || item.Title != "持械入宋：第十一季" {
		t.Fatalf("item0 = %+v", item)
	}
	if item.Score != 8.4 || item.Badge != "新剧" || item.RankText != "10504万热度" {
		t.Fatalf("item0 元数据 = %+v", item)
	}
	if item.Status != "古装·全113集" || item.EpisodeCount != 113 || item.Intro != "来啦来啦" {
		t.Fatalf("item0 详情 = %+v", item)
	}
	if item.Cover != "https://p3-novel.byteimg.com/novel-pic/a~tplv-shrink:640:0.image" {
		t.Fatalf("cover = %s", item.Cover)
	}
	if len(item.Tags) != 2 {
		t.Fatalf("tags = %v", item.Tags)
	}
	if item.Upcoming || item.Person {
		t.Fatalf("item0 不应是预约/演员: %+v", item)
	}

	pending := page.Items[1]
	if !pending.Upcoming || pending.Status != "" || pending.Score != 0 {
		t.Fatalf("无集数条目应为预约: %+v", pending)
	}

	actor := page.Items[2]
	if !actor.Person || actor.ID != "actor:9001" || actor.Title != "示例演员" || actor.RankText != "1234万人气" {
		t.Fatalf("actor = %+v", actor)
	}

	if !page.HasMore || page.Cursor == "" {
		t.Fatalf("hasMore=%v cursor=%q", page.HasMore, page.Cursor)
	}
	cur := decodeRankCursor(page.Cursor)
	if cur.Offset != 15 || cur.Session != "sess-2" {
		t.Fatalf("cursor = %+v", cur)
	}
	if len(cur.Filter) != 3 {
		t.Fatalf("filter = %v", cur.Filter)
	}
}

func TestRankPageSkipsSeenAndEndsCursor(t *testing.T) {
	cursor := rankCursor{Offset: 15, Session: "sess-2", Filter: []string{"7704532495357725721"}}

	payload := rankListPayload()
	data := payload["data"].(map[string]any)
	data["has_more"] = false

	page, err := parseRankPage(payload, cursor)
	if err != nil {
		t.Fatalf("parseRankPage: %v", err)
	}
	if page.Items[0].ID == "7704532495357725721" {
		t.Fatal("游标里已出现的剧集不应重复返回")
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(page.Items))
	}
	if page.HasMore || page.Cursor != "" {
		t.Fatalf("末页不应生成游标: hasMore=%v cursor=%q", page.HasMore, page.Cursor)
	}
}

// 请求参数：首页 tab_type=2，续页 tab_type=1 并携带 session_id/filter_ids。
func TestRankSendsCursorArgs(t *testing.T) {
	var queries []url.Values
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"has_more":true,"next_offset":15,"session_id":"s1","cell_view":{"cell_data":[{"video_data":[{"series_id":"1001","title":"示例"}]}]}}}`))
	}))

	selections := map[string]string{"rank": "human", "rank_sub": "human_hot_play", "rank_panel": "cate_130"}
	if _, err := c.Rank(context.Background(), selections, ""); err != nil {
		t.Fatalf("Rank 第一页: %v", err)
	}
	first := queries[0]
	if got := first.Get("tab_type"); got != rankFirstPageTab {
		t.Errorf("tab_type = %s, want %s", got, rankFirstPageTab)
	}
	if first.Get("selected_items") != "human" || first.Get("sub_selected_items") != "human_hot_play" || first.Get("panel_selected_items") != "cate_130" {
		t.Errorf("选择参数 = %v", first)
	}
	if first.Get("offset") != "0" || first.Get("session_id") != "" || first.Get("filter_ids") != "" {
		t.Errorf("首页不应携带游标: %v", first)
	}
	if got := first.Get("cell_id"); got != rankCellID {
		t.Errorf("cell_id = %s", got)
	}

	cursor := rankCursor{Offset: 15, Session: "s1", Filter: []string{"1001"}}.encode()
	if _, err := c.Rank(context.Background(), selections, cursor); err != nil {
		t.Fatalf("Rank 第二页: %v", err)
	}
	next := queries[1]
	if got := next.Get("tab_type"); got != rankNextPageTab {
		t.Errorf("续页 tab_type = %s, want %s", got, rankNextPageTab)
	}
	if next.Get("offset") != "15" || next.Get("session_id") != "s1" || next.Get("filter_ids") != "1001" {
		t.Errorf("续页游标参数 = %v", next)
	}
}

func TestRankPanelSendsSelectorTab(t *testing.T) {
	var query url.Values
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(rankSelectorPayload())
		_, _ = w.Write(body)
	}))

	if _, err := c.RankPanel(context.Background()); err != nil {
		t.Fatalf("RankPanel: %v", err)
	}
	if query.Get("tab_type") != rankPanelTab || query.Get("selected_items") != "" {
		t.Fatalf("选择器参数 = %v", query)
	}
}
