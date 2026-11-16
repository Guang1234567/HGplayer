package server

import (
	"errors"
	"net/http"
	"time"

	"hgplayer/internal/media"
)

// mediaError 是媒体代理的错误响应。
type mediaError struct {
	Code  string `json:"code,omitempty"`
	Error string `json:"error"`
}

// handleMedia 是媒体代理入口。
//
// 优先经上游媒体代理（Range 透传）；未配置代理时回退到本地 MediaProvider。
// 加密片源返回 501：解密管线属于受控实现，不在本服务内提供。
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, mediaError{Error: "缺少媒体标识"})
		return
	}

	if s.media != nil {
		err := s.media.Serve(w, r, id)
		switch {
		case err == nil:
			return
		case errors.Is(err, media.ErrEncrypted):
			writeJSON(w, http.StatusNotImplemented, mediaError{
				Code:  "ENC_REQUIRED",
				Error: "该片源受 CENC 保护，需要解密管线。",
			})
			return
		case errors.Is(err, media.ErrResponseStarted):
			// 响应已开始（流式供给中途失败）：只能中断连接，不能再写错误响应。
			s.logger.Warn("媒体流中断", "vid", id, "err", err.Error())
			return
		default:
			s.logger.Warn("媒体代理失败", "vid", id, "err", err.Error())
			writeJSON(w, http.StatusBadGateway, mediaError{
				Code:  "MEDIA_PROXY_FAILED",
				Error: err.Error(),
			})
			return
		}
	}

	// 回退：本地媒体文件（测试注入的 provider）。
	if s.cfg.MediaProvider == nil {
		writeJSON(w, http.StatusNotFound, mediaError{Error: "媒体不存在"})
		return
	}
	total, src, err := s.cfg.MediaProvider(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, mediaError{Error: "媒体不存在"})
		return
	}
	if total <= 0 || src == nil {
		writeJSON(w, http.StatusBadGateway, mediaError{Error: "媒体大小无效"})
		return
	}
	http.ServeContent(w, r, "", time.Now(), src)
}
