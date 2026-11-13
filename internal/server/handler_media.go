package server

import (
	"net/http"
	"time"
)

// mediaError 是媒体代理的错误响应。
type mediaError struct {
	Error string `json:"error"`
}

// handleMedia 是媒体代理（伪 CDN）入口。
//
// 直接委托给 http.ServeContent：它内建解析 Range 请求头，按区间写出
// 206 + Content-Range，并在区间非法时返回 416、在无 Range 时返回 200。
//
// 真实解密与上游取流由 internal/service 承接；本 spike 阶段以本地
// 静态文件为源，验证 Range 语义与 <video> 拖拽 seek 的正确性。
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, mediaError{Error: "缺少媒体标识"})
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

	// ServeContent 自动处理 Range：支持时返回 206 + Content-Range，
	// 不支持时返回 200 全量，区间越界返回 416。
	http.ServeContent(w, r, "", time.Now(), src)
}
