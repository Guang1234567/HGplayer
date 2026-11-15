package hgproto

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 剧集详情走官网服务端渲染页：完整分集 vid 列表内嵌在 _ROUTER_DATA 中，
// 不需要任何签名。App 端详情接口对匿名请求返回空响应，官网是唯一可用链路。

// routerDataPattern 匹配页面内嵌的 _ROUTER_DATA 赋值。
var routerDataPattern = regexp.MustCompile(`(?:window\.)?_ROUTER_DATA\s*=\s*\{`)

// routerPageKeys 是剧集详情所在的 loaderData 键（按优先级）。
var routerPageKeys = []string{"player_(series_id)/page", "player_(series_id)/(vid)/page", "detail_page"}

// fetchSeriesPage 拉取官网剧集页 HTML。
//
// player 页与 detail 页都包含同一份数据；某一台 CDN 边缘异常时另一条仍可用。
func (c *Client) fetchSeriesPage(ctx context.Context, seriesID string) ([]byte, error) {
	targets := []string{
		c.siteOrigin + "/player/" + seriesID,
		c.siteOrigin + "/detail?series_id=" + seriesID,
	}
	headers := map[string]string{
		"User-Agent": websiteUA,
		"Accept":     "text/html,application/xhtml+xml",
		"Referer":    c.siteOrigin + "/",
	}

	var lastErr error
	for _, target := range targets {
		body, err := c.doRawBody(ctx, target, headers)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// parseSeriesPage 从官网剧集页解析分集列表与元信息。
func parseSeriesPage(html []byte, seriesID string) (*SeriesDetail, error) {
	router, err := readRouterData(html)
	if err != nil {
		return nil, err
	}

	loader, ok := router["loaderData"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("官网页面缺少剧集数据")
	}
	var page map[string]any
	for _, key := range routerPageKeys {
		if candidate, ok := loader[key].(map[string]any); ok {
			page = candidate
			break
		}
	}
	if page == nil {
		return nil, fmt.Errorf("官网页面缺少剧集数据")
	}
	if success, ok := page["isSuccess"].(bool); ok && !success {
		return nil, fmt.Errorf("官网未能返回该剧集")
	}

	detail, ok := page["seriesDetail"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("官网页面缺少剧集数据")
	}
	// 身份必须与请求一致：页面里同时带着推荐剧集，取错就会串剧。
	if payloadIDString(detail["series_id"]) != seriesID {
		return nil, fmt.Errorf("官网返回的剧集与请求不一致")
	}
	if raw, ok := page["series_id"]; ok && raw != nil && payloadIDString(raw) != seriesID {
		return nil, fmt.Errorf("官网返回的剧集与请求不一致")
	}

	rows, ok := detail["vid_list"].([]any)
	if !ok {
		return nil, fmt.Errorf("官网页面未找到分集数据")
	}
	episodes := make([]EpisodeBrief, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, raw := range rows {
		vid := payloadIDString(raw)
		if !numericID.MatchString(vid) || seen[vid] {
			return nil, fmt.Errorf("官网分集数据异常")
		}
		seen[vid] = true
		episodes = append(episodes, EpisodeBrief{Vid: vid, EpisodeIndex: len(episodes) + 1})
	}
	if len(episodes) == 0 {
		return nil, fmt.Errorf("官网分集列表为空")
	}

	title := cleanTitle(trimString(detail["series_name"]))
	if title == "" {
		title = "未命名短剧"
	}
	return &SeriesDetail{
		SeriesID:    seriesID,
		SeriesTitle: title,
		Cover:       normalizeCover(firstString(detail["series_cover"], "")),
		Description: cleanTitle(trimString(detail["series_intro"])),
		Episodes:    episodes,
	}, nil
}

// readRouterData 从官网 HTML 中提取 _ROUTER_DATA JSON。
//
// 用花括号配平而不是正则整段匹配：该对象内含嵌套对象与字符串转义。
func readRouterData(html []byte) (map[string]any, error) {
	loc := routerDataPattern.FindIndex(html)
	if loc == nil {
		return nil, fmt.Errorf("官网页面未返回剧集数据")
	}
	start := loc[1] - 1 // 花括号起点

	depth := 0
	quoted := false
	escaped := false
	for i := start; i < len(html); i++ {
		ch := html[i]
		if quoted {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				quoted = false
			}
			continue
		}
		switch ch {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var data map[string]any
				if err := json.Unmarshal(html[start:i+1], &data); err != nil {
					return nil, fmt.Errorf("官网剧集数据解析失败")
				}
				return data, nil
			}
		}
	}
	return nil, fmt.Errorf("官网剧集数据不完整")
}

// payloadIDString 把响应中的 ID 字段转成字符串（对齐源实现的 String()）。
func payloadIDString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}
