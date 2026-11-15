package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSetSessionCookieEnablesAPI 验证：壳页下发的会话 Cookie 能让 /api 通过守卫。
func TestSetSessionCookieEnablesAPI(t *testing.T) {
	s := newTestServer("tok")

	// 壳页响应下发 Cookie。
	shellRec := httptest.NewRecorder()
	s.SetSessionCookie(shellRec)
	res := shellRec.Result()
	cookies := res.Cookies()
	if len(cookies) == 0 {
		t.Fatal("壳页未下发 session cookie")
	}
	if cookies[0].Name != sessionCookieName {
		t.Fatalf("cookie name = %q, want %q", cookies[0].Name, sessionCookieName)
	}

	// 带上该 Cookie 请求 /api，应通过守卫。
	req := httptest.NewRequest(http.MethodGet, "/api/system/version", nil)
	req.AddCookie(cookies[0])
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("带 cookie 的 /api 请求 status = %d, want 200", rr.Code)
	}
}

// TestProtoRoutesRequireSession 验证协议路由同样受会话守卫保护。
func TestProtoRoutesRequireSession(t *testing.T) {
	s := newTestServer("tok")
	for _, path := range []string{"/api/catalog", "/api/search?q=x", "/api/series/1", "/api/play/v1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s 无会话时 status = %d, want 401", path, rr.Code)
		}
	}
}

// TestProtoRoutesWithoutClient 验证未配置协议客户端时返回 503（而非 500/404）。
func TestProtoRoutesWithoutClient(t *testing.T) {
	cfg := Config{Version: "t", SessionToken: "tok"}
	s := New(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "tok"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}
