package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(token string) *Server {
	cfg := Config{
		Version:      "test-1.0.0",
		SessionToken: token,
		MediaProvider: func(id string) (int64, io.ReadSeeker, error) {
			return 0, nil, http.ErrNotSupported
		},
	}
	return New(cfg, nil)
}

func TestVersionEndpoint(t *testing.T) {
	s := newTestServer("tok")
	req := httptest.NewRequest(http.MethodGet, "/api/system/version", nil)
	// 会话守卫：未带 Cookie 应 401。
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("无会话时 status = %d, want 401", rr.Code)
	}

	// 带有效会话 Cookie。
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "tok"})
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "test-1.0.0") {
		t.Fatalf("body = %q, want 包含版本号", body)
	}
}

func TestSessionGuardRejectsWrongToken(t *testing.T) {
	s := newTestServer("correct")
	req := httptest.NewRequest(http.MethodGet, "/api/system/version", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "wrong"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// TestSessionGuard401Code 验证 401 携带 LOCAL_SESSION 语义码，
// 供前端与上游账号凭证过期（ACCOUNT_TOKEN）区分处理。
func TestSessionGuard401Code(t *testing.T) {
	s := newTestServer("tok")
	req := httptest.NewRequest(http.MethodGet, "/api/system/version", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), CodeLocalSession) {
		t.Fatalf("body = %q, want 包含 %s", rr.Body.String(), CodeLocalSession)
	}
	if strings.Contains(rr.Body.String(), CodeAccountToken) {
		t.Fatal("401 不应携带 ACCOUNT_TOKEN 语义")
	}
}

func TestSessionGuardPassesMedia(t *testing.T) {
	// /media 不要求会话 Cookie，仅 /api 要求。
	s := newTestServer("tok")
	req := httptest.NewRequest(http.MethodGet, "/media/abc", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("/media 不应被会话守卫拦截，got %d", rr.Code)
	}
}
