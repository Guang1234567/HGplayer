package cache

import (
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	s := New()
	s.Set("k", "v", 0)
	got, ok := s.Get("k")
	if !ok || got != "v" {
		t.Fatalf("got = %v, ok = %v", got, ok)
	}

	if _, ok := s.Get("missing"); ok {
		t.Fatal("missing key should not hit")
	}
}

func TestTTLExpiry(t *testing.T) {
	s := New()
	s.Set("k", "v", 10*time.Millisecond)

	if _, ok := s.Get("k"); !ok {
		t.Fatal("should hit immediately")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := s.Get("k"); ok {
		t.Fatal("should expire after TTL")
	}
}

func TestDeleteAndClear(t *testing.T) {
	s := New()
	s.Set("a", 1, 0)
	s.Set("b", 2, 0)

	s.Delete("a")
	if _, ok := s.Get("a"); ok {
		t.Fatal("deleted key should not hit")
	}

	s.Clear()
	if _, ok := s.Get("b"); ok {
		t.Fatal("cleared key should not hit")
	}
}

// TestInvalidateOnLoginLogout 验证登录/登出使缓存整体失效。
func TestInvalidateOnLoginLogout(t *testing.T) {
	s := New()
	s.Set("catalog-cursor", "cursor-A", 0)
	s.Set("play-url", "url-A", 0)

	v0 := s.Version()
	if _, ok := s.Get("catalog-cursor"); !ok {
		t.Fatal("should have cursor before invalidation")
	}

	// 模拟登录：整体失效。
	s.Invalidate()

	if _, ok := s.Get("catalog-cursor"); ok {
		t.Fatal("cursor should be invalidated after login")
	}
	if _, ok := s.Get("play-url"); ok {
		t.Fatal("play url should be invalidated after login")
	}
	if s.Version() != v0+1 {
		t.Fatalf("version = %d, want %d", s.Version(), v0+1)
	}

	// 登出同样失效。
	s.Set("new-cursor", "cursor-B", 0)
	s.Invalidate()
	if _, ok := s.Get("new-cursor"); ok {
		t.Fatal("should be invalidated after logout")
	}
}
