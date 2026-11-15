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
// 移植自 hongguo-open-player/src/server/native/app-source.js（GPL-3.0-only）的
// playbackRequest / shortplayRequest / parseFallback / parseEntryPlayInfo / fetchAppPlayUrl。
// 取址失败时按「主接口 → 备用接口 → 短剧接口」逐级兜底，
// HTTP 200 但响应体为空同样视为失败（对齐源实现 fetchAppPlayUrl）。
//
// 已知限制：本机对真实 App 取址端点（reading.snssdk.com 等）返回 HTTP 200 + 0 字节，
// 参考实现同样如此，因此这条链路无法做真实端到端验证；只能逐行对齐源实现，
// 并用本地 httptest 固定住请求形状、兜底顺序与解析规则。
//
// 未移植的源实现能力：entryCache（分集条目缓存）、nextVids 批量取址、清晰度选项
// （requestedQuality 固定为 auto，allowLowerQuality 为 false）、ByteVC2 转码
// （allowBytevc2 固定为 false）、12s 截止时间（由 ctx 与上游客户端超时承担）。
func (c *Client) Play(ctx context.Context, vid string) (*PlayInfo, error) {
	if !numericID.MatchString(vid) {
		return nil, inputErrf("分集编号无效")
	}

	response, err := c.fetchPlayResponse(ctx, vid)
	if err != nil {
		return nil, err
	}
	if codeOf(response) != 0 {
		return nil, fmt.Errorf("App 接口暂未提供本集播放信息")
	}

	data, _ := response["data"].(map[string]any)
	entry, _ := data[vid].(map[string]any)
	if entry == nil {
		return nil, fmt.Errorf("App 接口未返回所选分集，已停止取址")
	}
	return c.parseEntryPlayInfo(ctx, vid, entry)
}

