package server

import (
	"fmt"
	"log/slog"
	"time"
)

// logAccess 的日志输出方法。按级别记录结构化访问日志。
//
// 仅记录路径、状态码与耗时，不记录完整 URL，避免凭证落入日志。
func (s *Server) logLevel(level, component string, status int, method, path string, duration time.Duration) {
	attrs := []slog.Attr{
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status", status),
		slog.Duration("duration", duration),
	}
	msg := fmt.Sprintf("%s %s", component, path)
	switch level {
	case "error":
		s.logger.LogAttrs(slog.LevelError, msg, attrs...)
	case "warn":
		s.logger.LogAttrs(slog.LevelWarn, msg, attrs...)
	default:
		s.logger.LogAttrs(slog.LevelInfo, msg, attrs...)
	}
}
