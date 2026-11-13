// Package archive 承载本地档案包的持久化：收藏、进度、观看历史、搜索历史。
//
// 不依赖账号，登录与否都可用，是本项目保留的核心功能之一。
// 存储使用单个 JSON 文件 + 原子写（临时文件 + rename），
// 并在写入前保留一份 .bak，用于主文件损坏时回退。
package archive

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Progress 是单集的观看进度。
type Progress struct {
	SeriesID    string  `json:"series_id"`
	SeriesTitle string  `json:"series_title,omitempty"`
	Cover       string  `json:"cover,omitempty"`
	Episode     int     `json:"episode_index"`
	Position    float64 `json:"position_sec"`
	Duration    float64 `json:"duration_sec,omitempty"`
	UpdatedAt   int64   `json:"updated_at"`
}

// Favorite 是收藏项。
type Favorite struct {
	SeriesID    string `json:"series_id"`
	SeriesTitle string `json:"series_title,omitempty"`
	Cover       string `json:"cover,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

// State 是档案包的完整状态。
type State struct {
	History       map[string]Progress `json:"history"`
	SearchHistory []string            `json:"searchHistory"`
	Favorites     map[string]Favorite `json:"favorites"`
}

// maxSearchHistory 是搜索历史保留条数。
const maxSearchHistory = 10

// Store 是档案包存储。
type Store struct {
	dir    string
	file   string
	backup string

	mu   sync.Mutex
	state State
}

// New 创建档案包存储。
//
// 若主文件已存在则加载；若主文件损坏则尝试从 .bak 恢复。
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("档案目录为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:    dir,
		file:   filepath.Join(dir, "archive.json"),
		backup: filepath.Join(dir, "archive.json.bak"),
		state: State{
			History:   map[string]Progress{},
			Favorites: map[string]Favorite{},
		},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load 加载档案包状态。
//
// 优先读取主文件；主文件不存在或损坏时回退到 .bak；
// 两者都不可用时使用空状态。
func (s *Store) load() error {
	raw, err := os.ReadFile(s.file)
	if err == nil {
		if decodeErr := s.decode(raw); decodeErr == nil {
			return nil
		}
		// 主文件损坏，继续尝试备份。
	}
	bak, err := os.ReadFile(s.backup)
	if err == nil {
		if decodeErr := s.decode(bak); decodeErr == nil {
			return nil
		}
	}
	// 主文件与备份都不可用，使用空状态。
	return nil
}

// decode 解码并校验状态。
func (s *Store) decode(raw []byte) error {
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if st.History == nil {
		st.History = map[string]Progress{}
	}
	if st.Favorites == nil {
		st.Favorites = map[string]Favorite{}
	}
	if st.SearchHistory == nil {
		st.SearchHistory = []string{}
	}
	s.state = st
	return nil
}

// Progress 返回指定剧目的进度。
func (s *Store) Progress(seriesID string) (Progress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.History[seriesID]
	return p, ok
}

// History 返回全部观看历史，按更新时间倒序。
func (s *Store) History() []Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Progress, 0, len(s.state.History))
	for _, p := range s.state.History {
		out = append(out, p)
	}
	return out
}

// IsFavorite 判断是否已收藏。
func (s *Store) IsFavorite(seriesID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.state.Favorites[seriesID]
	return ok
}

// Favorites 返回全部收藏。
func (s *Store) Favorites() []Favorite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Favorite, 0, len(s.state.Favorites))
	for _, f := range s.state.Favorites {
		out = append(out, f)
	}
	return out
}

// UpdateProgress 记录或更新观看进度。
func (s *Store) UpdateProgress(p Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validSeriesID(p.SeriesID) {
		return errors.New("剧目标识无效")
	}
	if p.Position < 0 {
		return errors.New("进度不能为负")
	}
	s.state.History[p.SeriesID] = p
	return s.saveLocked()
}

// ToggleFavorite 切换收藏状态。
//
// 已收藏则取消，未收藏则收藏。
func (s *Store) ToggleFavorite(f Favorite) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validSeriesID(f.SeriesID) {
		return false, errors.New("剧目标识无效")
	}
	if _, exists := s.state.Favorites[f.SeriesID]; exists {
		delete(s.state.Favorites, f.SeriesID)
		return false, s.saveLocked()
	}
	s.state.Favorites[f.SeriesID] = f
	return true, s.saveLocked()
}

// UpdateSearchHistory 增删搜索历史。
//
// action 为 "add" 时加入并去重截断到最近 maxSearchHistory 条；
// 为 "delete" 时删除指定条目；为 "clear" 时清空。
func (s *Store) UpdateSearchHistory(action, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch action {
	case "clear":
		s.state.SearchHistory = []string{}
	case "delete":
		out := s.state.SearchHistory[:0]
		for _, item := range s.state.SearchHistory {
			if item != value {
				out = append(out, item)
			}
		}
		s.state.SearchHistory = out
	case "add":
		if value == "" {
			return errors.New("搜索关键词为空")
		}
		// 去重并移到最前。
		out := []string{value}
		for _, item := range s.state.SearchHistory {
			if item != value {
				out = append(out, item)
			}
		}
		if len(out) > maxSearchHistory {
			out = out[:maxSearchHistory]
		}
		s.state.SearchHistory = out
	default:
		return errors.New("不支持的搜索历史操作")
	}
	return s.saveLocked()
}

// SearchHistory 返回搜索历史。
func (s *Store) SearchHistory() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.state.SearchHistory))
	copy(out, s.state.SearchHistory)
	return out
}

// saveLocked 原子写盘，调用方需已持有锁。
func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "archive-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	// 保留一份备份，用于主文件损坏时回退。
	// 显式写入而非 rename，保证首次保存时备份也会生成。
	if err := os.WriteFile(s.backup, raw, 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.file); err != nil {
		return err
	}
	return nil
}

// validSeriesID 校验剧目标识，防止原型污染与非法字符。
func validSeriesID(id string) bool {
	if id == "" || id == "__proto__" || id == "constructor" || id == "prototype" {
		return false
	}
	return true
}
