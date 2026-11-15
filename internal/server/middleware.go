package server

import (
	"net/http"
	"time"
)

// logAccess 记录 /api/* 与 /media/* 的访问日志。
//
// 仅记录路径与状态码，不记录完整 URL（避免凭证落入日志）。
func (s *Server) logAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIOrMedia(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		duration := time.Since(start)

		level := "info"
		if recorder.status >= 500 {
			level = "error"
		} else if recorder.status >= 400 {
			level = "warn"
		}
		s.logLevel(level, "API", recorder.status, r.Method, r.URL.Path, duration)
	})
}

// sessionGuard 是本地会话守卫。
//
// /api/* 要求携带本地会话 Cookie，否则返回 401；
// 首页（/ 与 /index.html）负责下发会话 Cookie。
func (s *Server) sessionGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if isAPIPath(path) {
			if s.cfg.SessionToken == "" || !s.hasSession(r) {
				writeJSON(w, http.StatusUnauthorized, errorResponse{
					Code:  CodeLocalSession,
					Error: "请从播放器首页建立本地会话",
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// errorWrap 统一包装 handler panic，返回 500。
func (s *Server) errorWrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logLevel("error", "API", http.StatusInternalServerError, "PANIC", r.URL.Path, 0)
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "操作失败，请稍后重试"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder 记录响应状态码，供日志中间件使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr.status == 0 {
		sr.status = code
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	return sr.ResponseWriter.Write(b)
}

// hasSession 检查请求是否携带有效本地会话 Cookie。
func (s *Server) hasSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return cookie.Value == s.cfg.SessionToken
}

const sessionCookieName = "player_session"

// isAPIOrMedia 判断路径是否需要访问日志。
func isAPIOrMedia(path string) bool {
	return isAPIPath(path) || isMediaPath(path)
}

func isAPIPath(path string) bool {
	return path == "/api" || len(path) > 4 && path[:5] == "/api/"
}

func isMediaPath(path string) bool {
	return path == "/media" || len(path) > 6 && path[:7] == "/media/"
}

// errorResponse 是错误响应结构。
//
// Code 用于区分两类 401：LOCAL_SESSION 表示本地会话未建立，
// ACCOUNT_TOKEN 表示上游账号凭证过期。前端据此分别处理。
type errorResponse struct {
	Code  string `json:"code,omitempty"`
	Error string `json:"error"`
}

// 401 语义常量。
const (
	CodeLocalSession = "LOCAL_SESSION"
	CodeAccountToken = "ACCOUNT_TOKEN"
)
