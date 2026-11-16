// Package server 承载前端同源 HTTP 服务：路由、中间件、媒体代理。
//
// 前端与 /api、/media 同源（挂载到 Wails AssetServer 的 Handler），
// 复用"相对路径 + 同源"契约，前端无需改动调用语义。
package server

import (
	"encoding/json"
	"net/http"

	"hgplayer/internal/account"
	"hgplayer/internal/archive"
	"hgplayer/internal/cache"
	"hgplayer/internal/hgproto"
	"hgplayer/internal/logx"
	"hgplayer/internal/media"
	"hgplayer/internal/sign"
)

// Server 是应用的 HTTP 服务，持有配置、签名器、协议客户端、日志器、
// 会话、档案包与缓存。
type Server struct {
	cfg     Config
	signer  sign.Signer
	logger  *logx.Logger
	account *account.Manager
	archive *archive.Store
	cache   *cache.Store
	proto   *hgproto.Client
	media   *media.Proxy
}

// New 创建服务实例。logger/account/archive 为空时使用默认实例。
func New(cfg Config, signer sign.Signer) *Server {
	l := cfg.Logger
	if l == nil {
		l = logx.New(logx.Options{TailLines: 200})
	}
	mgr := cfg.Account
	if mgr == nil {
		mgr = account.NewManager(nil)
	}
	st := cfg.Archive
	if st == nil {
		st, _ = archive.New(cfg.ArchiveDir)
	}
	return &Server{
		cfg:     cfg,
		signer:  signer,
		logger:  l,
		account: mgr,
		archive: st,
		cache:   cache.New(),
		proto:   cfg.Proto,
		media:   cfg.MediaProxy,
	}
}

// Handler 返回完整的应用 HTTP 路由。
//
// 所有 /api/* 与 /media/* 都在同一个 origin 下，由 Wails AssetServer
// 的 Handler 统一承载。未匹配的路径交回 Wails 默认的前端静态资源处理。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// —— 系统 / 版本 ——
	mux.HandleFunc("GET /api/system/version", s.handleVersion)

	// —— 账号会话 ——
	mux.HandleFunc("GET /api/account", s.handleAccountGet)
	mux.HandleFunc("POST /api/account/login", s.handleAccountLogin)
	mux.HandleFunc("POST /api/account/logout", s.handleAccountLogout)

	// —— 本地档案包 ——
	mux.HandleFunc("GET /api/archive/history", s.handleArchiveHistory)
	mux.HandleFunc("GET /api/archive/favorites", s.handleArchiveFavorites)
	mux.HandleFunc("POST /api/archive/progress", s.handleArchiveSaveProgress)
	mux.HandleFunc("POST /api/archive/favorites/toggle", s.handleArchiveToggleFavorite)
	mux.HandleFunc("GET /api/archive/search-history", s.handleArchiveSearchHistory)
	mux.HandleFunc("POST /api/archive/search-history", s.handleArchiveUpdateSearchHistory)

	// —— 红果协议（目录 / 搜索 / 详情 / 取址）——
	mux.HandleFunc("GET /api/catalog", s.handleCatalog)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/series/{id}", s.handleSeries)
	mux.HandleFunc("GET /api/play/{vid}", s.handlePlay)

	// —— 排行榜（分类 / 榜单分页）——
	mux.HandleFunc("GET /api/rank/panel", s.handleRankPanel)
	mux.HandleFunc("GET /api/rank", s.handleRank)

	// —— 媒体代理 ——
	mux.HandleFunc("GET /media/{id}", s.handleMedia)

	return s.chain(mux)
}

// chain 组装中间件链。顺序：访问日志 → 本地会话 → 错误包装。
func (s *Server) chain(next http.Handler) http.Handler {
	return s.errorWrap(s.sessionGuard(s.logAccess(next)))
}

// SetSessionCookie 在应用壳响应上下发本地会话 Cookie。
//
// /index.html 由 Wails 静态资源处理器直接返回，不经过本服务的中间件，
// 因此由调用方（assetHandler）在服务壳页面时显式调用本方法。
func (s *Server) SetSessionCookie(w http.ResponseWriter) {
	if s.cfg.SessionToken == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.cfg.SessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// writeJSON 以 JSON 写出响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
