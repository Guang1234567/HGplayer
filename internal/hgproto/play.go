package hgproto

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"hgplayer/internal/sign"
)

// PlayInfo 是单集播放信息（含解密所需参数）。
type PlayInfo struct {
	Vid        string     `json:"vid"`
	URL        string     `json:"url"`
	BackupURL  string     `json:"backup_url,omitempty"`
	Codec      string     `json:"codec"`
	Encrypted  bool       `json:"encrypted"`
	ContentKey string     `json:"content_key,omitempty"`
	VideoMeta  *VideoMeta `json:"video_meta,omitempty"`
	Definition string     `json:"definition,omitempty"`
}

// VideoMeta 是视频元信息。
type VideoMeta struct {
	Definition string `json:"definition"`
	VWidth     int    `json:"vwidth"`
	VHeight    int    `json:"vheight"`
	Bitrate    int    `json:"bitrate"`
	CodecType  string `json:"codec_type"`
}

// errUnsupportedSource 表示片源地址未通过安全校验（对齐源实现的 SECURITY_BLOCKED）。
var errUnsupportedSource = errors.New("App 片源返回了不受支持的服务地址")

// playSourceDomains 是允许的片源域名（对齐源实现 checkedUrl 的白名单）。
var playSourceDomains = []string{"qznovelvod.com", "fqnovelvod.com", "douyinvod.com"}

// playDigitPattern 用于从质量描述（如 "1080p"）中取出分辨率档位。
var playDigitPattern = regexp.MustCompile(`\d+`)

// Play 获取单集播放地址与解密参数。
//
// 移植自 beilunyang/openhongguo（Apache-2.0）的在役取址实现：
// 请求 /novel/player/video_model/v1/，body 为 {video_id, biz_param}，
// 并用真实 Argus 551 / Ladon 签名（不带 x-medusa）。
//
// 已知限制：响应是 CENC 保护的 H.265 片源，前端需要解密管线才能播放。
func (c *Client) Play(ctx context.Context, vid string) (*PlayInfo, error) {
	if !numericID.MatchString(vid) {
		return nil, inputErrf("分集编号无效")
	}

	target, headers, body := c.playbackSigned(vid)
	response, err := c.doJSONRaw(ctx, http.MethodPost, target, body, headers)
	if err != nil {
		return nil, fmt.Errorf("App 取址不可用：%v", err)
	}
	if code := codeOf(response); code != 0 {
		return nil, fmt.Errorf("App 接口暂未提供本集播放信息")
	}

	data, _ := response["data"].(map[string]any)
	model := data["video_model"]
	if model == nil {
		return nil, fmt.Errorf("App 接口未返回本集片源模型")
	}
	// 复用解析链路：把模型包成带 video_model 的分集条目。
	return c.parseEntryPlayInfo(ctx, vid, map[string]any{"video_model": model})
}

// 取址走独立签名：真实 Argus 551 / Ladon 信封，且不带 x-medusa。
//
// 2026-11-10 起取址接口（/novel/player/video_model/v1/）只接受真实信封：
// 4 字节时间戳桩、或与 551 信封同时出现的 x-medusa，都会被回以
// HTTP 200 + 空响应体。目录 / 详情 / 搜索等读接口仍沿用桩签名与 medusa。
const (
	// playbackHost 是取址接口域名（在役客户端的所有 /novel/ 请求都发到这里）。
	playbackHost = "https://api5-normal-sinfonlinea.fqnovel.com"
	// playbackPath 是取址接口路径。
	playbackPath = "/novel/player/video_model/v1/"
	// 取址请求声明的客户端版本（与在役客户端一致）。
	playbackVersionCode = "73932"
	playbackVersionName = "7.3.9.32"
	// playbackAID 是红果 App 的 aid。
	playbackAID = "8662"
	// playbackBranch 是取址请求要求命中的 f13 分支。
	playbackBranch = 1
)

// playbackPayload 是取址请求体。
// buildPlaybackBody 构造取址请求体。
//
// 用 map 而不是结构体：Go 的 json.Marshal 对 map 键排序，
// 与在役客户端发出的字节完全一致（请求体本身参与签名）。
func buildPlaybackBody(vid string) []byte {
	body, _ := json.Marshal(map[string]any{
		"video_id": vid,
		"biz_param": map[string]any{
			"video_id_type":             0,
			"need_all_video_definition": true,
			"video_platform":            3,
		},
	})
	return body
}

