package hgproto

import (
	"context"
)

// SeriesDetail 是剧集详情（含分集列表与本地进度/收藏）。
type SeriesDetail struct {
	SeriesID      string         `json:"series_id"`
	SeriesTitle   string         `json:"series_title"`
	Cover         string         `json:"cover"`
	Description   string         `json:"description"`
	Episodes      []EpisodeBrief `json:"episodes"`
	SavedProgress *SavedProgress `json:"saved_progress,omitempty"`
	IsFavorite    bool           `json:"is_favorite"`
}

// EpisodeBrief 是分集简要信息。
type EpisodeBrief struct {
	Vid          string `json:"vid"`
	Title        string `json:"title"`
	EpisodeIndex int    `json:"episode_index"`
	PositionSec  int    `json:"position_sec"`
	DurationSec  int    `json:"duration_sec"`
}

// SavedProgress 是本地记录的观看进度。
type SavedProgress struct {
	EpisodeIndex int `json:"episode_index"`
	PositionSec  int `json:"position_sec"`
	DurationSec  int `json:"duration_sec"`
}

// Series 查询剧集详情与分集列表。
//
// localProgress 可为 nil（未观看过）；isFav 表示是否已收藏。
func (c *Client) Series(ctx context.Context, seriesID string, localProgress *SavedProgress, isFav bool) (*SeriesDetail, error) {
	if !numericID.MatchString(seriesID) {
		return nil, inputErrf("剧目标识无效")
	}

	html, err := c.fetchSeriesPage(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	detail, err := parseSeriesPage(html, seriesID)
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
