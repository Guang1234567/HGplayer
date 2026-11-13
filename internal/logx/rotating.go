package logx

import (
	"io"
	"os"
	"path/filepath"
)

// rotatingWriter 是按大小轮转的写入器。
//
// 写入超过 maxSize 字节时，把当前文件重命名为 .1，
// 依次顺延 .2、.3 …，超过 maxFiles 的旧文件被删除。
type rotatingWriter struct {
	path     string
	maxSize  int64
	maxFiles int

	file *os.File
	size int64
}

func newRotatingWriter(path string, maxSizeMB, maxFiles int) *rotatingWriter {
	if maxSizeMB <= 0 {
		maxSizeMB = 10
	}
	if maxFiles <= 0 {
		maxFiles = 5
	}
	return &rotatingWriter{
		path:     path,
		maxSize:  int64(maxSizeMB) * 1024 * 1024,
		maxFiles: maxFiles,
	}
}

// Write 写入并按需轮转。
func (w *rotatingWriter) Write(p []byte) (int, error) {
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size+int64(len(p)) > w.maxSize && w.size > 0 {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err == nil {
		w.size = info.Size()
	}
	w.file = f
	return nil
}

// rotate 轮转日志文件。
func (w *rotatingWriter) rotate() error {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	// 从最旧的开始删除，腾出位置。
	oldest := filepath.Join(w.path, ".") + fmtInt(w.maxFiles)
	os.Remove(oldest)
	// .N → .N+1
	for i := w.maxFiles - 1; i >= 1; i-- {
		from := filepath.Join(w.path, ".") + fmtInt(i)
		to := filepath.Join(w.path, ".") + fmtInt(i+1)
		if _, err := os.Stat(from); err == nil {
			os.Rename(from, to)
		}
	}
	// 当前 → .1
	if _, err := os.Stat(w.path); err == nil {
		os.Rename(w.path, filepath.Join(w.path, ".1"))
	}
	w.size = 0
	return w.open()
}

// Close 关闭底层文件。
func (w *rotatingWriter) Close() error {
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

var _ io.WriteCloser = (*rotatingWriter)(nil)
