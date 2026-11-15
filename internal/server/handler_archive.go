package server

import (
	"encoding/json"
	"net/http"

	"hgplayer/internal/archive"
)

// progressRequest 是保存进度的请求体。
type progressRequest struct {
	SeriesID    string  `json:"series_id"`
	SeriesTitle string  `json:"series_title"`
	Cover       string  `json:"cover"`
	Episode     int     `json:"episode_index"`
	Position    float64 `json:"position_sec"`
	Duration    float64 `json:"duration_sec"`
}

// handleArchiveSaveProgress 保存播放进度。
func (s *Server) handleArchiveSaveProgress(w http.ResponseWriter, r *http.Request) {
	var req progressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "请求体无效"})
		return
	}

	err := s.archive.UpdateProgress(archive.Progress{
		SeriesID:    req.SeriesID,
		SeriesTitle: req.SeriesTitle,
		Cover:       req.Cover,
		Episode:     req.Episode,
		Position:    req.Position,
		Duration:    req.Duration,
		UpdatedAt:   nowUnix(),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// favoriteRequest 是收藏切换的请求体。
type favoriteRequest struct {
	SeriesID    string `json:"series_id"`
	SeriesTitle string `json:"series_title"`
	Cover       string `json:"cover"`
}

// handleArchiveToggleFavorite 切换收藏状态。
func (s *Server) handleArchiveToggleFavorite(w http.ResponseWriter, r *http.Request) {
	var req favoriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "请求体无效"})
		return
	}

	added, err := s.archive.ToggleFavorite(archive.Favorite{
		SeriesID:    req.SeriesID,
		SeriesTitle: req.SeriesTitle,
		Cover:       req.Cover,
		UpdatedAt:   nowUnix(),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorited": added})
}

// handleArchiveSearchHistory 返回搜索历史。
func (s *Server) handleArchiveSearchHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"history": s.archive.SearchHistory()})
}

// searchHistoryRequest 是搜索历史写操作的请求体。
type searchHistoryRequest struct {
	Action string `json:"action"`
	Value  string `json:"value"`
}

// handleArchiveUpdateSearchHistory 增删清搜索历史。
func (s *Server) handleArchiveUpdateSearchHistory(w http.ResponseWriter, r *http.Request) {
	var req searchHistoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "请求体无效"})
		return
	}
	if err := s.archive.UpdateSearchHistory(req.Action, req.Value); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": s.archive.SearchHistory()})
}
