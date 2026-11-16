package cenc

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrNeedMoreHead 表示头部窗口不足以解析出完整布局，调用方应扩大窗口后重试。
var ErrNeedMoreHead = errors.New("头部窗口不足")

// Plan 描述一份加密片源的可解密布局（由文件头部窗口解析得到）。
//
// 布局依赖一个事实：容器重写只改变 moov 的长度（剔除 senc/saio/saiz/sgpd/sbgp
// 与 sample entry 里的 sinf），其余顶层 box 原样保留，因此载荷区满足
//
//	明文偏移 = 上游偏移 + Shift（剔除加密辅助 box 后 Shift 为负，明文更短）
//
// 前缀覆盖明文文件的起始部分（到载荷 box 的内容起点，含其 header）。
type Plan struct {
	// Total 是上游加密文件的总字节数。
	Total int64
	// Prefix 是明文文件的起始字节。
	Prefix []byte
	// Shift 把上游偏移换算成明文偏移：plain = upstream + Shift（载荷区适用）。
	Shift int64
	// Samples 是加密样本，按上游偏移升序；Offset/Size 为上游坐标。
	Samples []Sample
}

// PlainSize 是解密后文件的总字节数。
func (p *Plan) PlainSize() int64 { return p.Total + p.Shift }

// PrefixSize 是明文前缀的长度（载荷内容的明文起点）。
func (p *Plan) PrefixSize() int64 { return int64(len(p.Prefix)) }

// SampleIndex 返回第一个可能覆盖明文偏移 offset 的样本下标。
//
// 样本按上游偏移升序且互不重叠，因此也按结束偏移升序，可二分查找。
func (p *Plan) SampleIndex(offset int64) int {
	up := offset - p.Shift
	lo, hi := 0, len(p.Samples)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if int64(p.Samples[mid].Offset)+int64(p.Samples[mid].Size) <= up {
			lo = mid + 1
			continue
		}
		hi = mid
	}
	return lo
}

// BuildPlan 用文件头部窗口解析布局。
//
// head 必须包含完整的 moov 与载荷 box 的 header；不足时返回 ErrNeedMoreHead，
// 调用方应扩大窗口（如 bytes=0-131071）后重试。total 是上游文件总长度。
func BuildPlan(head []byte, total int64) (*Plan, error) {
	if total <= 0 {
		return nil, errors.New("文件长度非法")
	}
	if int64(len(head)) > total {
		return nil, errors.New("头部窗口超过文件长度")
	}
	// 解析会就地重写 moov（stco 重定位、sample entry 改写），
	// 先复制一份，避免影响调用方的缓冲区。
	head = bytes.Clone(head)

	// 1. 扫描顶层 box；最后一个 box（载荷）允许只取到 header。
	var (
		moovIndex    = -1
		moov         Box
		payloadIndex = -1
		payload      topBoxHeader
	)
	boxes := make([]topBoxHeader, 0, 8)
	off := int64(0)
	for {
		if off+8 > int64(len(head)) {
			return nil, fmt.Errorf("%w: 顶层 box header 不完整", ErrNeedMoreHead)
		}
		size := int64(binary.BigEndian.Uint32(head[off : off+4]))
		typ := [4]byte{head[off+4], head[off+5], head[off+6], head[off+7]}
		header := int64(8)
		if size == 1 {
			if off+16 > int64(len(head)) {
				return nil, fmt.Errorf("%w: 扩展 size 不完整", ErrNeedMoreHead)
			}
			size = int64(binary.BigEndian.Uint64(head[off+8 : off+16]))
			header = 16
		} else if size == 0 {
			size = total - off
		}
		if size < header || off+size > total {
			return nil, errors.New("MP4 box 长度非法")
		}
		current := topBoxHeader{typeID: typ, start: off, size: size, header: header}
		boxes = append(boxes, current)

		if string(typ[:]) == "moov" && moovIndex < 0 {
			if off+size > int64(len(head)) {
				return nil, fmt.Errorf("%w: moov 未取全", ErrNeedMoreHead)
			}
			parsed, err := Parse(head[off : off+size])
			if err != nil {
				return nil, err
			}
			if len(parsed) != 1 {
				return nil, errors.New("moov 解析失败")
			}
			moov, moovIndex = parsed[0], len(boxes)-1
		}

		if off+size > int64(len(head)) {
			// 载荷 box：header 已在窗口内，内容未取全。
			if off+header > int64(len(head)) {
				return nil, fmt.Errorf("%w: 载荷 header 不完整", ErrNeedMoreHead)
			}
			payloadIndex, payload = len(boxes)-1, current
			break
		}
		off += size
	}

	if moovIndex < 0 {
		return nil, fmt.Errorf("%w: 未在头部窗口内找到 moov", ErrNeedMoreHead)
	}
	if payloadIndex < 0 {
		return nil, fmt.Errorf("%w: 未找到未取全的载荷 box", ErrNeedMoreHead)
	}
	if moovIndex > payloadIndex {
		return nil, errors.New("moov 位于载荷之后，暂不支持窗口式解密")
	}

	// 2. 提取加密样本（上游坐标），并校验落在载荷区内。
	tracks, err := trackSamples(head, []Box{moov}, int(total))
	if err != nil {
		return nil, err
	}
	samples := make([]Sample, 0, 128)
	for _, track := range tracks {
		for i, offset := range track.Offsets {
			if i >= len(track.IVs) {
				continue
			}
			size := track.Sizes[i]
			if int64(offset)+int64(size) > total {
				return nil, errors.New("样本超出文件范围")
			}
			samples = append(samples, Sample{Offset: offset, Size: size, IV: track.IVs[i]})
		}
	}
	if len(samples) == 0 {
		return nil, errors.New("未找到可解密的加密样本")
	}
	sortSamples(samples)

	// 3. 重写 moov 并按新布局重定位 chunk 偏移。
	tree, err := rewriteMoovTree(head, &moov)
	if err != nil {
		return nil, err
	}
	moovSize := 8 + boxTreeSize(tree)

	destinations := make(map[int]int, len(boxes))
	at := int64(0)
	for i := range boxes {
		destinations[int(boxes[i].start)] = int(at)
		if i == moovIndex {
			at += int64(moovSize)
			continue
		}
		at += boxes[i].size
	}
	if err := relocateChunkOffsets(head, tree, destinations); err != nil {
		return nil, err
	}
	moovPlain := moovBytes(&moov, tree)

	// 4. 组装明文前缀：到载荷内容起点为止。
	prefix := make([]byte, 0, int(payload.start)+int(payload.header))
	for i := range boxes {
		if i == payloadIndex {
			prefix = append(prefix, head[payload.start:payload.start+payload.header]...)
			break
		}
		if i == moovIndex {
			prefix = append(prefix, moovPlain...)
			continue
		}
		prefix = append(prefix, head[boxes[i].start:boxes[i].start+boxes[i].size]...)
	}

	plan := &Plan{
		Total:   total,
		Prefix:  prefix,
		Shift:   int64(destinations[int(payload.start)]) - payload.start,
		Samples: samples,
	}
	payloadPlain := plan.PrefixSize()
	for i := range samples {
		start := int64(samples[i].Offset) + plan.Shift
		if start < payloadPlain {
			return nil, errors.New("加密样本落在明文前缀内")
		}
		if start+int64(samples[i].Size) > plan.PlainSize() {
			return nil, errors.New("加密样本超出明文文件范围")
		}
	}
	return plan, nil
}

