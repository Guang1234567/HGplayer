package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testMedia 构造一个内存媒体提供者，返回固定内容。
func testMedia(t *testing.T, content []byte) func(id string) (int64, io.ReadSeeker, error) {
	t.Helper()
	return func(id string) (int64, io.ReadSeeker, error) {
		if id != "sample.mp4" {
			return 0, nil, errors.New("not found")
		}
		return int64(len(content)), bytes.NewReader(content), nil
	}
}

func newMediaServer(t *testing.T, provider MediaProvider) *Server {
	t.Helper()
	cfg := Config{
		Version:       "test",
		SessionToken:  "tok",
		MediaProvider: provider,
	}
	return New(cfg, nil)
}

// TestMediaRange206 验证 Range 请求返回 206 + Content-Range。
func TestMediaRange206(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 4096)
	s := newMediaServer(t, testMedia(t, content))

	req := httptest.NewRequest(http.MethodGet, "/media/sample.mp4", nil)
	req.Header.Set("Range", "bytes=0-1023")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rr.Code)
	}
	if got := rr.Header().Get("Content-Range"); got != "bytes 0-1023/4096" {
		t.Fatalf("Content-Range = %q", got)
	}
	if got := rr.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
	if body := rr.Body.String(); len(body) != 1024 {
		t.Fatalf("body len = %d, want 1024", len(body))
	}
}

// TestMediaOpenRange 验证开区间到末尾。
func TestMediaOpenRange(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 2000)
	s := newMediaServer(t, testMedia(t, content))

	req := httptest.NewRequest(http.MethodGet, "/media/sample.mp4", nil)
	req.Header.Set("Range", "bytes=1500-")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rr.Code)
	}
	if got := rr.Header().Get("Content-Range"); got != "bytes 1500-1999/2000" {
		t.Fatalf("Content-Range = %q", got)
	}
}

// TestMediaNoRange 验证无 Range 时返回 200 全量。
func TestMediaNoRange(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 1024)
	s := newMediaServer(t, testMedia(t, content))

	req := httptest.NewRequest(http.MethodGet, "/media/sample.mp4", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
}

// TestMediaUnsupportedRange 验证越界区间返回 416。
func TestMediaUnsupportedRange(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 500)
	s := newMediaServer(t, testMedia(t, content))

	req := httptest.NewRequest(http.MethodGet, "/media/sample.mp4", nil)
	req.Header.Set("Range", "bytes=9999-")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", rr.Code)
	}
}

// TestMediaNotFound 验证缺失媒体返回 404。
func TestMediaNotFound(t *testing.T) {
	s := newMediaServer(t, testMedia(t, []byte("x")))
	req := httptest.NewRequest(http.MethodGet, "/media/missing.mp4", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "媒体不存在") {
		t.Fatalf("body = %q", rr.Body.String())
	}
}
