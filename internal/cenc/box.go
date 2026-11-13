// Package cenc 处理 ISO BMFF（MP4）容器的解析与重写。
//
// 本包只实现标准的 MP4 box 结构解析与容器改写，不包含任何
// 私有加密算法或密钥派生。密钥派生通过 sign 包的内部接口接入，
// 由内部开发在不对外暴露的文件中实现。
//
// MP4 容器由一系列 box 组成，每个 box 有 8 字节头：
//
//	[4 字节 size][4 字节 type][payload]
//
// size 为 1 时使用 64 位扩展 size（header 共 16 字节）；
// size 为 0 时表示 box 延伸到文件末尾。
package cenc

import (
	"encoding/binary"
	"errors"
	"io"
)

// Box 是一个已解析的 MP4 box。
type Box struct {
	// Type 是 4 字节的 box 类型（如 "moov"、"mdat"）。
	Type [4]byte
	// Start 是 box 在缓冲区中的起始偏移（含 header）。
	Start int
	// Size 是 box 的总长度（含 header）。
	Size int
	// HeaderSize 是 header 长度（8 或 16）。
	HeaderSize int
	// Payload 是 box 内容（不含 header）。
	Payload []byte
	// Children 是容器型 box 的子 box；非容器型为 nil。
	Children []Box
}

// maxNesting 限制 box 嵌套深度，防止恶意数据导致栈溢出。
const maxNesting = 32

// containerTypes 是需要递归解析子 box 的容器类型。
var containerTypes = map[string]bool{
	"moov": true, "trak": true, "mdia": true, "minf": true,
	"stbl": true, "edts": true, "dinf": true, "udta": true,
	"mvex": true, "moof": true, "traf": true,
}

// Parse 解析缓冲区中的全部 box，递归展开容器型 box 的子 box。
//
// 返回的 Box 列表保持文件中的顺序。解析失败（截断、非法 size、
// 嵌套过深）返回错误。
func Parse(data []byte) ([]Box, error) {
	return parseRange(data, 0, len(data), 0)
}

// parseRange 解析 [start, end) 区间内的 box。
func parseRange(data []byte, start, end, depth int) ([]Box, error) {
	if depth > maxNesting {
		return nil, errors.New("MP4 box 嵌套超过上限")
	}

	var boxes []Box
	off := start
	for off+8 <= end {
		size := int(binary.BigEndian.Uint32(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		headerSize := 8

		switch {
		case size == 1:
			// 64 位扩展 size。
			if off+16 > end {
				return nil, errors.New("MP4 box header 截断")
			}
			ext := binary.BigEndian.Uint64(data[off+8 : off+16])
			if ext > uint64(len(data)) {
				return nil, errors.New("MP4 box size 非法")
			}
			size = int(ext)
			headerSize = 16
		case size == 0:
			// 延伸到末尾。
			size = end - off
		}

		if size < headerSize || off+size > end {
			return nil, errors.New("MP4 box size 非法或超出范围")
		}

		payload := data[off+headerSize : off+size]
		box := Box{
			Type:       [4]byte{data[off+4], data[off+5], data[off+6], data[off+7]},
			Start:      off,
			Size:       size,
			HeaderSize: headerSize,
			Payload:    payload,
		}

		// 容器型 box 递归解析子 box。
		if containerTypes[typ] {
			children, err := parseRange(payload, 0, len(payload), depth+1)
			if err != nil {
				return nil, err
			}
			box.Children = children
		}

		boxes = append(boxes, box)
		off += size
	}

	if off != end {
		return nil, errors.New("MP4 box 序列截断")
	}
	return boxes, nil
}

// Find 在 box 树中查找第一个指定类型的 box（深度优先，先序）。
func Find(boxes []Box, typ string) *Box {
	for i := range boxes {
		if string(boxes[i].Type[:]) == typ {
			return &boxes[i]
		}
		if len(boxes[i].Children) > 0 {
			if found := Find(boxes[i].Children, typ); found != nil {
				return found
			}
		}
	}
	return nil
}

// WriteBox 序列化一个 box 到缓冲区。
func WriteBox(buf []byte, typ [4]byte, payload []byte) {
	size := 8 + len(payload)
	binary.BigEndian.PutUint32(buf[:4], uint32(size))
	copy(buf[4:8], typ[:])
	copy(buf[8:], payload)
}

// ReadBox 从 reader 读取一个 box 的头信息（不含 payload）。
func ReadBox(r io.Reader) (typ [4]byte, size int, headerSize int, err error) {
	var head [8]byte
	if _, err = io.ReadFull(r, head[:]); err != nil {
		return
	}
	size = int(binary.BigEndian.Uint32(head[:4]))
	typ = [4]byte{head[4], head[5], head[6], head[7]}
	headerSize = 8

	if size == 1 {
		var ext [8]byte
		if _, err = io.ReadFull(r, ext[:]); err != nil {
			return
		}
		size = int(binary.BigEndian.Uint64(ext[:]))
		headerSize = 16
	} else if size == 0 {
		size = -1 // 延伸至末尾，由调用方处理。
	}
	return
}
