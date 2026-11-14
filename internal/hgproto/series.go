package hgproto

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SeriesDetail 是剧集详情（含分集列表与本地进度/收藏）。
type SeriesDetail struct {
	SeriesID    string          `json:"series_id"`
	SeriesTitle string          `json:"series_title"`
	Cover       string          `json:"cover"`
	Description string          `json:"description"`
	Episodes    []EpisodeBrief  `json:"episodes"`
	SavedProgress *SavedProgress `json:"saved_progress,omitempty"`
	IsFavorite  bool            `json:"is_favorite"`
}

// EpisodeBrief 是分集简要信息。
type EpisodeBrief struct {
	Vid           string `json:"vid"`
	Title         string `json:"title"`
	EpisodeIndex  int    `json:"episode_index"`
	PositionSec   int    `json:"position_sec"`
	DurationSec   int    `json:"duration_sec"`
}

// SavedProgress 是本地记录的观看进度。
type SavedProgress struct {
	EpisodeIndex int     `json:"episode_index"`
	PositionSec  int     `json:"position_sec"`
	DurationSec  int     `json:"duration_sec"`
}

// Series 查询剧集详情与分集列表。
//
// localProgress 可为 nil（未观看过）；isFav 表示是否已收藏。
func (c *Client) Series(ctx context.Context, seriesID string, localProgress *SavedProgress, isFav bool) (*SeriesDetail, error) {
	if !numericID.MatchString(seriesID) {
		return nil, fmt.Errorf("剧目标识无效")
	}

	params := c.deviceParams(map[string]string{
		"series_id": seriesID,
	})
	params["_rticket"] = strconv.FormatInt(timestamp()*1000, 10)

	target := c.seriesAPI + "?" + buildQuery(params)

	payload, err := c.doJSON(ctx, "GET", target, nil, map[string]string{
		"User-Agent": playbackUA,
		"Accept":     "application/json",
	})
	if err != nil {
		return nil, err
	}

	detail, err := parseSeriesPayload(seriesID, payload)
	if err != nil {
		return nil, err
	}

	// 注入本地进度与收藏标记。
	detail.SavedProgress = localProgress
	detail.IsFavorite = isFav

	// 将本地进度对齐到对应分集。
	if localProgress != nil {
		for i := range detail.Episodes {
			if detail.Episodes[i].EpisodeIndex == localProgress.EpisodeIndex {
				detail.Episodes[i].PositionSec = localProgress.PositionSec
				detail.Episodes[i].DurationSec = localProgress.DurationSec
				break
			}
		}
	}
	return detail, nil
}

// parseSeriesPayload 解析剧集详情响应。
func parseSeriesPayload(seriesID string, payload map[string]any) (*SeriesDetail, error) {
	if code, ok := payload["code"].(float64); !ok || code != 0 {
		return nil, fmt.Errorf("剧集服务暂不可用")
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("剧集缺少数据")
	}

	detail := &SeriesDetail{
		SeriesID:    seriesID,
		SeriesTitle: cleanTitle(firstString(data["title"], data["series_title"], data["series_name"], "")),
		Cover:       normalizeCover(firstString(data["cover"], "")),
		Description: cleanTitle(firstString(data["video_desc"], data["series_intro"], data["description"], "")),
	}

	episodes, ok := data["episode_list"].([]any)
	if !ok {
		episodes, ok = data["episodes"].([]any)
	}
	if ok {
		for idx, row := range episodes {
			ep, ok := row.(map[string]any)
			if !ok {
				continue
			}
			vid := trimString(firstString(ep["vid"], ep["video_id"], ""))
			if vid == "" {
				continue
			}
			detail.Episodes = append(detail.Episodes, EpisodeBrief{
				Vid:          vid,
				Title:        cleanTitle(firstString(ep["title"], ep["name"], "")),
				EpisodeIndex: idx + 1,
			})
		}
	}

	return detail, nil
}

// PlayInfo 是单集播放信息（含解密所需参数）。
type PlayInfo struct {
	Vid           string  `json:"vid"`
	URL           string  `json:"url"`
	BackupURL     string  `json:"backup_url,omitempty"`
	Codec         string  `json:"codec"`
	Encrypted     bool    `json:"encrypted"`
	ContentKey    string  `json:"content_key,omitempty"`
	VideoMeta     *VideoMeta `json:"video_meta,omitempty"`
	Definition    string  `json:"definition,omitempty"`
}

// VideoMeta 是视频元信息。
type VideoMeta struct {
	Definition string `json:"definition"`
	VWidth     int    `json:"vwidth"`
	VHeight    int    `json:"vheight"`
	Bitrate    int    `json:"bitrate"`
	CodecType  string `json:"codec_type"`
}

// Play 获取单集播放地址与解密参数。
func (c *Client) Play(ctx context.Context, vid string) (*PlayInfo, error) {
	if vid == "" || len(vid) > 64 {
		return nil, fmt.Errorf("分集标识无效")
	}

	params := c.deviceParams(map[string]string{
		"vid": vid,
	})
	params["_rticket"] = strconv.FormatInt(timestamp()*1000, 10)

	target := c.videoAPI + "?" + buildQuery(params)

	payload, err := c.doJSON(ctx, "GET", target, nil, map[string]string{
		"User-Agent": videoUA,
		"Accept":     "application/json",
	})
	if err != nil {
		return nil, err
	}

	info, err := parsePlayPayload(vid, payload)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// parsePlayPayload 解析播放地址响应。
func parsePlayPayload(vid string, payload map[string]any) (*PlayInfo, error) {
	if code, ok := payload["code"].(float64); !ok || code != 0 {
		return nil, fmt.Errorf("取址服务暂不可用")
	}

	data, ok := payload["data"].(map[string]any)
	if !ok {
		data = payload
	}

	modelRaw := data["video_model"]
	if modelRaw == nil {
		return nil, fmt.Errorf("未返回片源地址")
	}

	var model map[string]any
	switch m := modelRaw.(type) {
	case map[string]any:
		model = m
	case string:
		if err := json.Unmarshal([]byte(m), &model); err != nil {
			return nil, fmt.Errorf("video_model 解析失败")
		}
	default:
		return nil, fmt.Errorf("video_model 类型异常")
	}

	info := &PlayInfo{Vid: vid}
	info.URL = firstString(model["main_url"], model["url"])
	info.BackupURL = firstString(model["backup_url"], "")

	meta, ok := model["video_meta"].(map[string]any)
	if ok {
		info.VideoMeta = &VideoMeta{
			Definition: firstString(meta["definition"], ""),
			VWidth:     toInt(meta["vwidth"]),
			VHeight:    toInt(meta["vheight"]),
			Bitrate:    toInt(meta["bitrate"]),
			CodecType:  firstString(meta["codec_type"], ""),
		}
		info.Definition = info.VideoMeta.Definition
		info.Codec = info.VideoMeta.CodecType
	}

	enc, ok := model["encrypt_info"].(map[string]any)
	if ok {
		info.Encrypted = enc["encrypt"] == true
		info.ContentKey = firstString(enc["spade_a"], enc["content_key"], "")
	}

	if info.URL == "" {
		return nil, fmt.Errorf("未返回片源地址")
	}
	return info, nil
}

// 编译期检查。
var _ = strings.TrimSpace