// playbackDeviceOrdered 返回取址请求的设备参数（插入序即签名顺序）。
func (c *Client) playbackDeviceOrdered() []kv {
	model, brand, osVersion, osAPI, resolution, dpi, channel, _ := c.device.profileOf()
	return []kv{
		{"iid", c.device.IID},
		{"device_id", c.device.DeviceID},
		{"ac", "wifi"},
		{"channel", channel},
		{"aid", playbackAID},
		{"app_name", "novelread"},
		{"cdid", c.device.CDID},
		{"version_code", playbackVersionCode},
		{"version_name", playbackVersionName},
		{"device_platform", "android"},
		{"os", "android"},
		{"ssmix", "a"},
		{"device_type", model},
		{"device_brand", brand},
		{"language", "zh"},
		{"os_api", osAPI},
		{"os_version", osVersion},
		{"manifest_version_code", playbackVersionCode},
		{"resolution", resolution},
		{"dpi", dpi},
		{"update_version_code", playbackVersionCode},
		{"host_abi", "arm64-v8a"},
		{"dragon_device_type", "phone"},
		{"pv_player", playbackVersionCode},
		{"compliance_status", "0"},
		{"need_personal_recommend", "1"},
		{"player_so_load", "1"},
		{"is_android_pad_screen", "0"},
		{"openudid", c.device.OpenUDID},
	}
}

// playbackSigned 构造取址请求：URL、请求头与请求体。
//
// 通过递增 _rticket 重试，直到命中 playbackBranch（与在役实现一致）。
func (c *Client) playbackSigned(vid string) (string, map[string]string, []byte) {
	body := buildPlaybackBody(vid)
	base := time.Now().UnixMilli()

	target := ""
	query := ""
	ticket := base
	for offset := int64(0); offset < 32; offset++ {
		ticket = base + offset
		khronos := uint32(ticket / 1000)
		pairs := append(c.playbackDeviceOrdered(),
			kv{"ts", strconv.FormatUint(uint64(khronos), 10)},
			kv{"_rticket", strconv.FormatInt(ticket, 10)},
		)
		query = encodeOrderedQuery(pairs)
		target = c.playbackAPI + "?" + query
		if sign.BranchOf(query, body, khronos) == playbackBranch {
			break
		}
	}
	return target, c.playbackHeaders(query, body, uint32(ticket/1000), ticket), body
}

// playbackHeaders 组装取址请求头：桩签名之外的字段 + 真实 Argus 551 / Ladon。
func (c *Client) playbackHeaders(query string, body []byte, khronos uint32, ticket int64) map[string]string {
	model, _, osVersion, _, _, _, _, build := c.device.profileOf()
	headers := map[string]string{
		"User-Agent":                playbackUserAgent(model, osVersion, build),
		"Accept":                    "application/json; charset=utf-8,application/x-protobuf",
		"Content-Type":              "application/json; charset=UTF-8",
		"x-xs-from-web":             "0",
		"x-tt-request-tag":          "t=0;n=0",
		"sdk-version":               "2",
		"passport-sdk-version":      "50561",
		"x-vc-bdturing-sdk-version": "3.7.2.cn",
		"x-ss-dp":                   playbackAID,
		"lc":                        "101",
		"x-ss-req-ticket":           strconv.FormatInt(ticket, 10),
		"x-khronos":                 strconv.FormatUint(uint64(khronos), 10),
		"x-gorgon":                  sign.Gorgon(query, body, khronos, sign.RandomUint16()),
		"x-helios":                  sign.Helios(khronos),
		"x-tt-dt":                   "",
		"x-ss-stub":                 sign.MD5Upper(body),
		"x-reading-request":         readingRequestID(ticket),
		"x-argus":                   sign.Argus551(body, query, khronos, sign.RandomUint32(), playbackAID, c.device.DeviceID, c.device.IID),
		"x-ladon":                   sign.Ladon551(khronos, sign.RandomUint32(), playbackAID),
	}
	// 取址请求必须带设备 cookie（与在役客户端一致；未注册时没有 ttreq）。
	if c.device.IID != "" {
		cookie := "passport_csrf_token=; passport_csrf_token_default=; store-region=cn-hb; store-region-src=did; install_id=" + c.device.IID
		if c.device.TTReq != "" {
			cookie += "; ttreq=" + c.device.TTReq
		}
		headers["Cookie"] = cookie
	}
	return headers
}

