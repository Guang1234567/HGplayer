package hgproto

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// 目录与搜索的分类常量。
const (
	categoryRealDrama = "real-drama"
	categoryComic     = "comic-drama"
	categoryAI        = "ai-drama"
	categoryHot       = "hot-drama"
)

// categoryAPIPath 是分类页统一入口。
//
// 上游没有按分类拆分路径：req_scene 在请求体内区分类别。
// 拼出 /landpage/comic/v/、/landpage/ai/v/ 之类路径会被上游直接 404。
const categoryAPIPath = "/reading/distribution/category/landpage/v/"

// browseList 获取目录分类页。
//
// 分类页为 POST + JSON body；榜单为 GET。两者都通过元数据签名头鉴权，
// 并依赖 branchOf 重试直到签名分支为 0。
//
// 返回与前端约定的卡片列表：series_id、series_title、cover、episode_count、tags。
func (c *Client) BrowseList(ctx context.Context, category string, genre string, page int) (*BrowseResult, error) {
	if page < 1 || page > 50 {
		return nil, inputErrf("页码需为 1 至 50 的整数")
	}

	var apiPath string
	var values []kv
	var payload []byte

	if rank, ok := rankings[category]; ok {
		// 榜单：GET，参数全部走 query。
		apiPath = "/reading/bookapi/bookmall/cell/change/v"
		values = []kv{
			{"tab_type", rankTabType(page)},
			{"client_req_type", "2"},
			{"client_template", "2"},
			{"background_selected_items", ""},
			{"category_id", "0"},
			{"limit", "20"},
			{"cell_sub_id", "0"},
			{"gender", "2"},
			{"cell_id", "7470092475068071998"},
			{"selected_items", rank[0]},
			{"sub_selected_items", rank[1]},
			{"panel_selected_items", ""},
			{"offset", "0"},
			{"session_id", ""},
			{"filter_ids", ""},
			{"unlimited_selector_change_type", "0"},
		}
	} else {
		if _, ok := categories[category]; !ok {
			return nil, inputErrf("分类无效: %s", category)
		}
		if genre != "" && !genrePattern.MatchString(genre) {
			return nil, inputErrf("题材标识无效")
		}
		apiPath = categoryAPIPath
		body, err := buildCatalogBody(category, genre, 0, "", page)
		if err != nil {
			return nil, err
		}
		payload = body
	}

	target, headers, body := c.metadataSigned(apiPath, values, payload)

	method := http.MethodPost
	if payload == nil {
		method = http.MethodGet
	}

	parsed, err := c.doJSONRaw(ctx, method, target, body, headers)
	if err != nil {
		return nil, err
	}

	results, hasMore, err := parseBrowsePayload(parsed)
	if err != nil {
		return nil, err
	}
	return &BrowseResult{
		Results: results,
		Page:    page,
		HasMore: hasMore,
	}, nil
}

// genrePattern 校验题材标识。
var genrePattern = regexp.MustCompile(`^cate_\d{1,8}$`)

// rankTabType 返回榜单分页的 tab_type。
func rankTabType(page int) string {
	if page == 1 {
		return "26"
	}
	return "18"
}

// BrowseResult 是目录浏览结果。
type BrowseResult struct {
	Results []SeriesCard `json:"results"`
	Page    int          `json:"page"`
	HasMore bool         `json:"hasMore"`
}

// SeriesCard 是目录中的短剧卡片。
type SeriesCard struct {
	SeriesID     string   `json:"series_id"`
	SeriesTitle  string   `json:"series_title"`
	Cover        string   `json:"cover"`
	EpisodeCount int      `json:"episode_count"`
	Description  string   `json:"description"`
	Tags         []string `json:"tags"`
}

var numericID = regexp.MustCompile(`^\d{1,30}$`)

// parseBrowsePayload 解析目录响应。
//
// 分类页的卡片在 data.video_data；榜单（cell/change）把卡片包在
// data.cell_view.cell_data[].video_data 里，两种形状都要支持。
func parseBrowsePayload(payload map[string]any) ([]SeriesCard, bool, error) {
	if code, ok := payload["code"].(float64); !ok || code != 0 {
		return nil, false, fmt.Errorf("目录服务暂不可用")
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("目录缺少列表数据")
	}

	rows, ok := data["video_data"].([]any)
	if !ok {
		if view, viewOK := data["cell_view"].(map[string]any); viewOK {
			rows, ok = rankingRows(view)
		}
	}
	if !ok {
		return nil, false, fmt.Errorf("分类结构异常")
	}

	seen := map[string]bool{}
	cards := make([]SeriesCard, 0, len(rows))
	for _, row := range rows {
		video, ok := row.(map[string]any)
		if !ok {
			continue
		}
		id := trimString(video["series_id"])
		title := cleanTitle(firstString(video["title"], video["series_title"], video["series_name"]))
		if !numericID.MatchString(id) || title == "" || seen[id] {
			continue
		}
		seen[id] = true

		cards = append(cards, SeriesCard{
			SeriesID:     id,
			SeriesTitle:  title,
			Cover:        normalizeCover(firstString(video["cover"], "")),
			EpisodeCount: toInt(video["episode_cnt"]),
			Description:  cleanTitle(firstString(video["video_desc"], video["series_intro"], "")),
			Tags:         extractTags(video["sub_title_list"]),
		})
	}

	hasMore := data["has_more"] == true
	if !hasMore {
		if view, viewOK := data["cell_view"].(map[string]any); viewOK {
			hasMore = view["has_more"] == true
		}
	}
	return cards, hasMore, nil
}

