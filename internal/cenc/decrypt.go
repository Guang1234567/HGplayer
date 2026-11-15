package cenc

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
)

// CENC（cenc-aes-ctr）样本解密与容器重写。
//
// 上游片源是加密的 fMP4：stsd 里是 encv/enca 条目，样本 IV 在 senc 里，
// 每个样本用 AES-128-CTR 加密（计数器 = 8 字节 nonce ‖ 8 字节 0）。
// 解密后必须把容器改写成明文容器：恢复原始编码类型、剔除加密辅助 box、
// 并按新的 moov 长度重定位 chunk 偏移，否则播放器读不出样本。

// maxSamples 是单文件允许的最大样本数，防止异常容器耗尽内存。
const maxSamples = 100000

// Sample 是一个加密样本的位置与初始向量。
type Sample struct {
	// Offset 是样本在文件中的字节偏移。
	Offset int
	// Size 是样本字节长度。
	Size int
	// IV 是 senc 中的 8 字节 nonce（CTR 计数器的前 8 字节）。
	IV []byte
}

// EncryptedSamples 提取文件中所有加密样本，并按偏移升序返回。
//
// 第二个返回值表示容器是否声明了样本加密（存在 senc，或任一 track 为 encv/enca）。
// 未加密容器返回空样本列表。
func EncryptedSamples(data []byte) ([]Sample, bool, error) {
	top, err := Parse(data)
	if err != nil {
		return nil, false, err
	}
	for i := range top {
		if boxType(&top[i]) == "moof" {
			return nil, false, errors.New("暂不支持分片（fragmented）MP4")
		}
	}
	total := len(data)
	tracks, err := trackSamples(data, top, total)
	if err != nil {
		return nil, false, err
	}

	encrypted := false
	samples := make([]Sample, 0, 128)
	for _, track := range tracks {
		if track.Encrypted || len(track.IVs) > 0 {
			encrypted = true
		}
		for i, offset := range track.Offsets {
			if i >= len(track.IVs) {
				continue
			}
			size := track.Sizes[i]
			if offset+size > total {
				return nil, false, errors.New("样本超出文件范围")
			}
			samples = append(samples, Sample{Offset: offset, Size: size, IV: track.IVs[i]})
		}
	}
	if !encrypted {
		return nil, false, nil
	}
	sortSamples(samples)
	return samples, true, nil
}

// Decrypt 解出明文 MP4：解密所有样本并重写容器。
//
// key 是 16 字节内容密钥。输入未加密容器时原样返回。
func Decrypt(data, key []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, errors.New("内容密钥长度必须为 16 字节")
	}

	samples, encrypted, err := EncryptedSamples(data)
	if err != nil {
		return nil, err
	}
	if !encrypted {
		return data, nil
	}
	if len(samples) == 0 {
		return nil, errors.New("容器声明加密但没有可解密的样本")
	}

	out := make([]byte, len(data))
	copy(out, data)
	for _, sample := range samples {
		if err := decryptSample(key, sample, out); err != nil {
			return nil, err
		}
	}

	rewritten, err := rewriteContainer(out)
	if err != nil {
		return nil, err
	}
	return rewritten, nil
}

// decryptSample 用 AES-128-CTR 就地解密一个样本。
func decryptSample(key []byte, sample Sample, data []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	iv := make([]byte, aes.BlockSize)
	copy(iv, sample.IV)
	cipher.NewCTR(block, iv).XORKeyStream(data[sample.Offset:sample.Offset+sample.Size], data[sample.Offset:sample.Offset+sample.Size])
	return nil
}

// sortSamples 按偏移升序排序（样本可能来自多条 track）。
func sortSamples(samples []Sample) {
	for i := 1; i < len(samples); i++ {
		for j := i; j > 0 && samples[j].Offset < samples[j-1].Offset; j-- {
			samples[j], samples[j-1] = samples[j-1], samples[j]
		}
	}
}

