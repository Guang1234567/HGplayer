package server

import (
	"net/http"
	"strconv"

	"hgplayer/internal/hgproto"
)

// handleCatalog 返回剧场目录（分类 / 题材 / 分页）。
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.protoReady(w) {
		return
	}
	q := r.URL.Query()
	category := q.Get("category")
	if category == "" {
		category = "real-drama"
	}
	page := atoiDefault(q.Get("page"), 1)

	res, err := s.proto.BrowseList(r.Context(), category, q.Get("genre"), page)
	if err != nil {
		s.writeProtoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleSearch 关键词搜索。
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !s.protoReady(w) {
		return
	}
	q := r.URL.Query()
	keyword := q.Get("q")
	page := atoiDefault(q.Get("page"), 1)

	res, err := s.proto.Search(r.Context(), keyword, page)
	if err != nil {
		s.writeProtoError(w, err)
		return
	}
	// 记录最近搜索（失败不影响结果）。
	if keyword != "" {
		_ = s.archive.UpdateSearchHistory("add", keyword)
	}
	writeJSON(w, http.StatusOK, res)
}

// handleSeries 返回剧集详情，并注入本地进度与收藏状态。
func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	if !s.protoReady(w) {
		return
	}
	id := r.PathValue("id")

	var progress *hgproto.SavedProgress
	if p, ok := s.archive.Progress(id); ok {
		progress = &hgproto.SavedProgress{
			EpisodeIndex: p.Episode,
			PositionSec:  int(p.Position),
			DurationSec:  int(p.Duration),
		}
	}

	detail, err := s.proto.Series(r.Context(), id, progress, s.archive.IsFavorite(id))
	if err != nil {
		s.writeProtoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handlePlay 返回单集播放地址与解密参数。
func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	if !s.protoReady(w) {
		return
	}
	info, err := s.proto.Play(r.Context(), r.PathValue("vid"))
	if err != nil {
		s.writeProtoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// protoReady 检查协议客户端是否可用。
func (s *Server) protoReady(w http.ResponseWriter) bool {
	if s.proto == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{
			Code:  "PROTO_UNAVAILABLE",
			Error: "协议客户端未就绪。",
		})
		return false
	}
	return true
}

// writeProtoError 把协议层错误映射为 HTTP 响应。
func (s *Server) writeProtoError(w http.ResponseWriter, err error) {
	s.logger.Warn("协议请求失败", "err", err.Error())
	status := http.StatusBadGateway
	// 输入类错误返回 400，便于前端区分「参数问题」与「上游问题」。
	if hgproto.IsInputError(err) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, errorResponse{Code: "PROTO_ERROR", Error: err.Error()})
}

// atoiDefault 解析整数，失败时返回默认值。
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