// readingRequestID 生成 x-reading-request：毫秒时间戳 + 随机数。
func readingRequestID(ticket int64) string {
	return strconv.FormatInt(ticket, 10) + "-" + strconv.FormatInt(int64(sign.RandomUint32()&0x7fffffff), 10)
}

// codeOf 读取响应中的业务状态码（兼容 Code / code，缺失或 0 视为成功）。
func codeOf(response map[string]any) int {
	if response == nil {
		return 0
	}
	for _, key := range []string{"Code", "code"} {
		switch value := response[key].(type) {
		case float64:
			if value != 0 {
				return int(value)
			}
		case int:
			if value != 0 {
				return value
			}
		case json.Number:
			if n, err := value.Int64(); err == nil && n != 0 {
				return int(n)
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n != 0 {
				return n
			}
		}
	}
	return 0
}

// —— 响应解析 ——

// parseEntryPlayInfo 从分集条目解析播放信息（对齐源实现 parseEntryPlayInfo）。
//
// 质量选项固定为 auto：取像素最高的一批片源，同档内按编码等级升序。
func (c *Client) parseEntryPlayInfo(ctx context.Context, vid string, entry map[string]any) (*PlayInfo, error) {
	model, err := playModel(entry)
	if err != nil {
		return nil, err
	}

	// video_model 内没有片源列表时，改用模型自带的 fallback_api 再取一次。
	inline := len(videoListOf(model)) > 0
	source := model
	if !inline {
		fallback := parseFallback(model["fallback_api"])
		if fallback == "" {
			return nil, fmt.Errorf("App 接口未返回本集片源地址")
		}
		outer, err := c.doJSONRaw(ctx, http.MethodGet, fallback, nil, map[string]string{"User-Agent": videoUA})
		if err != nil {
			return nil, err
		}
		videoInfo, _ := outer["video_info"].(map[string]any)
		if codeOf(outer) != 0 || codeOf(videoInfo) != 0 {
			return nil, fmt.Errorf("App 接口暂未提供本集播放信息")
		}
		source, _ = videoInfo["data"].(map[string]any)
		if source == nil {
			return nil, fmt.Errorf("App 接口未返回本集片源模型")
		}
	}

	candidates := playCandidates(videoListOf(source))
	rows := playAutoRows(candidates)
	if len(rows) == 0 {
		// 与源实现一致：只有「没有任何兼容片源」时才报 ByteVC2。
		if hasByteVC2(candidates) && !hasCompatibleCodec(candidates) {
			return nil, fmt.Errorf("本集使用 ByteVC2 编码，当前播放器暂不支持")
		}
		return nil, fmt.Errorf("本集暂未提供兼容的 H.264/HEVC 片源")
	}

	seed, err := playKeySeed(source)
	if err != nil {
		return nil, err
	}

	var (
		lastErr  error
		blocked  bool
		variants []playVariant
		seen     = make(map[string]bool)
	)
	for _, row := range playSelectedRows(rows) {
		spade := playText(row.item["spade_a"])
		encryptInfo := row.item["encrypt_info"]
		if spade == "" {
			if info, ok := encryptInfo.(map[string]any); ok {
				spade = playText(info["spade_a"])
			}
		}
		// 内联模型下声明了加密却没有 spade_a，视为响应无效。
		if inline && spade == "" && encryptInfo != nil && !encryptDisabled(encryptInfo) {
			lastErr = fmt.Errorf("本集片源缺少有效解密信息")
			continue
		}

		var contentKey []byte
		if spade != "" {
			contentKey, err = sign.SpadeKeyDeriver{}.DeriveContentKey([]byte(spade))
			if err != nil {
				lastErr = err
				continue
			}
		}

		for _, candidate := range playURLs(row.item) {
			media := cleanPlayURL(candidate)
			if seed != nil {
				media, err = sign.DecryptSpadeURL(candidate, seed)
				if err != nil {
					lastErr = err
					continue
				}
			}
			target, err := checkedPlayURL(media)
			if err != nil {
				lastErr = err
				if errors.Is(err, errUnsupportedSource) {
					blocked = true
				}
				continue
			}

			keyHex := ""
			if len(contentKey) > 0 {
				keyHex = hex.EncodeToString(contentKey)
			}
			identity := target + ":" + keyHex
			if seen[identity] {
				continue
			}
			seen[identity] = true
			variants = append(variants, playVariant{
				url:        target,
				contentKey: keyHex,
				codec:      playCodecName(row.codec),
				rawCodec:   row.codec,
				quality:    row.quality,
				width:      row.width,
				height:     row.height,
				bitrate:    row.bitrate,
			})
		}
	}

	if len(variants) > 0 {
		return buildPlayInfo(vid, variants), nil
	}
	if blocked {
		return nil, lastErr
	}
	return nil, fmt.Errorf("本集片源地址或密钥无法解析")
}

// playModel 取出分集条目中的 video_model（可能是 JSON 字符串或对象）。
func playModel(entry map[string]any) (map[string]any, error) {
	raw := entry["video_model"]
	switch value := raw.(type) {
	case map[string]any:
		return value, nil
	case string:
		var model map[string]any
		if err := json.Unmarshal([]byte(value), &model); err == nil && model != nil {
			return model, nil
		}
	}
	return nil, fmt.Errorf("App 接口未返回所选分集，已停止取址")
}

// videoListOf 取出片源列表条目。
//
// 源实现只把数组形式当成内联片源（Array.isArray）；为兼容真实响应里可能出现的
// 对象形式（键为清晰度），这里两种形式都接受，其余行为保持一致。
func videoListOf(model map[string]any) []map[string]any {
	switch list := model["video_list"].(type) {
	case []any:
		items := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if entry, ok := item.(map[string]any); ok && entry != nil {
				items = append(items, entry)
			}
		}
		return items
	case map[string]any:
		// 源实现按插入序遍历对象；Go 的 map 无序，这里按字典序保证结果确定。
		keys := make([]string, 0, len(list))
		for key := range list {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]map[string]any, 0, len(list))
		for _, key := range keys {
			if entry, ok := list[key].(map[string]any); ok && entry != nil {
				items = append(items, entry)
			}
		}
		return items
	}
	return nil
}