// topBoxHeader 是顶层 box 的头部信息（内容可能未取全）。
type topBoxHeader struct {
	typeID [4]byte
	start  int64
	size   int64
	header int64
}

// boxTreeSize 计算 box 树序列化后的字节数（不分配缓冲区）。
func boxTreeSize(boxes []Box) int {
	size := 0
	for i := range boxes {
		if len(boxes[i].Children) > 0 {
			size += 8 + boxTreeSize(boxes[i].Children)
			continue
		}
		size += 8 + len(boxes[i].Payload)
	}
	return size
}

// FetchFunc 按上游字节区间取原始数据；返回长度必须等于区间长度。
type FetchFunc func(u0, u1 int64) ([]byte, error)

// ReadWindow 读出一段明文窗口。
//
// wStart/wEnd 是明文区间（可跨前缀与载荷）；fetch 按上游区间取原始字节。
// 只解密落在该区间内的样本，首尾被切开的样本按块号偏移局部解密。
func (p *Plan) ReadWindow(key []byte, wStart, wEnd int64, fetch FetchFunc) ([]byte, error) {
	if wStart < 0 || wEnd < wStart || wEnd > p.PlainSize() {
		return nil, errors.New("明文窗口超出文件范围")
	}
	windowStart, windowEnd := wStart, wEnd
	out := make([]byte, windowEnd-windowStart)

	// 1. 前缀部分直接来自计划。
	at := int64(0)
	if wStart < p.PrefixSize() {
		n := min(windowEnd, p.PrefixSize()) - wStart
		copy(out, p.Prefix[wStart:wStart+n])
		at, wStart = n, wStart+n
	}
	if wStart >= windowEnd {
		return out, nil
	}

	// 2. 载荷部分：上游区间与明文区间只差一个位移。
	uStart, uEnd := wStart-p.Shift, windowEnd-p.Shift
	raw, err := fetch(uStart, uEnd)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != uEnd-uStart {
		return nil, errors.New("上游返回长度与请求区间不一致")
	}
	for i := p.SampleIndex(wStart); i < len(p.Samples); i++ {
		sample := p.Samples[i]
		sampleStart := int64(sample.Offset)
		sampleEnd := sampleStart + int64(sample.Size)
		if sampleStart >= uEnd {
			break
		}
		from, to := max(sampleStart, uStart), min(sampleEnd, uEnd)
		if from >= to {
			continue
		}
		segment := raw[from-uStart : to-uStart]
		if err := DecryptSampleRange(key, sample.IV, segment, int(from-sampleStart), int(to-sampleStart)); err != nil {
			return nil, err
		}
	}
	copy(out[at:], raw)
	return out, nil
}

// DecryptSampleRange 就地解密样本中相对样本起点 [from, to) 的字节。
//
// 计数器块为 8 字节 nonce ‖ 64 位块号，块号自 from/16 起算，
// 因此可只解密窗口覆盖到的部分样本（首尾被切开的样本无需整只取回）。
// data 是待解密的缓冲区（非整只样本），from/to 是样本内的相对偏移。
func DecryptSampleRange(key, iv []byte, data []byte, from, to int) error {
	if len(key) != 16 {
		return errors.New("内容密钥长度必须为 16 字节")
	}
	if len(iv) != 8 {
		return errors.New("样本 IV 必须为 8 字节")
	}
	if to-from != len(data) {
		// from/to 是样本内偏移，data 只承载 [from, to) 这一段。
		return errors.New("样本窗口与缓冲区长度不一致")
	}
	if from == to {
		return nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	counter := make([]byte, aes.BlockSize)
	copy(counter, iv)
	binary.BigEndian.PutUint64(counter[8:], uint64(from/aes.BlockSize))
	stream := cipher.NewCTR(block, counter)
	if skip := from % aes.BlockSize; skip > 0 {
		var discard [aes.BlockSize]byte
		stream.XORKeyStream(discard[:skip], discard[:skip])
	}
	stream.XORKeyStream(data, data)
	return nil
}
