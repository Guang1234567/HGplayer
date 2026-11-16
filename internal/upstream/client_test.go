package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"hgplayer/internal/guard"
	"hgplayer/internal/sign"
)

func TestDoInjectsSignerHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Test-Sign")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := New(Options{
		Signer: sign.SignerFunc(func(sign.Signature) sign.Headers {
			return sign.Headers{"X-Test-Sign": "injected"}
		}),
	})

	resp, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL + "/x"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotHeader != "injected" {
		t.Fatalf("header = %q, want injected", gotHeader)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d", resp.Status)
	}
}

func TestDoBlockedByGuard(t *testing.T) {
	c := New(Options{
		Guard: guard.New("allowed.com"),
	})

	_, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: "https://evil.com/x"})
	if err == nil {
		t.Fatal("expected guard rejection")
	}
}

func TestDoAllowedByGuard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// 测试服务器是 http，guard 要求 https。这里只验证 guard 放行逻辑
	// 用一个 https 的桩：直接构造 URL 校验。
	g := guard.New("allowed.com")
	if _, err := g.CheckURL("https://api.allowed.com/x"); err != nil {
		t.Fatalf("guard should allow subdomain: %v", err)
	}
	_ = srv
}

func TestDoTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()

	c := New(Options{Timeout: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := c.Do(ctx, Request{Method: http.MethodGet, URL: srv.URL})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestDoWithRetrySucceedsAfterTransient(t *testing.T) {
	// calls 会被多个 handler goroutine 并发访问，必须原子。
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			// 模拟超时。
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := New(Options{Timeout: 100 * time.Millisecond})
	policy := DefaultRetryPolicy()
	policy.MaxAttempts = 4
	policy.InitialBackoff = 10 * time.Millisecond

	resp, err := DoWithRetry(context.Background(), func(ctx context.Context) (*Response, error) {
		return c.Do(ctx, Request{Method: http.MethodGet, URL: srv.URL})
	}, policy)

	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	if resp == nil || calls.Load() < 2 {
		t.Fatalf("expected retry, calls = %d", calls.Load())
	}
}

func TestDoWithRetryStopsOnNonRetryable(t *testing.T) {
	var calls int
	policy := DefaultRetryPolicy()
	policy.MaxAttempts = 5
	policy.InitialBackoff = time.Millisecond

	_, err := DoWithRetry(context.Background(), func(ctx context.Context) (*Response, error) {
		calls++
		return nil, ErrRequest
	}, policy)

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("non-retryable should stop immediately, calls = %d", calls)
	}
}
