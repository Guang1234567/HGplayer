// Package media 承载媒体代理相关能力，包括 Range 请求解析与流式响应。
package media

import (
	"fmt"
	"strconv"
	"strings"
)

// Range 表示一次 HTTP Range 请求解析后的结果。
// Start/End 为闭区间（含 End）；当请求为开区间（如 bytes=0-）时 End 为最大允许值。
type Range struct {
	Start int64
	End   int64 // 闭区间；-1 表示"到末尾"
	Valid bool
}

// Total 表示总字节数。
type Total int64

// ParseRange 解析 "bytes=..." 形式的 Range 请求头。
//
// 支持的形态（与原 Node 实现一致，仅支持单一区间）：
//
//	bytes=0-1023         含两端闭区间
//	bytes=0-            开区间，到末尾
//	bytes=-500          后缀长度（最后 500 字节）
//
// 返回的 Range.Valid 表示是否成功解析。解析失败时返回 Valid=false，
// 调用方应据此回退为全量响应（HTTP 200）。
func ParseRange(header string, total int64) Range {
	r := Range{Valid: false}
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "bytes=") {
		return r
	}
	spec := strings.TrimSpace(header[len("bytes="):])
	if spec == "" || strings.Contains(spec, ",") {
		// 空串或多区间（不支持的形态）→ 按全量处理。
		return r
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return r
	}
	startStr := strings.TrimSpace(spec[:dash])
	endStr := strings.TrimSpace(spec[dash+1:])

	if startStr == "" {
		// 后缀长度形态：bytes=-N，取最后 N 个字节。
		suffix, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || suffix <= 0 {
			return r
		}
		if suffix > total {
			suffix = total
		}
		if total <= 0 {
			return r
		}
		r.Start = total - suffix
		r.End = total - 1
		r.Valid = true
		return r
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return r
	}
	if start >= total {
		// 起点超出范围 → 416 语义，标记 Valid 但调用方需单独处理。
		r.Start = start
		r.End = start
		r.Valid = true
		return r
	}
	if endStr == "" {
		r.Start = start
		r.End = total - 1
		r.Valid = true
		return r
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return r
	}
	if end >= total {
		end = total - 1
	}
	r.Start = start
	r.End = end
	r.Valid = true
	return r
}

// ContentRange 生成 HTTP 206 响应的 Content-Range 头值。
func ContentRange(start, end, total int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", start, end, total)
}

// AcceptRanges 常量，表示支持 Range。
const AcceptRanges = "bytes"
