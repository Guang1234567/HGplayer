package device

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeviceSaveLoad(t *testing.T) {
	dir := t.TempDir()
	info := Info{DeviceID: "1624456208323876", IID: "1624456208327972", Cookie: "x=1"}
	if err := Save(dir, info); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, exists, err := Load(dir)
	if err != nil || !exists {
		t.Fatalf("Load: exists=%v err=%v", exists, err)
	}
	if got.DeviceID != info.DeviceID || got.IID != info.IID {
		t.Fatalf("got = %+v, want %+v", got, info)
	}
}

func TestDeviceLoadMissing(t *testing.T) {
	dir := t.TempDir()
	_, exists, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if exists {
		t.Fatal("expected not exists")
	}
}

func TestDeviceLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, exists, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if exists {
		t.Fatal("corrupt file should not be treated as existing")
	}
}

func TestDeviceReset(t *testing.T) {
	dir := t.TempDir()
	_ = Save(dir, Info{DeviceID: "1", IID: "2"})
	if err := Reset(dir); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	_, exists, _ := Load(dir)
	if exists {
		t.Fatal("device should be gone after reset")
	}
}

func TestDeviceSaveInvalid(t *testing.T) {
	if err := Save(t.TempDir(), Info{DeviceID: "1"}); err == nil {
		t.Fatal("expected error for incomplete device")
	}
}

func TestDeviceValid(t *testing.T) {
	if !Valid(Info{DeviceID: "1", IID: "2"}) {
		t.Fatal("valid device should pass")
	}
	if Valid(Info{DeviceID: "1"}) {
		t.Fatal("incomplete device should fail")
	}
}