// fetchPlayResponse 按 主接口 → 备用接口 → 短剧接口 的顺序取址。
//
// 源实现仅在主接口返回空响应（EMPTY_RESPONSE）时尝试备用接口，
// 其他网络错误直接跳到短剧接口；这里统一成逐级兜底，语义等价且更简单。
func (c *Client) fetchPlayResponse(ctx context.Context, vid string) (map[string]any, error) {
	payload := buildPlaybackPayload(vid)

	var firstErr error
	for _, endpoint := range []string{c.videoAPI, c.videoAPIFallback} {
		if endpoint == "" {
			continue
		}
		response, err := c.playbackRequest(ctx, endpoint, payload)
		if err == nil {
			return response, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	// 终极降级：番茄小说客户端短剧接口（aid=1967），沿用同一设备身份。
	shortErr := errors.New("短剧接口未返回所选分集")
	if short, err := c.shortplayRequest(ctx, vid); err != nil {
		shortErr = err
	} else if entry := shortplayEntry(short, vid); entry != nil {
		return map[string]any{"code": 0, "data": map[string]any{vid: entry}}, nil
	}

	if firstErr == nil {
		firstErr = errors.New("主接口未返回可用响应")
	}
	return nil, fmt.Errorf("App 取址不可用：主接口 %v；短剧接口 %v", firstErr, shortErr)
}

// playbackRequest 请求播放取址接口并解析 JSON 响应。
func (c *Client) playbackRequest(ctx context.Context, endpoint string, payload []byte) (map[string]any, error) {
	target, headers, body := c.playbackSigned(endpoint, payload)
	return c.doJSONRaw(ctx, http.MethodPost, target, body, headers)
}

// playbackSigned 构造播放取址请求：URL、请求头与 gzip 压缩后的请求体。
//
// 头部必须在此自行组装并经 c.upRaw 发出：upstream.Client.Do 注入签名时，
// sign.PlayerSigner 会用 url.Values.Encode 重写 query（改为字典序、空格编码为 '+'），
// 而播放签名对 query 字节级敏感，被改写后的签名输入与源实现
// （app-source.js playbackRequest + argus.js PlayerSigner.headers）不再一致。
// 这里把原始 query 直接交给签名器，且 DeviceID/IID 留空（query 中已含设备身份），
// 保证签名作用于实际发送的 gzip 字节与 query。
func (c *Client) playbackSigned(endpoint string, payload []byte) (string, map[string]string, []byte) {
	body := gzipBytes(payload)
	ticket := strconv.FormatInt(time.Now().UnixMilli(), 10)

	pairs := append(c.playbackDeviceOrdered(), kv{"_rticket", ticket})
	query := encodeOrderedQuery(pairs)
	target := endpoint + "?" + query

	headers := map[string]string{
		"User-Agent":           playbackUA,
		"Accept":               "application/json; charset=utf-8,application/x-protobuf",
		"Content-Type":         "application/json; charset=UTF-8",
		"Content-Encoding":     "gzip",
		"sdk-version":          "2",
		"passport-sdk-version": "5051452",
		"x-xs-from-web":        "0",
		"x-ss-dp":              "8662",
	}
	if c.device.IID != "" {
		headers["Cookie"] = "store-region=cn-bj; store-region-src=did; install_id=" + c.device.IID
	}
	for key, value := range c.signPlayback(target, query, body) {
		headers[key] = value
	}
	return target, headers, body
}

// signPlayback 用播放器签名器为 query 与实际发送的 body 生成鉴权头。
func (c *Client) signPlayback(target, query string, body []byte) map[string]string {
	if c.signer == nil {
		return nil
	}
	return c.signer.Sign(sign.Signature{
		Method: http.MethodPost,
		URL:    target,
		Query:  query,
		Body:   body,
	})
}

// playbackDeviceOrdered 返回播放请求的设备参数（插入序，对齐源实现的 PLAYBACK_DEVICE）。
func (c *Client) playbackDeviceOrdered() []kv {
	return []kv{
		{"ac", "wifi"},
		{"channel", "update_64"},
		{"aid", "8662"},
		{"app_name", "novelread"},
		{"version_code", orDefault(c.device.VersionCode, "74332")},
		{"version_name", orDefault(c.device.VersionName, "7.4.3.32")},
		{"device_platform", "android"},
		{"os", "android"},
		{"ssmix", "a"},
		{"device_type", "25053RT47C"},
		{"device_brand", "Redmi"},
		{"language", "zh"},
		{"os_api", "36"},
		{"os_version", "16"},
		{"manifest_version_code", orDefault(c.device.ManifestVersion, "74332")},
		{"resolution", "1280*2772"},
		{"dpi", "520"},
		{"update_version_code", orDefault(c.device.UpdateVersion, "74332")},
		{"host_abi", "arm64-v8a"},
		{"dragon_device_type", "phone"},
		{"pv_player", orDefault(c.device.PVPlayer, "74332")},
		{"compliance_status", "0"},
		{"need_personal_recommend", "1"},
		{"player_so_load", "1"},
		{"is_android_pad_screen", "0"},
		{"device_id", c.device.DeviceID},
		{"iid", c.device.IID},
		{"cdid", c.device.CDID},
	}
}

// orDefault 在值为空时回退到默认值。
func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// playbackPayload 是播放取址请求体。
//
// 字段顺序与源实现一致：请求体参与签名，字节顺序敏感。
type playbackPayload struct {
	DrScene         string              `json:"dr_scene"`
	BizParam        playbackBizParam    `json:"biz_param"`
	MixedVideoIDMap map[string][]string `json:"mixed_video_id_map"`
}

// playbackBizParam 是播放取址请求的业务参数。
type playbackBizParam struct {
	DetailPageVersion      int  `json:"detail_page_version"`
	DeviceLevel            int  `json:"device_level"`
	DisableDiggStat        bool `json:"disable_digg_stat"`
	NeedAllVideoDefinition bool `json:"need_all_video_definition"`
	NeedMP4Align           bool `json:"need_mp4_align"`
	UseOSPlayer            bool `json:"use_os_player"`
	UseServerDNS           bool `json:"use_server_dns"`
	VideoPlatform          int  `json:"video_platform"`
}

// buildPlaybackPayload 构造播放取址请求体。
func buildPlaybackPayload(vid string) []byte {
	body, _ := json.Marshal(playbackPayload{
		DrScene: "player",
		BizParam: playbackBizParam{
			DetailPageVersion:      0,
			DeviceLevel:            3,
			DisableDiggStat:        false,
			NeedAllVideoDefinition: true,
			NeedMP4Align:           false,
			UseOSPlayer:            false,
			UseServerDNS:           false,
			VideoPlatform:          1024,
		},
		MixedVideoIDMap: map[string][]string{"1004": {vid}},
	})
	return body
}

// shortplayRequest 请求番茄小说客户端短剧接口（aid=1967）。
//
// 与源实现一致：body 不压缩，条目不按设备参数展开。
func (c *Client) shortplayRequest(ctx context.Context, vid string) (map[string]any, error) {
	pairs := []kv{
		{"iid", c.device.IID},
		{"device_id", c.device.DeviceID},
		{"aid", "1967"},
		{"app_name", "novelapp"},
		{"version_code", "72132"},
		{"version_name", "7.2.1.32"},
		{"device_platform", "android"},
		{"device_brand", "Xiaomi"},
		{"os_version", "13"},
		{"cdid", c.device.CDID},
	}
	query := encodeOrderedQuery(pairs)
	target := c.shortPlayAPI + "?" + query

	body, _ := json.Marshal(shortPlayPayload{
		VideoID:     vid,
		ContentType: 1004,
		BizParam: shortPlayBizParam{
			DetailPageVersion:      0,
			DeviceLevel:            3,
			DisableDiggStat:        false,
			DisableVideoRelateBook: false,
			FromVideoID:            "",
			NeedAllVideoDefinition: true,
			NeedMP4Align:           false,
			Source:                 4,
			UseOSPlayer:            false,
			UseServerDNS:           false,
			VideoPlatform:          3,
		},
	})

	headers := map[string]string{
		"User-Agent":   "com.dragon.read",
		"Content-Type": "application/json; charset=utf-8",
	}
	for key, value := range c.signPlayback(target, query, body) {
		headers[key] = value
	}
	return c.doJSONRaw(ctx, http.MethodPost, target, body, headers)
}

// shortPlayPayload 是短剧接口请求体（字段顺序与源实现一致）。
type shortPlayPayload struct {
	VideoID     string            `json:"video_id"`
	ContentType int               `json:"content_type"`
	BizParam    shortPlayBizParam `json:"biz_param"`
}

// shortPlayBizParam 是短剧接口的业务参数。
type shortPlayBizParam struct {
	DetailPageVersion      int    `json:"detail_page_version"`
	DeviceLevel            int    `json:"device_level"`
	DisableDiggStat        bool   `json:"disable_digg_stat"`
	DisableVideoRelateBook bool   `json:"disable_video_relate_book"`
	FromVideoID            string `json:"from_video_id"`
	NeedAllVideoDefinition bool   `json:"need_all_video_definition"`
	NeedMP4Align           bool   `json:"need_mp4_align"`
	Source                 int    `json:"source"`
	UseOSPlayer            bool   `json:"use_os_player"`
	UseServerDNS           bool   `json:"use_server_dns"`
	VideoPlatform          int    `json:"video_platform"`
}

// shortplayEntry 把短剧接口响应归一化成 data[vid] 形式的条目。
func shortplayEntry(response map[string]any, vid string) map[string]any {
	data, _ := response["data"].(map[string]any)
	if data == nil {
		return nil
	}
	if entry, ok := data[vid].(map[string]any); ok && entry != nil {
		return entry
	}
	// 短剧接口直接返回 video_model 时，data 本身就是条目。
	if _, ok := data["video_model"]; ok {
		return data
	}
	return nil
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
