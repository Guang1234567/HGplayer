package apppath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HGP_DATA_DIR", dir)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Base != dir {
		t.Fatalf("Base = %q, want %q", got.Base, dir)
	}
	if got.Logs != filepath.Join(dir, "logs") {
		t.Fatalf("Logs = %q", got.Logs)
	}
	if got.Data != filepath.Join(dir, "data") {
		t.Fatalf("Data = %q", got.Data)
	}
}

func TestResolveDefault(t *testing.T) {
	// 清空环境变量，使用默认路径。
	t.Setenv("HGP_DATA_DIR", "")
	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Base == "" {
		t.Fatal("Base should not be empty")
	}
}

func TestEnsure(t *testing.T) {
	base := t.TempDir()
	d := Dirs{
		Base: base,
		Logs: filepath.Join(base, "logs"),
		Data: filepath.Join(base, "data"),
	}
	if err := d.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, dir := range []string{d.Logs, d.Data} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("dir %q not created: %v", dir, err)
		}
	}
}
