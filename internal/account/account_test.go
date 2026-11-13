package account

import (
	"errors"
	"testing"
)

// memKeychain 是内存版凭证存储，用于测试。
type memKeychain struct {
	m map[string]string
}

func newMemKeychain() *memKeychain { return &memKeychain{m: map[string]string{}} }

func (k *memKeychain) Encrypt(plain string) (string, error) {
	k.m["tok"] = plain
	return "enc:" + plain, nil
}

func (k *memKeychain) Decrypt(cipher string) (string, error) {
	if len(cipher) < 4 || cipher[:4] != "enc:" {
		return "", errors.New("bad cipher")
	}
	return cipher[4:], nil
}

func TestLoginAndLogout(t *testing.T) {
	m := NewManager(newMemKeychain())
	if m.LoggedIn() {
		t.Fatal("should not be logged in initially")
	}

	if err := m.Login("acct-1", "用户A", "token-abc", 0, 1000); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !m.LoggedIn() {
		t.Fatal("should be logged in")
	}
	if m.Token() != "token-abc" {
		t.Fatalf("token = %q", m.Token())
	}
	if m.Current().AccountID != "acct-1" {
		t.Fatalf("account = %q", m.Current().AccountID)
	}

	m.Logout()
	if m.LoggedIn() {
		t.Fatal("should be logged out")
	}
	if m.Token() != "" {
		t.Fatal("token should be empty after logout")
	}
}

func TestLoginValidation(t *testing.T) {
	m := NewManager(newMemKeychain())
	if err := m.Login("", "x", "tok", 0, 1); err == nil {
		t.Fatal("empty account should fail")
	}
	if err := m.Login("acct", "x", "", 0, 1); err == nil {
		t.Fatal("empty token should fail")
	}
}

func TestLoginStoresEncryptedToken(t *testing.T) {
	kc := newMemKeychain()
	m := NewManager(kc)
	_ = m.Login("acct-1", "用户A", "secret-token", 0, 1000)

	stored := m.Stored()
	if stored == nil {
		t.Fatal("stored should not be nil")
	}
	if stored.Token == "secret-token" {
		t.Fatal("token should be encrypted on disk")
	}
	if stored.Token != "enc:secret-token" {
		t.Fatalf("stored token = %q", stored.Token)
	}
}

func TestLoadRestoresSession(t *testing.T) {
	kc := newMemKeychain()
	m := NewManager(kc)
	_ = m.Login("acct-2", "用户B", "tok-2", 0, 1000)

	// 模拟重启：新建管理器并加载持久化记录。
	m2 := NewManager(kc)
	if err := m2.Load(m.Stored()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !m2.LoggedIn() {
		t.Fatal("should be logged in after load")
	}
	if m2.Token() != "tok-2" {
		t.Fatalf("token = %q", m2.Token())
	}
}

func TestExpired(t *testing.T) {
	m := NewManager(newMemKeychain())
	// expiresAt = 2000，当前 1000 未过期；当前 2000 已过期。
	_ = m.Login("acct", "x", "tok", 2000, 1000)
	if m.Expired(1000) {
		t.Fatal("should not be expired at 1000")
	}
	if !m.Expired(2000) {
		t.Fatal("should be expired at 2000")
	}

	// 0 表示不过期。
	_ = m.Login("acct", "x", "tok", 0, 1000)
	if m.Expired(99999999) {
		t.Fatal("0 expiry should never expire")
	}
}

func TestLoadWithoutKeychainFails(t *testing.T) {
	m := NewManager(nil)
	stored := &Stored{AccountID: "a", Token: "enc:x"}
	if err := m.Load(stored); err == nil {
		t.Fatal("should fail without keychain")
	}
}
