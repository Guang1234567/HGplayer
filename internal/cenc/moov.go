package cenc

import (
	"encoding/binary"
	"errors"
)

// encSampleBoxTypes 是 CENC 加密相关的 sample 级 box 类型。
// 这些 box 携带加密元数据，重写容器时需要剔除或改写。
var encSampleBoxTypes = map[string]bool{
	"senc": true, // 样本加密数据
	"saio": true, // 样本加密辅助信息
	"saiz": true, // 样本加密辅助信息大小
	"sgpd": true, // 样本组描述
	"sbgp": true, // 样本组到样本映射
}

// stsdEntry 是 stsd box 中的一个 sample entry。
type stsdEntry struct {
	// Type 是 entry 类型（如 "avc1"、"hvc1"、"encv"、"enca"）。
	Type [4]byte
	// Size 是 entry 总长度（含 8 字节 header）。
	Size int
	// Payload 是 entry 内容（不含 header）。
	Payload []byte
	// Children 是 entry 内的子 box。
	Children []Box
}

// hasSampleEncryption 探测容器中是否存在加密样本（senc box）。
func hasSampleEncryption(boxes []Box) bool {
	for i := range boxes {
		if string(boxes[i].Type[:]) == "senc" {
			return true
		}
		if hasSampleEncryption(boxes[i].Children) {
			return true
		}
	}
	return false
}

// parseStsdEntries 解析 stsd box 中的 sample entry 列表。
func parseStsdEntries(stsd *Box) ([]stsdEntry, error) {
	if len(stsd.Payload) < 8 {
		return nil, errors.New("stsd box 过小")
	}
	// stsd payload 结构：[4 字节版本/标志][4 字节 entry 数][entry...]
	entryCount := int(binary.BigEndian.Uint32(stsd.Payload[4:8]))

	var entries []stsdEntry
	off := 8
	for i := 0; i < entryCount; i++ {
		if off+8 > len(stsd.Payload) {
			return nil, errors.New("stsd entry 截断")
		}
		size := int(binary.BigEndian.Uint32(stsd.Payload[off : off+4]))
		typ := [4]byte{stsd.Payload[off+4], stsd.Payload[off+5], stsd.Payload[off+6], stsd.Payload[off+7]}
		if size < 8 || off+size > len(stsd.Payload) {
			return nil, errors.New("stsd entry size 非法")
		}

		entryPayload := stsd.Payload[off+8 : off+size]
		entry := stsdEntry{Type: typ, Size: size, Payload: entryPayload}

		// 解析 entry 内的子 box（如 sinf、avcC）。
		if children, err := Parse(entryPayload); err == nil {
			entry.Children = children
		}

		entries = append(entries, entry)
		off += size
	}

	return entries, nil
}

// isEncryptedTrack 判断 track 是否加密（含 encv/enca entry）。
func isEncryptedTrack(stsd *Box) bool {
	entries, err := parseStsdEntries(stsd)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if string(e.Type[:]) == "encv" || string(e.Type[:]) == "enca" {
			return true
		}
	}
	return false
}

// PruneEncBoxes 从 box 树中剔除加密辅助 box（senc/saio/saiz/sgpd/sbgp）。
//
// 返回一个新的 box 树，不修改输入。用于把加密容器改写为明文容器。
func PruneEncBoxes(boxes []Box) []Box {
	out := make([]Box, 0, len(boxes))
	for i := range boxes {
		b := boxes[i]
		typ := string(b.Type[:])

		if encSampleBoxTypes[typ] {
			// 加密辅助 box 直接丢弃。
			continue
		}

		if len(b.Children) > 0 {
			b.Children = PruneEncBoxes(b.Children)
		}
		out = append(out, b)
	}
	return out
}

// RewriteMoov 重写 moov box，把加密 track 的 encv/enca 改写为明文 track。
//
// 核心操作：
// 1. 剔除加密辅助 box（senc 等）。
// 2. 将 encv/enca 的 sinf 子 box 移除，恢复原始 codec 类型。
//
// 密钥派生由 sign 内部接口提供，本函数只处理容器结构。
func RewriteMoov(moov *Box) ([]byte, error) {
	if moov == nil || string(moov.Type[:]) != "moov" {
		return nil, errors.New("不是 moov box")
	}

	// 先剔除加密辅助 box。
	pruned := PruneEncBoxes(moov.Children)

	// 遍历 stsd，改写加密 entry。
	for i := range pruned {
		if string(pruned[i].Type[:]) != "trak" {
			continue
		}
		rewriteTrakSampleEntries(&pruned[i])
	}

	// 重新序列化：moov header + pruned children。
	buf := make([]byte, 8+len(moov.Payload))
	WriteBox(buf, moov.Type, serializeChildren(pruned))
	return buf, nil
}

// rewriteTrakSampleEntries 改写单个 trak 内的 sample entry。
func rewriteTrakSampleEntries(trak *Box) {
	stsd := Find(trak.Children, "stsd")
	if stsd == nil {
		return
	}
	entries, err := parseStsdEntries(stsd)
	if err != nil {
		return
	}

	for i := range entries {
		typ := string(entries[i].Type[:])
		if typ != "encv" && typ != "enca" {
			continue
		}
		// 加密 entry：移除 sinf 子 box，恢复原始 codec。
		entries[i].Children = removeSinf(entries[i].Children)
	}
}

// removeSinf 从 entry 的子 box 中移除 sinf 子树。
func removeSinf(children []Box) []Box {
	out := make([]Box, 0, len(children))
	for i := range children {
		if string(children[i].Type[:]) == "sinf" {
			continue
		}
		out = append(out, children[i])
	}
	return out
}

// serializeChildren 把子 box 树序列化为连续缓冲区。
func serializeChildren(boxes []Box) []byte {
	var buf []byte
	for i := range boxes {
		b := boxes[i]
		payload := b.Payload
		if len(b.Children) > 0 {
			payload = serializeChildren(b.Children)
		}
		boxBuf := make([]byte, 8+len(payload))
		WriteBox(boxBuf, b.Type, payload)
		buf = append(buf, boxBuf...)
	}
	return buf
}
