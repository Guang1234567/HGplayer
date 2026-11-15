package hgproto

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// loadWebsitePayload 读取官网搜索接口的真实抓包样本。
func loadWebsitePayload(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/website_search.json")
	if err != nil {
		t.Fatalf("读取抓包样本失败: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("解析抓包样本失败: %v", err)
	}
	return payload
}

// cardView 是卡片中与源实现对拍所需的字段。
type cardView struct {
	ID    string
	Title string
	Eps   int
	Tags  []string
}

func viewCards(cards []SeriesCard) []cardView {
	views := make([]cardView, 0, len(cards))
	for _, card := range cards {
		tags := card.Tags
		if tags == nil {
			tags = []string{}
		}
		views = append(views, cardView{ID: card.SeriesID, Title: card.SeriesTitle, Eps: card.EpisodeCount, Tags: tags})
	}
	return views
}

// TestParseWebsiteSearchMatchesReference 用真实抓包与源实现（Node）逐条对拍。
func TestParseWebsiteSearchMatchesReference(t *testing.T) {
	payload := loadWebsitePayload(t)

	got, err := parseWebsiteSearch(payload, "都市")
	if err != nil {
		t.Fatalf("parseWebsiteSearch: %v", err)
	}

	want := []cardView{
		{"7694948718188973080", "带娃摆摊，前夫找上门第一季", 475, []string{"恋爱", "都市"}},
		{"7688352674999176254", "总裁是我前女友", 95, []string{"恋爱", "都市", "职场"}},
		{"7688740767782669374", "一道划痕，满城风雨", 51, []string{"剧情", "都市", "豪门"}},
		{"7520576652108106814", "暗光", 59, []string{"爱情", "都市爱情", "强取豪夺", "揭露阴谋"}},
		{"7676856790335622168", "安安寻父，傅家归暖", 36, []string{"萌宝", "都市", "豪门"}},
		{"7690208125218982937", "老公破产，花瓶夫人的身份瞒不住了", 57, []string{"剧情", "都市", "豪门"}},
		{"7697622096326708286", "退役归来，这个保镖不好惹", 100, []string{"战神", "都市"}},
		{"7689135051455335449", "千金蒙尘，承安来娶", 46, []string{"剧情", "逆袭", "都市", "豪门"}},
		{"7693833945556732952", "相亲相到孩子亲爸", 72, []string{"恋爱", "都市", "豪门"}},
		{"7678614810933595198", "特警老公藏起亿万身家", 60, []string{"恋爱", "都市", "豪门"}},
	}
	if !reflect.DeepEqual(viewCards(got), want) {
		t.Fatalf("解析结果与源实现不一致:\n got = %+v\nwant = %+v", viewCards(got), want)
	}

	first := got[0]
	if !strings.HasPrefix(first.Cover, "https://p6-novel.byteimg.com/") {
		t.Fatalf("封面地址异常: %q", first.Cover)
	}
	if first.Description == "" {
		t.Fatal("简介不应为空")
	}
}

// TestParseWebsiteSearchFiltersNonMatches 上游会用热门剧目填充无结果搜索，必须按关键词过滤。
func TestParseWebsiteSearchFiltersNonMatches(t *testing.T) {
	payload := loadWebsitePayload(t)

	got, err := parseWebsiteSearch(payload, "完全不存在zzz")
	if err != nil {
		t.Fatalf("parseWebsiteSearch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("结果 = %d, want 0", len(got))
	}
}

// TestParseWebsiteSearchSingleMatch 单条命中。
func TestParseWebsiteSearchSingleMatch(t *testing.T) {
	payload := loadWebsitePayload(t)

	got, err := parseWebsiteSearch(payload, "总裁是我前女友")
	if err != nil {
		t.Fatalf("parseWebsiteSearch: %v", err)
	}
	if len(got) != 1 || got[0].SeriesID != "7688352674999176254" {
		t.Fatalf("结果 = %+v", got)
	}
}

func TestParseWebsiteSearchErrors(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"空响应", map[string]any{}},
		{"业务错误", map[string]any{"code": "rate_limited", "message": "请求过于频繁"}},
		{"缺少列表", map[string]any{"totalCount": 0}},
		{"条目无法识别", map[string]any{"searchList": []any{
			map[string]any{"video_data": map[string]any{"series_id": "not-a-number", "series_title": "无效"}},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseWebsiteSearch(tc.payload, "任意"); err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

// TestParseWebsiteSearchEpisodeFallback 缺少 episode_cnt 时从"共 N 集"文案提取。
func TestParseWebsiteSearchEpisodeFallback(t *testing.T) {
	payload := map[string]any{
		"searchList": []any{
			map[string]any{"video_data": map[string]any{
				"series_id": "123456", "series_title": "甲剧",
				"episode_right_text": "共 88 集", "series_intro": "甲",
				"series_cover": "ftp://example.com/a.jpg",
			}},
		},
	}
	got, err := parseWebsiteSearch(payload, "甲剧")
	if err != nil {
		t.Fatalf("parseWebsiteSearch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("结果 = %d, want 1", len(got))
	}
	if got[0].EpisodeCount != 88 {
		t.Fatalf("集数 = %d, want 88", got[0].EpisodeCount)
	}
	if got[0].Cover != "" {
		t.Fatalf("非 http(s) 封面应置空，got %q", got[0].Cover)
	}
	if got[0].Tags == nil {
		t.Fatal("tags 不应为 null")
	}
}

// TestWebsiteTagsCapped 标签最多 4 个且去重。
func TestWebsiteTagsCapped(t *testing.T) {
	video := map[string]any{"category_list": []any{
		map[string]any{"name": "A"}, map[string]any{"name": "B"}, map[string]any{"name": "A"},
		map[string]any{"name": "C"}, map[string]any{"name": "D"}, map[string]any{"name": "E"},
	}}
	got := websiteTags(video)
	if len(got) != 4 {
		t.Fatalf("标签数 = %d, want 4 (%v)", len(got), got)
	}
	if !reflect.DeepEqual(got, []string{"A", "B", "C", "D"}) {
		t.Fatalf("标签 = %v", got)
	}
}
