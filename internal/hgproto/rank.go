package hgproto

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// 排行榜协议。
//
// 与源实现一致：榜单与分类选择器共用 /reading/bookapi/bookmall/cell/change/v，
// 区别只在 tab_type（选择器 26，首页 2，后续页 1）与 selected_items 等参数。
// 分页游标由本客户端编码：JSON{offset, session, filter}，filter 保存已出现的
// 剧集 ID，回传 filter_ids 供上游去重。
const (
	rankChangePath = "/reading/bookapi/bookmall/cell/change/v"
	// rankCellID 是短剧排行榜所在的 bookmall cell。
	rankCellID = "7470092475068071998"
	// rankPanelTab 拉取分类选择器的 tab_type。
	rankPanelTab = "26"
	// rankFirstPageTab / rankNextPageTab 是榜单第一页 / 后续页的 tab_type。
	rankFirstPageTab = "2"
	rankNextPageTab  = "1"
	// rankFilterLimit 是游标里保留的去重 ID 上限。
	rankFilterLimit = 200
)

// rankCursor 是榜单分页游标。
type rankCursor struct {
	Offset  int      `json:"offset"`
	Session string   `json:"session,omitempty"`
	Filter  []string `json:"filter,omitempty"`
}

// decodeRankCursor 解析游标；兼容纯数字（只有 offset）的旧格式。
func decodeRankCursor(value string) rankCursor {
	var cur rankCursor
	if strings.TrimSpace(value) == "" {
		return cur
	}
	if err := json.Unmarshal([]byte(value), &cur); err == nil {
		return cur
	}
	if offset, err := strconv.Atoi(value); err == nil {
		cur.Offset = offset
	}
	return cur
}

// encode 序列化为前端持有的不透明游标。
func (c rankCursor) encode() string {
	raw, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(raw)
}

// RankCategory 是榜单分类（全部 / 系列剧 / 漫剧 / 真人剧 / AI剧）。
type RankCategory struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	Subs []RankSub `json:"subs"`
}

// RankSub 是分类下的榜单（推荐榜 / 热播榜 / 新剧榜 …）。
type RankSub struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Desc  string            `json:"desc"`
	Panel []RankFilterGroup `json:"panel"`
}

// RankFilterGroup 是榜单的筛选项分组。
type RankFilterGroup struct {
	Key     string             `json:"key"`
	Name    string             `json:"name"`
	Options []RankFilterOption `json:"options"`
}

// RankFilterOption 是筛选项。
type RankFilterOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RankItem 是榜单条目。
type RankItem struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Cover        string   `json:"cover"`
	EpisodeCount int      `json:"episode_count"`
	Score        float64  `json:"score"`
	Status       string   `json:"status"`
	Badge        string   `json:"badge,omitempty"`
	RankText     string   `json:"rank_text,omitempty"`
	Tags         []string `json:"tags"`
	Cast         []string `json:"cast,omitempty"`
	Intro        string   `json:"intro,omitempty"`
	SubInfo      string   `json:"sub_info,omitempty"`
	Person       bool     `json:"person,omitempty"`
	Upcoming     bool     `json:"upcoming,omitempty"`
	Reserved     bool     `json:"reserved,omitempty"`
}

// RankPage 是榜单的一页。
type RankPage struct {
	Items   []RankItem `json:"items"`
	Cursor  string     `json:"cursor"`
	HasMore bool       `json:"has_more"`
}

// rankArgs 构造 cell/change 查询参数（顺序与源实现一致，签名对顺序敏感）。
func (c *Client) rankArgs(tab, unlimited string, selections map[string]string, cur rankCursor) []kv {
	values := []kv{
		{"tab_type", tab},
		{"client_req_type", "1"},
		{"client_template", "1"},
		{"background_selected_items", ""},
		{"category_id", "0"},
		{"limit", "0"},
		{"cell_sub_id", "0"},
		{"gender", "2"},
		{"cell_id", rankCellID},
		{"selected_items", strings.TrimSpace(selections["rank"])},
		{"sub_selected_items", strings.TrimSpace(selections["rank_sub"])},
		{"panel_selected_items", strings.TrimSpace(selections["rank_panel"])},
		{"unlimited_selector_change_type", unlimited},
		{"offset", strconv.Itoa(cur.Offset)},
	}
	if cur.Session != "" {
		values = append(values, kv{"session_id", cur.Session})
	}
	if len(cur.Filter) > 0 {
		values = append(values, kv{"filter_ids", strings.Join(cur.Filter, ",")})
	}
	return values
}

