package archive

import (
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestArchivePersistsProgressAndFavorites(t *testing.T) {
	s := newStore(t)
	if err := s.UpdateProgress(Progress{SeriesID: "123", Position: 12, Episode: 2}); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	added, err := s.ToggleFavorite(Favorite{SeriesID: "123"})
	if err != nil || !added {
		t.Fatalf("ToggleFavorite add: added=%v err=%v", added, err)
	}

	// 重新加载，验证持久化。
	dir := filepath.Dir(s.file)
	reloaded, err := New(dir)
	if err != nil {
		t.Fatalf("New reload: %v", err)
	}
	if p, ok := reloaded.Progress("123"); !ok || p.Position != 12 {
		t.Fatalf("reloaded progress = %+v, want Position 12", p)
	}
	if !reloaded.IsFavorite("123") {
		t.Fatal("reloaded should have favorite")
	}

	// 再次切换应取消收藏。
	removed, err := reloaded.ToggleFavorite(Favorite{SeriesID: "123"})
	if err != nil || removed {
		t.Fatalf("ToggleFavorite remove: removed=%v err=%v", removed, err)
	}
	if reloaded.IsFavorite("123") {
		t.Fatal("favorite should be removed")
	}
}

func TestArchiveRejectsInvalidProgress(t *testing.T) {
	s := newStore(t)
	cases := []Progress{
		{SeriesID: ""},
		{SeriesID: "__proto__"},
		{SeriesID: "1", Position: -1},
	}
	for _, c := range cases {
		if err := s.UpdateProgress(c); err == nil {
			t.Fatalf("expected error for %+v", c)
		}
	}
}

func TestArchiveSearchHistory(t *testing.T) {
	s := newStore(t)
	for _, kw := range []string{"短剧", "演员", "爱情"} {
		if err := s.UpdateSearchHistory("add", kw); err != nil {
			t.Fatalf("add %q: %v", kw, err)
		}
	}
	// 最新在前。
	got := s.SearchHistory()
	if len(got) != 3 || got[0] != "爱情" {
		t.Fatalf("SearchHistory = %v, want [爱情 短剧 演员]", got)
	}

	// 去重：重复添加不改变长度，且移到最前。
	if err := s.UpdateSearchHistory("add", "短剧"); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	got = s.SearchHistory()
	if len(got) != 3 || got[0] != "短剧" {
		t.Fatalf("after re-add = %v", got)
	}

	// 删除。
	if err := s.UpdateSearchHistory("delete", "短剧"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got = s.SearchHistory()
	if len(got) != 2 {
		t.Fatalf("after delete = %v", got)
	}

	// 清空。
	if err := s.UpdateSearchHistory("clear", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := s.SearchHistory(); len(got) != 0 {
		t.Fatalf("after clear = %v", got)
	}
}

func TestArchiveSearchHistoryTruncated(t *testing.T) {
	s := newStore(t)
	for i := range 15 {
		if err := s.UpdateSearchHistory("add", string(rune('a'+i%26))); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if got := s.SearchHistory(); len(got) != maxSearchHistory {
		t.Fatalf("len = %d, want %d", len(got), maxSearchHistory)
	}
}

func TestArchiveRecoversFromBackup(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.UpdateProgress(Progress{SeriesID: "1", Position: 10}); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	// 损坏主文件。
	if err := os.WriteFile(s.file, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}


	// 重新加载应从 .bak 恢复。
	recovered, err := New(dir)
	if err != nil {
		t.Fatalf("New recovered: %v", err)
	}
	if p, ok := recovered.Progress("1"); !ok || p.Position != 10 {
		t.Fatalf("recovered progress = %+v, want Position 10", p)
	}
}

func TestArchiveWriteFailurePropagates(t *testing.T) {
	s := newStore(t)
	if err := s.UpdateProgress(Progress{SeriesID: "1", Position: 5}); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	// 删除数据目录，使后续原子写在 CreateTemp 阶段失败。
	if err := os.RemoveAll(filepath.Dir(s.file)); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	err := s.UpdateProgress(Progress{SeriesID: "2", Position: 9})
	if err == nil {
		t.Fatal("expected write failure")
	}
}