// playCandidate 是一个片源候选（已归一化编码、质量与分辨率信息）。
type playCandidate struct {
	item      map[string]any
	codec     string
	codecRank int
	quality   string
	pixels    int
	width     int
	height    int
	bitrate   int
}

// playCandidates 归一化片源条目，只保留带地址的候选。
func playCandidates(items []map[string]any) []playCandidate {
	candidates := make([]playCandidate, 0, len(items))
	for _, item := range items {
		if !hasPlayURL(item) {
			continue
		}
		meta, _ := item["video_meta"].(map[string]any)
		codec := strings.ToLower(firstText(item["codec_type"], meta["codec_type"]))
		quality := firstText(item["quality_desc"], item["definition"], meta["definition"], item["height"], item["vheight"])
		width, height := playSize(item, meta)
		pixels := 0
		if width > 0 && height > 0 {
			pixels = min(width, height)
		} else {
			pixels = playLeadingInt(quality)
		}
		candidates = append(candidates, playCandidate{
			item:      item,
			codec:     codec,
			codecRank: playCodecRank(codec),
			quality:   quality,
			pixels:    pixels,
			width:     width,
			height:    height,
			bitrate:   toInt(item["bitrate"]),
		})
	}
	return candidates
}

// hasPlayURL 判断条目是否带可用地址。
func hasPlayURL(item map[string]any) bool {
	if stringValue(item["main_url"]) != "" || stringValue(item["backup_url"]) != "" {
		return true
	}
	_, ok := item["backup_url_list"].([]any)
	return ok
}

// playURLs 按 main_url → backup_url → backup_url_list 的顺序列出候选地址。
func playURLs(item map[string]any) []string {
	urls := make([]string, 0, 3)
	for _, key := range []string{"main_url", "backup_url"} {
		if value := stringValue(item[key]); value != "" {
			urls = append(urls, value)
		}
	}
	if list, ok := item["backup_url_list"].([]any); ok {
		for _, value := range list {
			if text := stringValue(value); text != "" {
				urls = append(urls, text)
			}
		}
	}
	return urls
}

