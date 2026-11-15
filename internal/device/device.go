// Package device 承载匿名设备身份：设备注册、设备文件持久化、IID 轮换。
//
// 匿名设备身份是本客户端的基础：登录与否都依赖它获取播放与目录能力。
package device

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Info 是匿名设备身份。
type Info struct {
	DeviceID string `json:"device_id"`
	IID      string `json:"iid"`
	Cookie   string `json:"cookie,omitempty"`
	CDID     string `json:"cdid,omitempty"`
	// OpenUDID 是上游要求的设备指纹字段（取址查询串的一部分）。
	OpenUDID string `json:"openudid,omitempty"`
	// TTReq 是设备注册下发的会话标记（取址 cookie 携带）。
	TTReq string `json:"ttreq,omitempty"`
	// StoreRegion / StoreRegionSrc 是注册下发的区域标记。
	StoreRegion    string `json:"store_region,omitempty"`
	StoreRegionSrc string `json:"store_region_src,omitempty"`
	// 机型档案：注册时声明，后续所有请求必须保持一致。
	Model      string `json:"device_model,omitempty"`
	Brand      string `json:"device_brand,omitempty"`
	OSVersion  string `json:"os_version,omitempty"`
	OSAPI      string `json:"os_api,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	DPI        string `json:"dpi,omitempty"`
	Channel    string `json:"channel,omitempty"`
	Build      string `json:"build,omitempty"`
	// VersionCode / VersionName 是注册声明的客户端版本。
	VersionCode string `json:"version_code,omitempty"`
	VersionName string `json:"version_name,omitempty"`
}

// fileName 是设备身份持久化文件名。
const fileName = "playback_device.json"

// Load 加载已持久化的设备身份。
//
// 返回 (设备, 是否已存在)。文件不存在或损坏时返回 (空, false)，
// 调用方应据此触发注册。
func Load(dir string) (Info, bool, error) {
	if dir == "" {
		return Info{}, false, errors.New("设备目录为空")
	}
	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		if os.IsNotExist(err) {
			return Info{}, false, nil
		}
		return Info{}, false, err
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return Info{}, false, nil
	}
	if info.DeviceID == "" || info.IID == "" {
		return Info{}, false, nil
	}
	return info, true, nil
}

// Save 持久化设备身份。
func Save(dir string, info Info) error {
	if dir == "" {
		return errors.New("设备目录为空")
	}
	if info.DeviceID == "" || info.IID == "" {
		return errors.New("设备身份不完整")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fileName), raw, 0o600)
}

// Reset 轮换设备身份（删除本地持久化）。
func Reset(dir string) error {
	if dir == "" {
		return errors.New("设备目录为空")
	}
	return os.Remove(filepath.Join(dir, fileName))
}

// Valid 校验设备身份是否完整。
func Valid(info Info) bool {
	return info.DeviceID != "" && info.IID != ""
}