// RankPanel 拉取榜单分类与子榜单。
func (c *Client) RankPanel(ctx context.Context) ([]RankCategory, error) {
	values := c.rankArgs(rankPanelTab, "0", nil, rankCursor{})
	target, headers, body := c.metadataSigned(rankChangePath, values, nil)
	parsed, err := c.doJSONRaw(ctx, http.MethodGet, target, body, headers)
	if err != nil {
		return nil, err
	}
	return parseRankPanel(parsed)
}

// Rank 拉取榜单条目。
//
// selections 为 rank / rank_sub / rank_panel 三个选择值；cursor 为空串表示第一页。
func (c *Client) Rank(ctx context.Context, selections map[string]string, cursor string) (*RankPage, error) {
	cur := decodeRankCursor(cursor)
	tab := rankFirstPageTab
	if cur.Offset > 0 {
		tab = rankNextPageTab
	}
	target, headers, body := c.metadataSigned(rankChangePath, c.rankArgs(tab, tab, selections, cur), nil)
	parsed, err := c.doJSONRaw(ctx, http.MethodGet, target, body, headers)
	if err != nil {
		return nil, err
	}
	return parseRankPage(parsed, cur)
}

// parseRankPanel 解析分类选择器。
//
// 选择器位于 data.cell_view.cell_selector.outer_row.items：
// 外层是分类，sub_cell_selector 内层是子榜单，panel.inner_rows 是筛选项。
func parseRankPanel(payload map[string]any) ([]RankCategory, error) {
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("榜单数据缺失")
	}
	view, ok := data["cell_view"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("榜单结构异常")
	}
	selector, ok := view["cell_selector"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("榜单缺少分类数据")
	}
	items := nestedList(selector, "outer_row", "items")

	categories := make([]RankCategory, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := trimString(item["selector_item_id"])
		name := cleanTitle(trimString(item["show_name"]))
		if id == "" || name == "" {
			continue
		}
		category := RankCategory{ID: id, Name: name, Subs: make([]RankSub, 0, 4)}
		for _, subRaw := range nestedList(item, "sub_cell_selector", "outer_row", "items") {
			sub, ok := subRaw.(map[string]any)
			if !ok {
				continue
			}
			subID := trimString(sub["selector_item_id"])
			subName := cleanTitle(trimString(sub["show_name"]))
			if subID == "" || subName == "" {
				continue
			}
			category.Subs = append(category.Subs, RankSub{
				ID:    subID,
				Name:  subName,
				Desc:  cleanTitle(trimString(sub["sub_title"])),
				Panel: parseRankPanelGroups(sub),
			})
		}
		categories = append(categories, category)
	}
	if len(categories) == 0 {
		return nil, fmt.Errorf("榜单分类为空")
	}
	return categories, nil
}

// parseRankPanelGroups 解析筛选项分组（panel.inner_rows）。
func parseRankPanelGroups(item map[string]any) []RankFilterGroup {
	rows := nestedList(item, "panel", "inner_rows")
	if len(rows) == 0 {
		return nil
	}
	groups := make([]RankFilterGroup, 0, len(rows))
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key := trimString(row["row_name"])
		if key == "" {
			continue
		}
		group := RankFilterGroup{Key: key, Name: key, Options: make([]RankFilterOption, 0, 4)}
		for _, optRaw := range nestedList(row, "items") {
			opt, ok := optRaw.(map[string]any)
			if !ok {
				continue
			}
			optID := trimString(opt["selector_item_id"])
			optName := cleanTitle(trimString(opt["show_name"]))
			if optID == "" || optName == "" {
				continue
			}
			group.Options = append(group.Options, RankFilterOption{ID: optID, Name: optName})
		}
		if len(group.Options) > 0 {
			groups = append(groups, group)
		}
	}
	return groups
}