// playCodecRank 归一化编码等级：0 = H.264，1 = HEVC，3 = 不支持的编码。
//
// 源实现中 ByteVC2 在 allowBytevc2 时等级为 2；本移植未开放该选项，
// 因此 ByteVC2 始终按「不支持」处理（等级 3，见 parseEntryPlayInfo 的错误分支）。
func playCodecRank(codec string) int {
	switch codec {
	case "h264", "avc", "avc1":
		return 0
	case "hevc", "h265", "hvc1", "hev1", "bytevc1":
		return 1
	}
	return 3
}

// playCodecName 把编码名映射为对外的 codec 值（对齐源实现）。
func playCodecName(codec string) string {
	switch codec {
	case "bytevc1":
		return "hevc"
	case "bvc2":
		return "bytevc2"
	}
	return codec
}

// hasCompatibleCodec 判断是否存在兼容编码（H.264 / HEVC）。
func hasCompatibleCodec(candidates []playCandidate) bool {
	for _, row := range candidates {
		if row.codecRank < 2 {
			return true
		}
	}
	return false
}

// hasByteVC2 判断候选中是否存在 ByteVC2 片源。
func hasByteVC2(candidates []playCandidate) bool {
	for _, row := range candidates {
		if row.codec == "bytevc2" || row.codec == "bvc2" {
			return true
		}
	}
	return false
}

// playAutoRows 按 auto 质量筛选并排序候选：像素降序，同像素按编码等级升序。
//
// auto 质量下所有编码都可用，因此过滤条件等价于 rank < 2（兼容编码）。
func playAutoRows(candidates []playCandidate) []playCandidate {
	rows := make([]playCandidate, 0, len(candidates))
	for _, row := range candidates {
		if row.codecRank < 2 {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].pixels != rows[j].pixels {
			return rows[i].pixels > rows[j].pixels
		}
		return rows[i].codecRank < rows[j].codecRank
	})
	return rows
}

// playSelectedRows 取像素最高的一批片源（像素不可确认时取首个）。
//
// auto 表示「可用的最好画质」：同分辨率档的所有编码都保留，但不静默降级。
func playSelectedRows(rows []playCandidate) []playCandidate {
	if best := rows[0].pixels; best > 0 {
		selected := make([]playCandidate, 0, len(rows))
		for _, row := range rows {
			if row.pixels == best {
				selected = append(selected, row)
			}
		}
		return selected
	}
	return rows[:1]
}

// playVariant 是解析出的片源变体（对应源实现 variants 条目）。
type playVariant struct {
	url        string
	contentKey string
	codec      string
	rawCodec   string
	quality    string
	width      int
	height     int
	bitrate    int
}

// buildPlayInfo 把候选变体映射为 PlayInfo（url 取首个，backup_url 取次选）。
func buildPlayInfo(vid string, variants []playVariant) *PlayInfo {
	first := variants[0]
	info := &PlayInfo{
		Vid:        vid,
		URL:        first.url,
		Codec:      first.codec,
		Encrypted:  first.contentKey != "",
		Definition: first.quality,
		VideoMeta: &VideoMeta{
			Definition: first.quality,
			VWidth:     first.width,
			VHeight:    first.height,
			Bitrate:    first.bitrate,
			CodecType:  first.rawCodec,
		},
	}
	if first.contentKey != "" {
		info.ContentKey = first.contentKey
	}
	// 备用地址只接受与首选同密钥的变体：PlayInfo 只暴露一把内容密钥，
	// 同像素档的另一编码（密钥不同）不能放进 backup_url。
	if len(variants) > 1 && variants[1].url != first.url && variants[1].contentKey == first.contentKey {
		info.BackupURL = variants[1].url
	}
	return info
}

// playKeySeed 解析模型中的 key_seed（base64），缺失时返回 nil。
//
// 对齐 Node 的 Buffer.from(x, 'base64')：兼容 base64url 字母表、允许省略填充。
func playKeySeed(source map[string]any) ([]byte, error) {
	text := stringValue(source["key_seed"])
	if text == "" {
		return nil, nil
	}
	normalized := strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimRight(text, "="))
	seed, err := base64.RawStdEncoding.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("App 片源密钥种子格式无效")
	}
	return seed, nil
}

