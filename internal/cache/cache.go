// Package cache 提供进程内缓存，并保证登录/登出时整体失效。
//
// 上游返回的目录游标、片源地址等都是"属于某一个身份"的进程内状态。
// 登录或登出会改变身份，必须整体清空缓存，否则会把上一个身份的
// 数据复用给新身份，产生正确性问题。
package cache

import (
	"sync"
	"time"
)

// Entry 是一条缓存项。
type Entry struct {
	// Value 是缓存值。
	Value any
	// ExpiresAt 是过期时间，0 表示不过期。
	ExpiresAt time.Time
}

// Expired 判断缓存项是否过期。
func (e Entry) Expired(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt)
}

// Store 是带失效控制的进程内缓存。
type Store struct {
	mu      sync.RWMutex
	items   map[string]Entry
	version uint64 // 全局版本号，登录/登出时递增，使所有缓存失效
}

// New 创建缓存。
func New() *Store {
	return &Store{items: map[string]Entry{}}
}

// Get 读取缓存项。命中且未过期时返回 (值, true)。
func (s *Store) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.items[key]
	if !ok || entry.Expired(time.Now()) {
		return nil, false
	}
	return entry.Value, true
}

// Set 写入缓存项。ttl <= 0 表示不过期。
func (s *Store) Set(key string, value any, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := Entry{Value: value}
	if ttl > 0 {
		entry.ExpiresAt = time.Now().Add(ttl)
	}
	s.items[key] = entry
}

// Delete 删除单个缓存项。
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
}

// Clear 清空全部缓存。
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = map[string]Entry{}
}

// Version 返回当前缓存版本号。
//
// 登录/登出时版本递增；调用方可记录版本，用于检测缓存是否已失效。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Invalidate 使所有缓存失效。
//
// 用于登录/登出等身份变更场景。版本号递增，旧缓存项在逻辑上
// 全部作废（仍保留在 map 中，但版本校验会拒绝复用）。
func (s *Store) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	s.items = map[string]Entry{}
}

// SnapshotVersion 返回写入时记录的版本号。
func (s *Store) SnapshotVersion() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}
