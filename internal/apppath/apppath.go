// Package apppath 提供应用数据目录的解析。
//
// 数据目录承载日志、设备身份、本地档案包等用户数据，
// 开发态与打包态位置不同，统一在此解析。
package apppath

import (
	"os"
	"path/filepath"
)

// Dirs 是应用运行期用到的数据目录集合。
type Dirs struct {
	// Base 是应用数据根目录。
	Base string
	// Logs 是日志目录。
	Logs string
	// Data 是业务数据目录（档案包、设备身份）。
	Data string
}

// Resolve 解析应用数据目录。
//
// 优先使用 HGP_DATA_DIR 环境变量（便于测试与自定义），
// 否则使用系统的用户数据目录（开发态与打包态一致）。
func Resolve() (Dirs, error) {
	base := os.Getenv("HGP_DATA_DIR")
	if base == "" {
		base = defaultBase()
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return Dirs{}, err
	}
	return Dirs{
		Base: abs,
		Logs: filepath.Join(abs, "logs"),
		Data: filepath.Join(abs, "data"),
	}, nil
}

// Ensure 创建所有数据目录。
func (d Dirs) Ensure() error {
	for _, dir := range []string{d.Base, d.Logs, d.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// defaultBase 返回系统用户数据目录下的应用子目录。
func defaultBase() string {
	if os.Getenv("OS") == "Windows_NT" {
		if home, err := os.UserConfigDir(); err == nil {
			return filepath.Join(home, "hgplayer")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".hgplayer")
	}
	return "hgplayer-data"
}