// playSize 取候选条目中最先可确认的分辨率。
func playSize(item, meta map[string]any) (int, int) {
	pairs := [4][2]any{
		{item["vwidth"], item["vheight"]},
		{item["width"], item["height"]},
		{meta["vwidth"], meta["vheight"]},
		{meta["width"], meta["height"]},
	}
	for _, pair := range pairs {
		width, widthOK := playNumber(pair[0])
		height, heightOK := playNumber(pair[1])
		if widthOK && heightOK && width > 0 && height > 0 {
			return int(width), int(height)
		}
	}
	return 0, 0
}

// cleanPlayURL 清理响应中可能出现的转义地址（对齐源实现 cleanUrl）。
func cleanPlayURL(value string) string {
	replaced := strings.ReplaceAll(value, `\/`, "/")
	replaced = strings.ReplaceAll(replaced, `\u0026`, "&")
	return strings.ReplaceAll(replaced, "&amp;", "&")
}

// checkedPlayURL 校验片源地址：https、无凭证、端口为空或 443、主机命中白名单。
func checkedPlayURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("App 片源地址格式无效")
	}
	if parsed.Scheme != "https" || parsed.User != nil || (parsed.Port() != "" && parsed.Port() != "443") {
		return "", errUnsupportedSource
	}
	host := strings.ToLower(parsed.Hostname())
	for _, domain := range playSourceDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return parsed.String(), nil
		}
	}
	return "", errUnsupportedSource
}

// parseFallback 解析 fallback_api（可能是字符串、数组或对象）。
//
// 字符串形式会先按 JSON 尝试解析出 fallback_api 字段，失败时原文返回。
func parseFallback(value any) string {
	switch v := value.(type) {
	case string:
		text := strings.TrimSpace(v)
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text), &parsed); err == nil {
			if api := playText(parsed["fallback_api"]); api != "" {
				return api
			}
		}
		return text
	case []any:
		if len(v) > 0 {
			return playText(v[0])
		}
	case map[string]any:
		return playText(v["fallback_api"])
	}
	return ""
}

// encryptDisabled 判断 encrypt_info 是否显式声明「未加密」。
//
// 对齐源实现的 [false, 0, '0', 'false'].includes(encrypt)：其余值（含缺失）
// 都视为已声明加密，没有 spade_a 时即为无效响应。
func encryptDisabled(encryptInfo any) bool {
	info, ok := encryptInfo.(map[string]any)
	if !ok {
		return false
	}
	switch value := info["encrypt"].(type) {
	case bool:
		return !value
	case float64:
		return value == 0
	case int:
		return value == 0
	case json.Number:
		n, err := value.Int64()
		return err == nil && n == 0
	case string:
		text := strings.TrimSpace(value)
		return text == "0" || text == "false"
	}
	return false
}

// playLeadingInt 取文本中的第一段数字（对齐源实现的 Number(quality.match(/\d+/)?.[0] || 0)）。
func playLeadingInt(text string) int {
	digits := playDigitPattern.FindString(text)
	if digits == "" {
		return 0
	}
	value, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return value
}

// firstText 返回首个非空文本。
func firstText(values ...any) string {
	for _, value := range values {
		if text := playText(value); text != "" {
			return text
		}
	}
	return ""
}

// playText 对齐源实现的 str()：字符串去空白，数字转十进制文本，其余为空串。
func playText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	case int:
		return strconv.Itoa(v)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

// stringValue 只接受字符串（源实现对地址候选要求 typeof === 'string'）。
func stringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

// playNumber 对齐源实现的 Number()：可解析为数值时返回。
func playNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		if number, err := v.Float64(); err == nil {
			return number, true
		}
	case string:
		if number, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return number, true
		}
	}
	return 0, false
}

// gzipBytes 返回输入经 gzip 压缩后的字节。
func gzipBytes(payload []byte) []byte {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, _ = writer.Write(payload)
	_ = writer.Close()
	return buf.Bytes()
}

// playbackUserAgent 按机型档案构造取址请求的 UA（版本号必须与 version_code 一致）。
func playbackUserAgent(model, osVersion, build string) string {
	return "com.phoenix.read/" + playbackVersionCode + " (Linux; U; Android " + osVersion + "; zh_CN; " +
		model + "; Build/" + build + "; Cronet/TTNetVersion:8d40f833 2026-03-03 QuicVersion:21ac1950 2025-11-18)"
}
