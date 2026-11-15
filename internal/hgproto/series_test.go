package hgproto

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"hgplayer/internal/sign"
)

// seriesRouterPayload 构造一段官网 _ROUTER_DATA。
func seriesRouterPayload(seriesID, title string, vids []any) map[string]any {
	return map[string]any{
		"loaderData": map[string]any{
			"player_(series_id)/page": map[string]any{
				"isSuccess": true,
				"series_id": seriesID,
				"seriesDetail": map[string]any{
					"series_id":    seriesID,
					"series_name":  title,
					"series_intro": "简介",
					"vid_list":     vids,
				},
			},
		},
	}
}

// seriesPageHTML 把 _ROUTER_DATA 包进一段官网风格的 HTML。
func seriesPageHTML(t *testing.T, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("序列化测试数据失败: %v", err)
	}
	return `<!doctype html><html><body><script>window._ROUTER_DATA = ` + string(raw) + `;window.__AFTER__ = 1;</script></body></html>`
}

// TestParseSeriesPageFromRealPage 用真实抓取的官网剧集页校验整条解析链路。
func TestParseSeriesPageFromRealPage(t *testing.T) {
	html, err := os.ReadFile("testdata/series_page.html")
	if err != nil {
		t.Fatalf("读取抓包样本失败: %v", err)
	}

	const seriesID = "7706512650816982040"
	detail, err := parseSeriesPage(html, seriesID)
	if err != nil {
		t.Fatalf("parseSeriesPage: %v", err)
	}
	if detail.SeriesID != seriesID {
		t.Fatalf("series_id = %s", detail.SeriesID)
	}
	if detail.SeriesTitle != "少夫人来自东北3" {
		t.Fatalf("series_title = %q", detail.SeriesTitle)
	}
	if detail.Cover != "https://p3-novel.byteimg.com/novel-pic/899ba52ee34a534d9be948a093468ed1~tplv-shrink:640:0.image" {
		t.Fatalf("cover = %q", detail.Cover)
	}
	if detail.Description == "" {
		t.Fatal("简介不应为空")
	}
	if len(detail.Episodes) != 96 {
		t.Fatalf("episodes = %d, want 96", len(detail.Episodes))
	}
	if detail.Episodes[0].Vid != "7706514645208534041" || detail.Episodes[0].EpisodeIndex != 1 {
		t.Fatalf("episodes[0] = %+v", detail.Episodes[0])
	}
	if last := detail.Episodes[95]; last.EpisodeIndex != 96 {
		t.Fatalf("episodes[95] = %+v", last)
	}
}

// TestSeriesFallsBackToDetailPage player 页不可用时改用 detail 页。
func TestSeriesFallsBackToDetailPage(t *testing.T) {
	payload := seriesRouterPayload("12345", "剧集A", []any{"111", "222"})
	page := seriesPageHTML(t, payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/player/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(sign.SignerFunc(func(sign.Signature) sign.Headers {
		return sign.Headers{"X-Test": "1"}
	}), DefaultDevice("1", "2", "3"), WithGuard(nil))
	c.siteOrigin = srv.URL

	detail, err := c.Series(context.Background(), "12345", nil, false)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(detail.Episodes) != 2 {
		t.Fatalf("episodes = %d, want 2", len(detail.Episodes))
	}
}

func TestParseSeriesPageRejectsBadData(t *testing.T) {
	valid := []any{"111", "222"}
	cases := []struct {
		name     string
		payload  map[string]any
		seriesID string
	}{
		{"剧集不一致", seriesRouterPayload("999", "剧集A", valid), "12345"},
		{"分集列表为空", seriesRouterPayload("12345", "剧集A", []any{}), "12345"},
		{"分集编号异常", seriesRouterPayload("12345", "剧集A", []any{"abc"}), "12345"},
		{"分集重复", seriesRouterPayload("12345", "剧集A", []any{"111", "111"}), "12345"},
		{"缺少 loaderData", map[string]any{"errors": map[string]any{}}, "12345"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := []byte(seriesPageHTML(t, tc.payload))
			if _, err := parseSeriesPage(html, tc.seriesID); err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

func TestReadRouterDataErrors(t *testing.T) {
	if _, err := readRouterData([]byte("<html><body>无数据</body></html>")); err == nil {
		t.Fatal("缺少 _ROUTER_DATA 时应报错")
	}
	// 截断的对象不能当成完整数据。
	if _, err := readRouterData([]byte(`<script>window._ROUTER_DATA = {"loaderData":{"a":{"b":1}`)); err == nil {
		t.Fatal("数据不完整时应报错")
	}
}

// TestNormalizeCover 覆盖 HEIC 变体转换与非法地址置空。
func TestNormalizeCover(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			"带签名的 HEIC 变体转为 JPEG",
			"https://p3-reading-sign.fqnovelpic.com/novel-pic/899ba52ee34a534d9be948a093468ed1~tplv-81nmtwyey9-superreso-aifit:400:0.heic?lk3s=64477e16&x-expires=1799940454&x-signature=fsWd4DNLz47Jldiz8ZIGgmxF3v8%3D",
			"https://p3-novel.byteimg.com/novel-pic/899ba52ee34a534d9be948a093468ed1~tplv-shrink:640:0.image",
		},
		{
			"无查询串的 HEIC 变体",
			"https://p3-novel.byteimg.com/novel-pic/abc~tplv-x.heic",
			"https://p3-novel.byteimg.com/novel-pic/abc~tplv-shrink:640:0.image",
		},
		{
			"JPEG 地址原样保留",
			"https://p3-novel.byteimg.com/novel-pic/abc~tplv-shrink:640:0.image",
			"https://p3-novel.byteimg.com/novel-pic/abc~tplv-shrink:640:0.image",
		},
		{
			"站外 HEIC 置空",
			"https://evil.example.com/novel-pic/abc~tplv-x.heic",
			"",
		},
		{
			"非 http 地址置空",
			"ftp://p3-novel.byteimg.com/novel-pic/abc~tplv-x.heic",
			"",
		},
		{"空地址", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCover(tc.input); got != tc.want {
				t.Fatalf("normalizeCover(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