// rankingRows 展开榜单的嵌套卡片列表。
//
// cell_data[].video_data 可能是单对象或数组；其它 cell（演员榜等）没有该字段。
func rankingRows(view map[string]any) ([]any, bool) {
	cells, ok := view["cell_data"].([]any)
	if !ok {
		return nil, false
	}
	var rows []any
	for _, cell := range cells {
		m, ok := cell.(map[string]any)
		if !ok {
			continue
		}
		switch video := m["video_data"].(type) {
		case []any:
			rows = append(rows, video...)
		case map[string]any:
			rows = append(rows, video)
		}
	}
	return rows, true
}

// extractTags 提取标签列表（最多 4 个）。
func extractTags(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		label := cleanTitle(firstString(m["content"], ""))
		if label != "" {
			out = append(out, label)
		}
		if len(out) == 4 {
			break
		}
	}
	return out
}

// Search 按关键词搜索短剧。
func (c *Client) Search(ctx context.Context, keyword string, page int) (*SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, inputErrf("搜索关键词不能为空")
	}
	if len(keyword) > 80 {
		return nil, inputErrf("搜索关键词过长")
	}
	if page < 1 || page > 50 {
		return nil, inputErrf("页码需为 1 至 50 的整数")
	}

	// 官网搜索是主链路；失败（接口变更 / 网络异常）时回退到 App 端签名搜索。
	results, webErr := c.searchWebsite(ctx, keyword, page)
	if webErr == nil {
		return &SearchResult{Results: results, Page: page}, nil
	}

	results, appErr := c.searchApp(ctx, keyword, page)
	if appErr == nil {
		return &SearchResult{Results: results, Page: page}, nil
	}
	return nil, fmt.Errorf("搜索暂不可用：官网 %v；App %v", webErr, appErr)
}

// SearchResult 是搜索结果。
type SearchResult struct {
	Results []SeriesCard `json:"results"`
	Page    int          `json:"page"`
}

// parseSearchPayload 解析搜索响应。
func parseSearchPayload(payload map[string]any) ([]SeriesCard, error) {
	if code, ok := payload["code"].(float64); !ok || code != 0 {
		return nil, fmt.Errorf("搜索服务暂不可用")
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("搜索缺少结果数据")
	}

	rows, ok := data["book_list"].([]any)
	if !ok {
		rows, ok = data["data_list"].([]any)
	}
	if !ok {
		return nil, fmt.Errorf("搜索结构异常")
	}

	seen := map[string]bool{}
	cards := make([]SeriesCard, 0, len(rows))
	for _, row := range rows {
		book, ok := row.(map[string]any)
		if !ok {
			continue
		}
		id := trimString(firstString(book["series_id"], book["book_id"]))
		title := cleanTitle(firstString(book["title"], book["series_title"], book["series_name"], ""))
		if !numericID.MatchString(id) || title == "" || seen[id] {
			continue
		}
		seen[id] = true
		cards = append(cards, SeriesCard{
			SeriesID:     id,
			SeriesTitle:  title,
			Cover:        normalizeCover(firstString(book["cover"], "")),
			EpisodeCount: toInt(book["episode_cnt"]),
			Description:  cleanTitle(firstString(book["video_desc"], book["series_intro"], "")),
			Tags:         extractTags(book["sub_title_list"]),
		})
	}
	return cards, nil
}

// cleanTitle 清理标题空白。
func cleanTitle(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

var (
	coverHTTPPattern     = regexp.MustCompile(`(?i)^https?://`)
	coverHEICPathPattern = regexp.MustCompile(`(?i)\.heic$`)
	coverHostPattern     = regexp.MustCompile(`(?i)(^|\.)(fqnovelpic\.com|byteimg\.com)$`)
)

// normalizeCover 规范化封面地址。
//
// HEIC 在 Chromium 下无法解码：能转换的转为已验证的 JPEG 变体，否则置空，
// 避免前端出现破图（与源实现 catalogCover 一致）。查询串（x-signature 等）
// 不影响判定，因此按 pathname 判断后缀。
func normalizeCover(value string) string {
	s := strings.TrimSpace(value)
	if !coverHTTPPattern.MatchString(s) {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || !coverHEICPathPattern.MatchString(u.Path) {
		return s
	}
	return heicToJPEG(u)
}

// heicToJPEG 把 HEIC 封面转为已验证的 JPEG 变体；不可转换时返回空串。
func heicToJPEG(u *url.URL) string {
	if !coverHostPattern.MatchString(u.Hostname()) {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/novel-pic/") || !strings.Contains(u.Path, "~") {
		return ""
	}
	return "https://p3-novel.byteimg.com" + strings.SplitN(u.Path, "~", 2)[0] + "~tplv-shrink:640:0.image"
}

// 小工具。

func trimString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func firstString(values ...any) string {
	for _, v := range values {
		if s := trimString(v); s != "" {
			return s
		}
	}
	return ""
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0
		}
		return i
	}
	return 0
}
