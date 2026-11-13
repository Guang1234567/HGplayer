package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// mediaDir 是 spike 阶段本地媒体文件所在目录。
var mediaDir = "media"

// localMediaProvider 是 Spike 阶段的媒体提供者：从本地 media 目录读取文件。
//
// 真实实现由 internal/service 提供，负责解密与上游取流。
func localMediaProvider(id string) (int64, io.ReadSeeker, error) {
	if !safeID(id) {
		return 0, nil, errors.New("非法媒体标识")
	}
	path := filepath.Join(mediaDir, id)
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return 0, nil, err
	}
	if info.IsDir() {
		f.Close()
		return 0, nil, errors.New("媒体标识指向目录")
	}
	return info.Size(), f, nil
}

// safeID 校验媒体标识，防止路径穿越。
func safeID(id string) bool {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "\\") ||
		strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return false
	}
	return true
}