// trackSample 是一条 track 的样本表。
type trackSample struct {
	Sizes     []int
	Offsets   []int
	IVs       [][]byte
	Encrypted bool
}

// trackSamples 解析所有 stbl 的样本表（stsz/stsc/stco|co64/senc/stsd）。
func trackSamples(data []byte, top []Box, total int) ([]trackSample, error) {
	var result []trackSample
	budget := 0

	for i := range top {
		for _, box := range walk(&top[i]) {
			if boxType(box) != "stbl" {
				continue
			}
			track, err := parseStbl(data, box, total, &budget)
			if err != nil {
				return nil, err
			}
			if track != nil {
				result = append(result, *track)
			}
		}
	}
	return result, nil
}

// parseStbl 解析一条 track 的 stbl，返回其样本表。
func parseStbl(data []byte, stbl *Box, total int, budget *int) (*trackSample, error) {
	stsz := findChild(stbl, "stsz")
	stco := findChild(stbl, "stco")
	if stco == nil {
		stco = findChild(stbl, "co64")
	}
	stsc := findChild(stbl, "stsc")
	senc := findChild(stbl, "senc")
	stsd := findChild(stbl, "stsd")

	encrypted := false
	if stsd != nil {
		for _, entry := range sampleEntryTypes(data, stsd) {
			if entry == "encv" || entry == "enca" {
				encrypted = true
			}
		}
	}
	if encrypted && (stsz == nil || stco == nil || stsc == nil || senc == nil) {
		return nil, errors.New("加密 track 缺少必需的样本表")
	}
	if stsz == nil || stco == nil || stsc == nil {
		return nil, nil
	}

	sizes, err := sampleSizes(data, stsz)
	if err != nil {
		return nil, err
	}
	*budget += len(sizes)
	if *budget > maxSamples {
		return nil, errors.New("样本数超过上限")
	}
	chunkOffsets, err := chunkOffsetsOf(data, stco)
	if err != nil {
		return nil, err
	}
	table, err := sampleToChunk(data, stsc)
	if err != nil {
		return nil, err
	}
	ivs, err := sampleIVs(data, senc, len(sizes))
	if err != nil {
		return nil, err
	}
	if senc == nil && (findChild(stbl, "saio") != nil || findChild(stbl, "saiz") != nil) {
		return nil, errors.New("暂不支持外部 CENC 辅助数据（saio/saiz）")
	}

	offsets, err := sampleOffsets(sizes, chunkOffsets, table)
	if err != nil {
		return nil, err
	}
	_ = total
	return &trackSample{Sizes: sizes, Offsets: offsets, IVs: ivs, Encrypted: encrypted}, nil
}

// sampleSizes 读取 stsz 的样本长度表。
func sampleSizes(data []byte, stsz *Box) ([]int, error) {
	payload := stsz.Payload
	if len(payload) < 12 {
		return nil, errors.New("stsz 过小")
	}
	sampleSize := int(binary.BigEndian.Uint32(payload[4:8]))
	count := int(binary.BigEndian.Uint32(payload[8:12]))
	if count < 0 || count > maxSamples {
		return nil, errors.New("stsz 样本数非法")
	}
	sizes := make([]int, count)
	if sampleSize != 0 {
		for i := range sizes {
			sizes[i] = sampleSize
		}
		return sizes, nil
	}
	if 12+count*4 > len(payload) {
		return nil, errors.New("stsz 表截断")
	}
	for i := range sizes {
		sizes[i] = int(binary.BigEndian.Uint32(payload[12+i*4:]))
	}
	return sizes, nil
}

