package hgproto

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// 红果官网搜索接口。
//
// 搜索页本身是前端空壳，结果由页面脚本请求 /api/search/result 后渲染；
// 该接口不需要签名，直接调用比隐藏窗口嗅探更快，也不会因页面加载失败而超时。
const (
	websiteOrigin    = "https://hongguoduanju.com"
	websiteSearchAPI = websiteOrigin + "/api/search/result"
	websiteUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

var (
	// websiteSeriesID 是官网结果里剧集 ID 的形态。
	websiteSeriesID = regexp.MustCompile(`^\d{6,}$`)
	// websiteEpisodePattern 从"共 80 集"之类的文案中提取集数。
	websiteEpisodePattern = regexp.MustCompile(`(\d+)\s*集`)
	// websiteHTTPURL 校验封面是否为绝对 http(s) 地址。
	websiteHTTPURL = regexp.MustCompile(`(?i)^https?://`)

	websiteSlashUEscape = regexp.MustCompile(`(?i)\\u002F`)
	websiteSlashEscape  = regexp.MustCompile(`\\/`)
	websiteAmpUEscape   = regexp.MustCompile(`(?i)\\u0026`)
	websiteAmpEscape    = regexp.MustCompile(`&amp;`)
)

// searchWebsite 通过官网搜索接口查询短剧。
func (c *Client) searchWebsite(ctx context.Context, keyword string, page int) ([]SeriesCard, error) {
	target := c.websiteAPI + "?aid=8662&query=" + pythonEncode(keyword) + "&page=" + strconv.Itoa(page)

	payload, err := c.doJSONRaw(ctx, http.MethodGet, target, nil, map[string]string{
		"User-Agent":      websiteUA,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9",
		"Referer":         websiteOrigin + "/",
	})
	if err != nil {
		return nil, err
	}
	return parseWebsiteSearch(payload, keyword)
}

// parseWebsiteSearch 解析官网搜索响应。
func parseWebsiteSearch(payload map[string]any, keyword string) ([]SeriesCard, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("搜索响应为空")
	}
	if _, ok := payload["code"].(string); ok {
		return nil, fmt.Errorf("搜索服务暂不可用")
	}
	rows, ok := payload["searchList"].([]any)
	if !ok {
		return nil, fmt.Errorf("搜索返回异常数据")
	}

	results := make([]SeriesCard, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	recognized := 0
	for _, raw := range rows {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		video := item
		if detail, ok := item["video_data"].(map[string]any); ok && len(detail) > 0 {
			video = detail
		}
		// 有些结果把剧集编号放在 item.keyword（video_data 为空），不能因此当成解析失败。
		id := firstString(video["series_id"], item["series_id"], item["keyword"])
		title := websiteText(firstString(video["series_name"], video["series_title"], item["name"], video["title"]))
		if !websiteSeriesID.MatchString(id) || title == "" || seen[id] {
			continue
		}
		seen[id] = true
		recognized++
		if !matchesSearchKeyword(keyword, title, item["name"], video["series_intro"],
			video["tags"], video["category_list"], video["celebrities"]) {
			continue
		}
		results = append(results, SeriesCard{
			SeriesID:     id,
			SeriesTitle:  title,
			Cover:        websiteCover(firstString(video["series_cover"], video["cover"])),
			EpisodeCount: websiteEpisodeCount(video),
			Description:  websiteText(firstString(video["series_intro"])),
			Tags:         websiteTags(video),
		})
	}
	// 有数据但一条都认不出，通常是字段改名；这是解析失败，不是"没有结果"。
	if recognized == 0 && len(rows) > 0 {
		return nil, fmt.Errorf("搜索返回的条目无法识别")
	}
	return results, nil
}

// websiteText 还原实体并去除首尾空白。
func websiteText(value string) string {
	s := strings.ReplaceAll(value, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&#039;", "'")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return strings.TrimSpace(s)
}

// websiteCover 规范化封面地址，非 http(s) 一律置空。
func websiteCover(value string) string {
	cleaned := websiteCleanURL(value)
	if !websiteHTTPURL.MatchString(cleaned) {
		return ""
	}
	return cleaned
}

// websiteCleanURL 还原 JSON 转义过的地址分隔符。
func websiteCleanURL(value string) string {
	s := websiteSlashUEscape.ReplaceAllString(value, "/")
	s = websiteSlashEscape.ReplaceAllString(s, "/")
	s = websiteAmpUEscape.ReplaceAllString(s, "&")
	return websiteAmpEscape.ReplaceAllString(s, "&")
}

// websiteEpisodeCount 取集数：优先 episode_cnt，其次从"共 N 集"文案提取。
func websiteEpisodeCount(video map[string]any) int {
	switch n := video["episode_cnt"].(type) {
	case float64:
		if n > 0 && n == math.Trunc(n) {
			return int(n)
		}
	case string:
		if v, err := strconv.Atoi(strings.TrimSpace(n)); err == nil && v > 0 {
			return v
		}
	}
	if m := websiteEpisodePattern.FindStringSubmatch(firstString(video["episode_right_text"])); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			return v
		}
	}
	return 0
}

// websiteTags 取标签，最多 4 个。
func websiteTags(video map[string]any) []string {
	list, ok := video["tags"].([]any)
	if !ok {
		list, ok = video["category_list"].([]any)
		if !ok {
			return []string{}
		}
	}

	tags := make([]string, 0, 4)
	for _, raw := range list {
		label := ""
		switch item := raw.(type) {
		case string:
			label = item
		case map[string]any:
			label = firstString(item["name"], item["tag_name"], item["title"])
		}
		if text := strings.TrimSpace(label); text != "" && !slices.Contains(tags, text) {
			tags = append(tags, text)
		}
		if len(tags) >= 4 {
			break
		}
	}
	return tags
}
