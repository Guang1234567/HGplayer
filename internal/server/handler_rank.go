package server

import (
	"net/http"
)

// 榜单选择值 / 游标的长度上限，避免把异常长参数送进签名链路。
const (
	maxRankSelectionLen = 64
	maxRankCursorLen    = 4096
)

// handleRankPanel 返回榜单分类与子榜单。
func (s *Server) handleRankPanel(w http.ResponseWriter, r *http.Request) {
	if !s.protoReady(w) {
		return
	}
	categories, err := s.proto.RankPanel(r.Context())
	if err != nil {
		s.writeProtoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

// handleRank 返回榜单条目（rank / rank_sub / rank_panel + 游标）。
func (s *Server) handleRank(w http.ResponseWriter, r *http.Request) {
	if !s.protoReady(w) {
		return
	}
	q := r.URL.Query()
	selections := map[string]string{
		"rank":       q.Get("rank"),
		"rank_sub":   q.Get("rank_sub"),
		"rank_panel": q.Get("rank_panel"),
	}
	for _, value := range selections {
		if len(value) > maxRankSelectionLen {
			writeJSON(w, http.StatusBadRequest, errorResponse{Code: "PROTO_ERROR", Error: "榜单参数过长"})
			return
		}
	}
	cursor := q.Get("cursor")
	if len(cursor) > maxRankCursorLen {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "PROTO_ERROR", Error: "榜单游标过长"})
		return
	}

	page, err := s.proto.Rank(r.Context(), selections, cursor)
	if err != nil {
		s.writeProtoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}