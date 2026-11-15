package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// nowUnix 返回当前 Unix 时间戳（秒）。
func nowUnix() int64 { return time.Now().Unix() }

// —— 账号会话 ——
type accountResponse struct {
	LoggedIn    bool   `json:"logged_in"`
	AccountID   string `json:"account_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// loginRequest 是登录请求体。
type loginRequest struct {
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	// Token 是上游凭证明文；真实实现中由登录流程从上游换取。
	Token string `json:"token,omitempty"`
}

// handleAccountGet 返回当前登录态。
func (s *Server) handleAccountGet(w http.ResponseWriter, r *http.Request) {
	resp := accountResponse{LoggedIn: false}
	if cur := s.account.Current(); cur != nil {
		resp.LoggedIn = true
		resp.AccountID = cur.AccountID
		resp.DisplayName = cur.DisplayName
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAccountLogin 建立登录会话。
//
// 登录成功后使进程内缓存整体失效，防止上一个身份的数据被复用。
func (s *Server) handleAccountLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "请求体无效"})
		return
	}
	if req.AccountID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "账号标识不能为空"})
		return
	}

	// 真实实现中，token 由上游登录流程换取；此处用占位值。
	token := req.Token
	if token == "" {
		token = "token-" + req.AccountID
	}

	if err := s.account.Login(req.AccountID, req.DisplayName, token, 0, nowUnix()); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "登录失败"})
		return
	}

	// 登录后整体失效缓存，避免跨身份复用。
	if s.cache != nil {
		s.cache.Invalidate()
	}
	s.logger.Info("账号登录成功", "account_id", req.AccountID)

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleAccountLogout 注销会话，并使缓存整体失效。
func (s *Server) handleAccountLogout(w http.ResponseWriter, r *http.Request) {
	s.account.Logout()
	if s.cache != nil {
		s.cache.Invalidate()
	}
	s.logger.Info("账号已注销")
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// —— 本地档案包 ——

// archiveHistoryResponse 是观看历史响应。
type archiveHistoryResponse struct {
	History []archiveProgress `json:"history"`
}

// archiveFavoritesResponse 是收藏响应。
type archiveFavoritesResponse struct {
	Favorites []archiveFavorite `json:"favorites"`
}

// archiveProgress 对应 archive.Progress 的 JSON 形态。
type archiveProgress struct {
	SeriesID    string  `json:"series_id"`
	SeriesTitle string  `json:"series_title,omitempty"`
	Cover       string  `json:"cover,omitempty"`
	Episode     int     `json:"episode_index"`
	Position    float64 `json:"position_sec"`
	Duration    float64 `json:"duration_sec,omitempty"`
	UpdatedAt   int64   `json:"updated_at"`
}

// archiveFavorite 对应 archive.Favorite 的 JSON 形态。
type archiveFavorite struct {
	SeriesID    string `json:"series_id"`
	SeriesTitle string `json:"series_title,omitempty"`
	Cover       string `json:"cover,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

// handleArchiveHistory 返回观看历史（按更新时间倒序）。
func (s *Server) handleArchiveHistory(w http.ResponseWriter, r *http.Request) {
	items := s.archive.History()
	// 按更新时间倒序。
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].UpdatedAt > items[j-1].UpdatedAt; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}

	out := make([]archiveProgress, 0, len(items))
	for _, p := range items {
		out = append(out, archiveProgress{
			SeriesID:    p.SeriesID,
			SeriesTitle: p.SeriesTitle,
			Cover:       p.Cover,
			Episode:     p.Episode,
			Position:    p.Position,
			Duration:    p.Duration,
			UpdatedAt:   p.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, archiveHistoryResponse{History: out})
}

// handleArchiveFavorites 返回收藏列表。
func (s *Server) handleArchiveFavorites(w http.ResponseWriter, r *http.Request) {
	items := s.archive.Favorites()
	out := make([]archiveFavorite, 0, len(items))
	for _, f := range items {
		out = append(out, archiveFavorite{
			SeriesID:    f.SeriesID,
			SeriesTitle: f.SeriesTitle,
			Cover:       f.Cover,
			UpdatedAt:   f.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, archiveFavoritesResponse{Favorites: out})
}
