// Package logx 提供带文件轮转的应用日志。
//
// 基于标准库 log/slog，输出到文件并按大小轮转，
// 同时保留一份内存缓冲供诊断接口读取最近若干行。
package logx

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Options 是日志配置。
type Options struct {
	// Dir 是日志目录，为空时仅输出到标准错误。
	Dir string
	// MaxSizeMB 是单个日志文件的最大大小，超过后轮转。
	MaxSizeMB int
	// MaxFiles 是保留的轮转文件数。
	MaxFiles int
	// TailLines 是内存中保留的最近日志行数，供诊断读取。
	TailLines int
}

// Logger 是应用日志器。
type Logger struct {
	slog *slog.Logger
	mu   sync.Mutex
	tail []string
}

// New 创建日志器。
func New(opts Options) *Logger {
	l := &Logger{}
	var writer io.Writer = os.Stderr
	if opts.Dir != "" {
		if err := os.MkdirAll(opts.Dir, 0o755); err == nil {
			writer = newRotatingWriter(filepath.Join(opts.Dir, "app.log"),
				opts.MaxSizeMB, opts.MaxFiles)
		}
	}
	handler := slog.NewTextHandler(&tailWriter{w: writer, l: l, limit: opts.TailLines},
		&slog.HandlerOptions{Level: slog.LevelInfo})
	l.slog = slog.New(handler)
	return l
}

// Logger 返回底层 slog 日志器。
func (l *Logger) Logger() *slog.Logger { return l.slog }

// Info / Warn / Error 便捷方法。
func (l *Logger) Info(msg string, args ...any) { l.slog.Info(msg, args...) }
func (l *Logger) Warn(msg string, args ...any) { l.slog.Warn(msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.slog.Error(msg, args...) }

// LogAttrs 结构化记录。
func (l *Logger) LogAttrs(level slog.Level, msg string, attrs ...slog.Attr) {
	l.slog.LogAttrs(context.Background(), level, msg, attrs...)
}

// Tail 返回最近若干行日志。
func (l *Logger) Tail(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.tail) {
		n = len(l.tail)
	}
	out := make([]string, n)
	copy(out, l.tail[len(l.tail)-n:])
	return out
}

// tailWriter 在写入时同步维护一份最近行缓冲。
type tailWriter struct {
	w     io.Writer
	l     *Logger
	limit int
	buf   []byte
}

func (tw *tailWriter) Write(p []byte) (int, error) {
	n, err := tw.w.Write(p)
	for _, line := range splitLines(append(tw.buf, p...)) {
		tw.l.mu.Lock()
		tw.l.tail = append(tw.l.tail, line)
		if tw.limit > 0 && len(tw.l.tail) > tw.limit {
			tw.l.tail = tw.l.tail[len(tw.l.tail)-tw.limit:]
		}
		tw.l.mu.Unlock()
	}
	tw.buf = tw.buf[:0]
	return n, err
}

func splitLines(b []byte) []string {
	var lines []string
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			if i > start {
				lines = append(lines, string(b[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, string(b[start:]))
	}
	return lines
}
