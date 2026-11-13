package server

import "io"

// MediaProvider 提供媒体源的字节总数与只读句柄。
//
// 由 internal/service 在真实实现中注入，spike 阶段使用本地文件。
// 返回的 io.ReadSeeker 需支持随机读取（http.ServeContent 依赖 Seek）。
type MediaProvider func(id string) (int64, io.ReadSeeker, error)