// chunkOffsetsOf 读取 stco/co64 的 chunk 偏移表。
func chunkOffsetsOf(data []byte, stco *Box) ([]int, error) {
	payload := stco.Payload
	if len(payload) < 8 {
		return nil, errors.New("stco 过小")
	}
	count := int(binary.BigEndian.Uint32(payload[4:8]))
	stride := 4
	if boxType(stco) == "co64" {
		stride = 8
	}
	if 8+count*stride > len(payload) {
		return nil, errors.New("stco 表截断")
	}
	offsets := make([]int, count)
	for i := range offsets {
		at := 8 + i*stride
		if stride == 4 {
			offsets[i] = int(binary.BigEndian.Uint32(payload[at:]))
			continue
		}
		value := binary.BigEndian.Uint64(payload[at:])
		if value > uint64(^uint(0)>>1) {
			return nil, errors.New("co64 偏移超出范围")
		}
		offsets[i] = int(value)
	}
	return offsets, nil
}

// sampleToChunk 读取 stsc（每 chunk 的样本数）。
func sampleToChunk(data []byte, stsc *Box) ([][2]int, error) {
	payload := stsc.Payload
	if len(payload) < 8 {
		return nil, errors.New("stsc 过小")
	}
	count := int(binary.BigEndian.Uint32(payload[4:8]))
	if 8+count*12 > len(payload) {
		return nil, errors.New("stsc 表截断")
	}
	table := make([][2]int, 0, count)
	for i := 0; i < count; i++ {
		at := 8 + i*12
		first := int(binary.BigEndian.Uint32(payload[at:]))
		per := int(binary.BigEndian.Uint32(payload[at+4:]))
		table = append(table, [2]int{first, per})
	}
	return table, nil
}

// sampleIVs 读取 senc 的 8 字节样本 nonce。
func sampleIVs(data []byte, senc *Box, sampleCount int) ([][]byte, error) {
	if senc == nil {
		return nil, nil
	}
	payload := senc.Payload
	if len(payload) < 8 {
		return nil, errors.New("senc 过小")
	}
	if binary.BigEndian.Uint32(payload[0:4]) != 0 {
		return nil, errors.New("暂不支持带子样本的 senc")
	}
	count := int(binary.BigEndian.Uint32(payload[4:8]))
	if count != sampleCount || len(payload) != 8+count*8 {
		return nil, errors.New("senc 布局不受支持（需要整样本 + 8 字节 IV）")
	}
	ivs := make([][]byte, count)
	for i := range ivs {
		at := 8 + i*8
		ivs[i] = payload[at : at+8]
	}
	return ivs, nil
}

// sampleOffsets 按 chunk 映射把样本长度展开成文件偏移。
func sampleOffsets(sizes, chunkOffsets []int, table [][2]int) ([]int, error) {
	if len(sizes) == 0 {
		return nil, nil
	}
	if len(table) == 0 || table[0][0] != 1 {
		return nil, errors.New("stsc 首项非法")
	}
	perChunk := make(map[int]int)
	for i, entry := range table {
		first, per := entry[0], entry[1]
		next := len(chunkOffsets) + 1
		if i+1 < len(table) {
			next = table[i+1][0]
		}
		for chunk := first - 1; chunk < next-1; chunk++ {
			perChunk[chunk] = per
		}
	}

	offsets := make([]int, 0, len(sizes))
	index := 0
	for chunk := 0; chunk < len(chunkOffsets); chunk++ {
		offset := chunkOffsets[chunk]
		count := perChunk[chunk]
		if count == 0 {
			count = 1
		}
		for i := 0; i < count && index < len(sizes); i++ {
			offsets = append(offsets, offset)
			offset += sizes[index]
			index++
		}
	}
	if index != len(sizes) {
		return nil, errors.New("样本数与 chunk 映射不一致")
	}
	return offsets, nil
}

// sampleEntryTypes 返回 stsd 中每个 sample entry 的类型。
func sampleEntryTypes(data []byte, stsd *Box) []string {
	payload := stsd.Payload
	if len(payload) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(payload[4:8]))
	types := make([]string, 0, count)
	at := 8
	for i := 0; i < count; i++ {
		if at+8 > len(payload) {
			break
		}
		size := int(binary.BigEndian.Uint32(payload[at:]))
		if size < 8 || at+size > len(payload) {
			break
		}
		types = append(types, string(payload[at+4:at+8]))
		at += size
	}
	return types
}