// parseRankPage 解析榜单页并生成下一页游标。
func parseRankPage(payload map[string]any, cur rankCursor) (*RankPage, error) {
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("榜单数据缺失")
	}
	view, ok := data["cell_view"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("榜单结构异常")
	}

	seen := make(map[string]bool, len(cur.Filter)+16)
	for _, id := range cur.Filter {
		seen[id] = true
	}

	items := make([]RankItem, 0, 16)
	ids := make([]string, 0, 16)
	add := func(item RankItem) {
		if item.ID == "" || item.Title == "" || seen[item.ID] {
			return
		}
		seen[item.ID] = true
		items = append(items, item)
		ids = append(ids, item.ID)
	}

	for _, rawCell := range nestedList(view, "cell_data") {
		cell, ok := rawCell.(map[string]any)
		if !ok {
			continue
		}
		for _, rawVideo := range videoRows(cell["video_data"]) {
			if video, ok := rawVideo.(map[string]any); ok {
				add(parseRankVideo(video))
			}
		}
		for _, rawActor := range nestedList(cell, "ugc_user_data") {
			if actor, ok := rawActor.(map[string]any); ok {
				add(parseRankActor(actor))
			}
		}
	}

	hasMore := data["has_more"] == true || view["has_more"] == true
	next := toInt(data["next_offset"])
	if next == 0 {
		next = toInt(view["next_offset"])
	}
	session := firstString(data["session_id"], view["session_id"])

	page := &RankPage{Items: items, HasMore: false}
	if hasMore && len(items) > 0 {
		if next <= cur.Offset {
			next = cur.Offset + 10
		}
		filter := append(append([]string{}, cur.Filter...), ids...)
		if len(filter) > rankFilterLimit {
			filter = filter[len(filter)-rankFilterLimit:]
		}
		page.Cursor = rankCursor{Offset: next, Session: session, Filter: filter}.encode()
		page.HasMore = true
	}
	return page, nil
}

// videoRows 把 video_data 的单对象 / 数组统一为列表。
func videoRows(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case map[string]any:
		return []any{v}
	default:
		return nil
	}
}

// parseRankVideo 把榜单项映射为统一结构。
func parseRankVideo(video map[string]any) RankItem {
	id := trimString(video["series_id"])
	episodeCount := toInt(video["episode_cnt"])
	item := RankItem{
		ID:           id,
		Title:        cleanTitle(firstString(video["title"], video["series_title"])),
		Cover:        normalizeCover(firstString(video["cover"], "")),
		EpisodeCount: episodeCount,
		Score:        toFloat(video["score"]),
		Status:       cleanTitle(trimString(video["sub_title"])),
		Badge:        cleanTitle(nestedString(video, "tag_info", "text")),
		RankText:     cleanTitle(trimString(video["rec_text"])),
		Tags:         extractTags(video["sub_title_list"]),
		Intro:        cleanTitle(firstString(video["video_desc"], video["series_intro"], "")),
	}
	if item.Status == "" && episodeCount > 0 {
		item.Status = fmt.Sprintf("全%d集", episodeCount)
	}
	if len(item.Tags) == 0 {
		if category := cleanTitle(trimString(video["category"])); category != "" {
			item.Tags = []string{category}
		}
	}
	// 未开播条目没有集数：前端据此显示预约按钮，而不是播放。
	if episodeCount == 0 {
		item.Upcoming = true
		item.Reserved = video["online_subscribed"] == true
	}
	return item
}

// parseRankActor 把演员榜条目映射为统一结构（ID 加 actor: 前缀）。
func parseRankActor(actor map[string]any) RankItem {
	user, _ := actor["user_info"].(map[string]any)
	id := firstString(user["actor_id_str"], user["user_id"])
	name := cleanTitle(firstString(user["user_name"], user["nickname"], ""))
	if id == "" || name == "" {
		return RankItem{}
	}
	return RankItem{
		ID:       "actor:" + id,
		Title:    name,
		Cover:    normalizeCover(firstString(user["user_avatar"], "")),
		RankText: cleanTitle(trimString(actor["rec_text"])),
		Tags:     extractTags(actor["sub_title_list"]),
		Intro:    cleanTitle(firstString(actor["intro"], "")),
		Person:   true,
	}
}

// nestedList 逐层取 map 字段并转为列表。
func nestedList(root map[string]any, path ...string) []any {
	var current any = root
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[key]
	}
	list, _ := current.([]any)
	return list
}

// nestedString 逐层取 map 字段并转为字符串。
func nestedString(root map[string]any, path ...string) string {
	var current any = root
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[key]
	}
	return trimString(current)
}

// toFloat 解析字符串或数字形式的评分。
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err == nil {
			return f
		}
	}
	return 0
}
