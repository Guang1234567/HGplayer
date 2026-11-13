// Package account 承载登录会话：账号身份、凭证存储与登录态管理。
//
// 与本地档案包（internal/archive）分离：
// - 档案包不依赖账号，登录与否都可用；
// - 账号会话是可选层，登录后叠加，用于上游账号态请求。
//
// 凭证存储通过 Keychain 接口抽象，由内部开发接入具体系统实现
// （Windows DPAPI / macOS Keychain），不在此暴露存储细节。
package account

import (
	"errors"
	"sync"
)

// Session 是登录会话。
type Session struct {
	// AccountID 是账号标识。
	AccountID string
	// DisplayName 是展示名称。
	DisplayName string
	// Token 是上游凭证（已加密存储的引用，明文不在此留存）。
	Token string
	// ExpiresAt 是凭证过期时间（Unix 秒），0 表示不过期。
	ExpiresAt int64
	// LoggedInAt 是登录时间（Unix 秒）。
	LoggedInAt int64
}

// Stored 是持久化的会话记录（Token 字段为加密后的值）。
type Stored struct {
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	Token       string `json:"token"`
	ExpiresAt   int64  `json:"expires_at"`
	LoggedInAt  int64  `json:"logged_in_at"`
}

// Keychain 是凭证的加密存储接口。
//
// 由内部开发接入具体系统实现（Windows DPAPI / macOS Keychain），
// 本包只依赖接口，不暴露存储细节。
type Keychain interface {
	// Encrypt 加密明文，返回可持久化的密文。
	Encrypt(plain string) (string, error)
	// Decrypt 解密密文，返回明文。
	Decrypt(cipher string) (string, error)
}

// Manager 管理登录会话。
type Manager struct {
	mu      sync.RWMutex
	keychain Keychain
	current *Session
	stored  *Stored
}

// NewManager 创建会话管理器。keychain 为 nil 时会话不可持久化。
func NewManager(keychain Keychain) *Manager {
	return &Manager{keychain: keychain}
}

// LoggedIn 判断当前是否已登录。
func (m *Manager) LoggedIn() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current != nil
}

// Current 返回当前会话（未登录返回 nil）。
func (m *Manager) Current() *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Token 返回当前凭证；未登录返回空串。
func (m *Manager) Token() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return ""
	}
	return m.current.Token
}

// Login 建立登录会话。
//
// token 是上游凭证明文，仅在内存中留存；持久化时经 keychain 加密。
func (m *Manager) Login(accountID, displayName, token string, expiresAt, now int64) error {
	if accountID == "" || token == "" {
		return errors.New("账号标识与凭证不能为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.current = &Session{
		AccountID:   accountID,
		DisplayName: displayName,
		Token:       token,
		ExpiresAt:   expiresAt,
		LoggedInAt:  now,
	}

	if m.keychain != nil {
		cipher, err := m.keychain.Encrypt(token)
		if err != nil {
			return err
		}
		m.stored = &Stored{
			AccountID:   accountID,
			DisplayName: displayName,
			Token:       cipher,
			ExpiresAt:   expiresAt,
			LoggedInAt:  now,
		}
	}
	return nil
}

// Logout 注销会话，清除内存与持久化记录。
func (m *Manager) Logout() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = nil
	m.stored = nil
}

// Load 从持久化记录恢复会话（需在启动时调用）。
func (m *Manager) Load(s *Stored) error {
	if s == nil || s.AccountID == "" {
		return errors.New("持久化会话无效")
	}
	if m.keychain == nil {
		return errors.New("未配置凭证存储")
	}
	token, err := m.keychain.Decrypt(s.Token)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.stored = s
	m.current = &Session{
		AccountID:   s.AccountID,
		DisplayName: s.DisplayName,
		Token:       token,
		ExpiresAt:   s.ExpiresAt,
		LoggedInAt:  s.LoggedInAt,
	}
	return nil
}

// Expired 判断会话是否已过期（需调用方提供当前时间）。
func (m *Manager) Expired(now int64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return false
	}
	if m.current.ExpiresAt == 0 {
		return false
	}
	return now >= m.current.ExpiresAt
}

// Stored 返回持久化记录（未登录返回 nil）。
func (m *Manager) Stored() *Stored {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stored
}