// walkAll 深度优先遍历一组 box 及其子 box。
func walkAll(boxes []Box) []*Box {
	out := make([]*Box, 0, len(boxes))
	for i := range boxes {
		out = append(out, walk(&boxes[i])...)
	}
	return out
}

// walk 深度优先遍历 box 及其子 box。
func walk(box *Box) []*Box {
	out := make([]*Box, 0, 8)
	var visit func(*Box)
	visit = func(current *Box) {
		for i := range current.Children {
			visit(&current.Children[i])
		}
		out = append(out, current)
	}
	visit(box)
	return out
}

// findChild 在直接子 box 中按类型查找。
func findChild(box *Box, typ string) *Box {
	for i := range box.Children {
		if boxType(&box.Children[i]) == typ {
			return &box.Children[i]
		}
	}
	return nil
}

// boxType 返回 box 的类型字符串。
func boxType(box *Box) string { return string(box.Type[:]) }

// rewriteContainer 把解密后的容器改写成明文容器。
//
// 与参考实现一致：moov 已在 mdat 之前时保持原序，只有它落在 mdat 之后才前移；
// 其余 box 一律保持原始顺序与内容。
func rewriteContainer(data []byte) ([]byte, error) {
	top, err := Parse(data)
	if err != nil {
		return nil, err
	}
	moovIndex, mdatIndex := -1, -1
	for i := range top {
		switch boxType(&top[i]) {
		case "moov":
			if moovIndex < 0 {
				moovIndex = i
			}
		case "mdat":
			if mdatIndex < 0 {
				mdatIndex = i
			}
		}
	}
	if moovIndex < 0 {
		return nil, errors.New("缺少 moov box")
	}

	// 输出顺序：默认保持原序；moov 在 mdat 之后时才前移到 mdat 之前。
	order := make([]int, 0, len(top))
	for i := range top {
		if i == moovIndex && mdatIndex >= 0 && moovIndex > mdatIndex {
			continue
		}
		order = append(order, i)
	}
	if mdatIndex >= 0 && moovIndex > mdatIndex {
		insertAt := 0
		for i, index := range order {
			if boxType(&top[index]) == "mdat" {
				insertAt = i
				break
			}
		}
		order = append(order[:insertAt], append([]int{moovIndex}, order[insertAt:]...)...)
	}

	tree, err := rewriteMoovTree(data, &top[moovIndex])
	if err != nil {
		return nil, err
	}
	moov := moovBytes(&top[moovIndex], tree)

	// 每个源 box 在新文件中的起始偏移（供 chunk 偏移重定位使用）。
	destinations := make(map[int]int, len(order))
	at := 0
	for _, index := range order {
		destinations[top[index].Start] = at
		if index == moovIndex {
			at += len(moov)
			continue
		}
		at += top[index].Size
	}

	// 按新布局重定位 chunk 偏移，再序列化最终 moov（stco 的 payload 与原缓冲区共享）。
	if err := relocateChunkOffsets(data, tree, destinations); err != nil {
		return nil, err
	}
	moov = moovBytes(&top[moovIndex], tree)

	out := make([]byte, 0, len(data))
	for _, index := range order {
		if index == moovIndex {
			out = append(out, moov...)
			continue
		}
		box := &top[index]
		out = append(out, data[box.Start:box.Start+box.Size]...)
	}
	return out, nil
}

// dropBoxTypes 是重写容器时需要剔除的加密辅助 box。
var dropBoxTypes = map[string]bool{
	"senc": true, "saio": true, "saiz": true, "sgpd": true, "sbgp": true,
}

