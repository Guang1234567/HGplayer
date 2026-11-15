package server

import (
	"net/http"

	"hgplayer/internal/account"
	"hgplayer/internal/archive"
	"hgplayer/internal/hgproto"
	"hgplayer/internal/logx"
	"hgplayer/internal/media"
)

// Config 是服务运行期配置。
type Config struct {
	// Version 是打包版本号，构建时注入。
	Version string
	// SessionToken 是本地会话令牌，启动时随机生成，用于本地会话守卫。
	SessionToken string
	// Logger 是应用日志器，为空时使用默认实例。
	Logger *logx.Logger
	// Account 是账号会话管理器，为空时使用默认实例。
	Account *account.Manager
	// Archive 是本地档案包存储，为空时按 ArchiveDir 创建。
	Archive *archive.Store
	// ArchiveDir 是本地档案包的数据目录。
	ArchiveDir string
	// Proto 是红果协议客户端；为空时依赖协议的路由返回 503。
	Proto *hgproto.Client
	// MediaProxy 是上游媒体代理；非空时 /media 走代理。
	MediaProxy *media.Proxy
	// MediaProvider 提供媒体源的字节总数与只读句柄。
	//
	// 返回的 io.ReadSeeker 需支持随机读取（http.ServeContent 依赖 Seek）。
	MediaProvider MediaProvider
}

// versionResponse 是 /api/system/version 的响应结构。
type versionResponse struct {
	Version string `json:"version"`
}

// handleVersion 返回打包版本号。
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{Version: s.cfg.Version})
}
