package hgproto

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// 目录与搜索的分类常量。
const (
	categoryRealDrama = "real-drama"
	categoryComic     = "comic"
	categoryAI        = "ai-drama"
	categoryHot       = "hot-drama"
)

var validCategories = map[string]bool{
	categoryRealDrama: true, categoryComic: true, categoryAI: true, categoryHot: true,
}

// categoryAPIPath 返回分类页 API 路径。
func categoryAPIPath(category string) string {
	switch category {
	case categoryComic:
		return "/reading/distribution/category/landpage/comic/v/"
	case categoryAI:
		return "/reading/distribution/category/landpage/ai/v/"
	default:
		return "/reading/distribution/category/landpage/v/"
	}
}

// browseList 获取目录分类页。
//
// 分类页为 POST + JSON body；榜单为 GET。两者都通过元数据签名头鉴权，
// 并依赖 branchOf 重试直到签名分支为 0。
//
// 返回与前端约定的卡片列表：series_id、series_title、cover、episode_count、tags。
func (c *Client) BrowseList(ctx context.Context, category string, genre string, page int) (*BrowseResult, error) {
	if page < 1 || page > 50 {
		return nil, fmt.Errorf("页码需为 1 至 50 的整数")
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
		if !validCategories[category] {
			return nil, fmt.Errorf("分类无效: %s", category)
		}
		if genre != "" && !genrePattern.MatchString(genre) {
			return nil, fmt.Errorf("题材标识无效")
		}
		apiPath = categoryAPIPath(category)
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
			Tags:          extractTags(video["sub_title_list"]),
		})
	}

	hasMore := data["has_more"] == true
	return cards, hasMore, nil
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
		return nil, fmt.Errorf("搜索关键词不能为空")
	}
	if len(keyword) > 80 {
		return nil, fmt.Errorf("搜索关键词过长")
	}
	if page < 1 || page > 50 {
		return nil, fmt.Errorf("页码需为 1 至 50 的整数")
	}

	params := c.deviceParams(map[string]string{
		"query":      keyword,
		"count":      "20",
		"tab_type":   "11",
	})
	params["_rticket"] = strconv.FormatInt(timestamp()*1000, 10)

	target := c.searchAPI + "?" + buildQuery(params)

	payload, err := c.doJSON(ctx, "GET", target, nil, map[string]string{
		"User-Agent": searchUA,
		"Accept":     "application/json",
	})
	if err != nil {
		return nil, err
	}

	results, err := parseSearchPayload(payload)
	if err != nil {
		return nil, err
	}
	return &SearchResult{Results: results, Page: page}, nil
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
			EpisodeCount: toInt(firstInt(book["episode_cnt"])),
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

// normalizeCover 处理封面 URL。
func normalizeCover(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(s), ".heic") {
		// HEIC 封面转为兼容的 JPEG 变体。
		if u := heicToJPEG(s); u != "" {
			return u
		}
	}
	return s
}

// heicToJPEG 将受支持的 HEIC 封面转为 JPEG。
func heicToJPEG(value string) string {
	u, err := parseURL(value)
	if err != nil {
		return ""
	}
	if u.Hostname() != "fqnovelpic.com" && u.Hostname() != "byteimg.com" {
		return ""
	}
	if !strings.HasPrefix(u.path, "/novel-pic/") {
		return ""
	}
	idx := strings.Index(u.path, "~")
	if idx < 0 {
		return ""
	}
	return "https://p3-novel.byteimg.com" + u.path[:idx] + "~tplv-shrink:640:0.image"
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

func firstInt(v any) any { return v }

// parseURL 解析 URL，避免额外依赖。
func parseURL(s string) (*urlParser, error) {
	return newURLParser(s)
}

// urlParser 是最小 URL 解析器，仅支持 host/path 提取。
type urlParser struct {
	host string
	path string
}

func newURLParser(raw string) (*urlParser, error) {
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	p := &urlParser{}
	if j := strings.Index(rest, "/"); j >= 0 {
		p.host = rest[:j]
		p.path = rest[j:]
	} else {
		p.host = rest
	}
	if p.host == "" {
		return nil, fmt.Errorf("URL 无效")
	}
	return p, nil
}

func (u *urlParser) Hostname() string {
	return strings.Split(u.host, ":")[0]
}

func (u *urlParser) Path() string { return u.path }