// rewriteMoovTree 重写 moov 子树：剔除加密辅助 box、把 encv/enca 恢复成明文条目。
//
// 返回的子树仍引用原缓冲区中的 payload，因此后续对 stco/co64 的重定位
// 会作用在同一份数据上；序列化请用 moovBytes。
func rewriteMoovTree(data []byte, moov *Box) ([]Box, error) {
	pruned := pruneEncBoxes(moov.Children)
	for i := range pruned {
		if boxType(&pruned[i]) != "trak" {
			continue
		}
		if err := rewriteSampleEntries(data, findDescendant(&pruned[i], "stsd")); err != nil {
			return nil, err
		}
	}
	return pruned, nil
}

// moovBytes 把 moov 子树序列化成完整 moov box。
func moovBytes(moov *Box, children []Box) []byte {
	return serializeBoxes([]Box{{Type: moov.Type, Children: children}})
}

// pruneEncBoxes 递归剔除加密辅助 box。
func pruneEncBoxes(boxes []Box) []Box {
	out := make([]Box, 0, len(boxes))
	for i := range boxes {
		box := boxes[i]
		if dropBoxTypes[boxType(&box)] {
			continue
		}
		if len(box.Children) > 0 {
			box.Children = pruneEncBoxes(box.Children)
		}
		out = append(out, box)
	}
	return out
}

// rewriteSampleEntries 把 stsd 中的 encv/enca 改写成原始编码的明文条目。
func rewriteSampleEntries(data []byte, stsd *Box) error {
	if stsd == nil {
		return nil
	}
	payload := stsd.Payload
	if len(payload) < 8 {
		return errors.New("stsd 过小")
	}
	count := int(binary.BigEndian.Uint32(payload[4:8]))

	entries := make([][]byte, 0, count)
	at := 8
	for i := 0; i < count; i++ {
		if at+8 > len(payload) {
			return errors.New("stsd entry 截断")
		}
		size := int(binary.BigEndian.Uint32(payload[at:]))
		if size < 8 || at+size > len(payload) {
			return errors.New("stsd entry size 非法")
		}
		typ := string(payload[at+4 : at+8])
		entry := payload[at : at+size]
		if typ == "encv" || typ == "enca" {
			restored, err := restoreSampleEntry(entry, typ)
			if err != nil {
				return err
			}
			entry = restored
		}
		entries = append(entries, entry)
		at += size
	}

	// stsd payload 布局：[version/flags(4)][entry_count(4)][entry...]。
	header := make([]byte, 8)
	copy(header, payload[:8])
	binary.BigEndian.PutUint32(header[4:], uint32(len(entries)))
	stsd.Payload = append(header, concatBytes(entries)...)
	stsd.Size = 8 + len(stsd.Payload)
	stsd.Children = nil
	return nil
}

// restoreSampleEntry 把一条加密 sample entry 改写成明文条目。
func restoreSampleEntry(entry []byte, typ string) ([]byte, error) {
	// encv：8 字节 box 头 + 78 字节 VisualSampleEntry 字段；
	// enca：8 字节 box 头 + 28 字节 AudioSampleEntry 字段。
	headerSize := 8 + 78
	if typ == "enca" {
		headerSize = 8 + 28
	}
	if len(entry) < headerSize {
		return nil, errors.New("加密 sample entry 过短")
	}
	// 子 box 的偏移相对「表头之后」的切片，拷贝时必须用同一个基准。
	childBuf := entry[headerSize:]
	children, err := Parse(childBuf)
	if err != nil {
		return nil, err
	}

	codec := ""
	for i := range children {
		if boxType(&children[i]) == "sinf" {
			if frma := findChild(&children[i], "frma"); frma != nil && len(frma.Payload) == 4 {
				codec = string(frma.Payload)
			}
		}
	}
	if codec == "" {
		// 退回到容器里的编解码配置推断。
		hasAVC, hasHEVC := false, false
		for i := range children {
			switch boxType(&children[i]) {
			case "avcC":
				hasAVC = true
			case "hvcC":
				hasHEVC = true
			}
		}
		switch {
		case hasAVC && !hasHEVC:
			codec = "avc1"
		case hasHEVC && !hasAVC:
			codec = "hvc1"
		}
	}
	if typ == "encv" && codec != "avc1" && codec != "avc3" && codec != "hvc1" && codec != "hev1" {
		return nil, fmt.Errorf("无法确定加密视频的原始编码：%q", codec)
	}
	if typ == "enca" && codec != "mp4a" && codec != "ac-3" && codec != "ec-3" && codec != "Opus" && codec != "fLaC" {
		return nil, fmt.Errorf("无法确定加密音频的原始编码：%q", codec)
	}

	out := make([]byte, headerSize, len(entry))
	copy(out, entry[:headerSize])
	copy(out[4:8], codec)
	for i := range children {
		if boxType(&children[i]) == "sinf" {
			continue
		}
		out = append(out, childBuf[children[i].Start:children[i].Start+children[i].Size]...)
	}
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	return out, nil
}

// relocateChunkOffsets 按新布局重写 stco/co64 的偏移。
func relocateChunkOffsets(data []byte, tree []Box, destinations map[int]int) error {
	for _, box := range walkAll(tree) {
		typ := boxType(box)
		if typ != "stco" && typ != "co64" {
			continue
		}
		payload := box.Payload
		if len(payload) < 8 {
			return errors.New("stco 过小")
		}
		count := int(binary.BigEndian.Uint32(payload[4:8]))
		stride := 4
		if typ == "co64" {
			stride = 8
		}
		if 8+count*stride > len(payload) {
			return errors.New("stco 表截断")
		}
		for i := 0; i < count; i++ {
			at := 8 + i*stride
			if stride == 4 {
				old := int(binary.BigEndian.Uint32(payload[at:]))
				updated, err := relocateOffset(old, data, destinations)
				if err != nil {
					return err
				}
				binary.BigEndian.PutUint32(payload[at:], uint32(updated))
				continue
			}
			old := int(binary.BigEndian.Uint64(payload[at:]))
			updated, err := relocateOffset(old, data, destinations)
			if err != nil {
				return err
			}
			binary.BigEndian.PutUint64(payload[at:], uint64(updated))
		}
	}
	return nil
}

// relocateOffset 把旧的绝对偏移换算成新布局下的偏移。
func relocateOffset(old int, data []byte, destinations map[int]int) (int, error) {
	for start, dest := range destinations {
		size := boxSizeAt(data, start)
		if old >= start && old < start+size {
			return dest + old - start, nil
		}
	}
	return 0, errors.New("chunk 偏移不在任何 box 内")
}

// boxSizeAt 读取给定偏移处 box 的总长度。
func boxSizeAt(data []byte, start int) int {
	if start+8 > len(data) {
		return 0
	}
	size := int(binary.BigEndian.Uint32(data[start:]))
	if size == 1 && start+16 <= len(data) {
		value := binary.BigEndian.Uint64(data[start+8:])
		if value > uint64(^uint(0)>>1) {
			return 0
		}
		return int(value)
	}
	if size == 0 {
		return len(data) - start
	}
	return size
}

// serializeBoxes 把 box 树序列化为连续缓冲区。
func serializeBoxes(boxes []Box) []byte {
	size := 0
	parts := make([][]byte, 0, len(boxes))
	for i := range boxes {
		box := boxes[i]
		payload := box.Payload
		if len(box.Children) > 0 {
			payload = serializeBoxes(box.Children)
		}
		buf := make([]byte, 8+len(payload))
		WriteBox(buf, box.Type, payload)
		parts = append(parts, buf)
		size += len(buf)
	}
	out := make([]byte, 0, size)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// concatBytes 拼接多个字节切片。
func concatBytes(parts [][]byte) []byte {
	size := 0
	for _, part := range parts {
		size += len(part)
	}
	out := make([]byte, 0, size)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// findDescendant 在 box 子树中查找第一个指定类型的 box。
func findDescendant(box *Box, typ string) *Box {
	for _, current := range walk(box) {
		if boxType(current) == typ {
			return current
		}
	}
	return nil
}
