package hgproto

import (
	"testing"

	"hgplayer/internal/guard"
)

// 白名单必须覆盖协议用到的全部端点域名，否则请求会被 guard 拦截。
func TestAllowedDomainsCoverEndpoints(t *testing.T) {
	g := guard.New(AllowedDomains...)

	urls := []string{
		// 目录 / 详情走官网域名。
		"https://hongguoduanju.com/reading/distribution/category/landpage/v/?aid=8662",
		"https://hongguoduanju.com/reading/bookapi/bookinfo/v/?series_id=1",
		// 取址 / 搜索走字节域名。
		"https://reading.snssdk.com/novel/player/multi_video_model/player/v1?vid=v1",
		"https://api5-normal-sinfonlinea.fqnovel.com/reading/bookapi/search/tab/v?query=x",
	}
	for _, u := range urls {
		if _, err := g.CheckURL(u); err != nil {
			t.Errorf("guard 拦截了合法端点 %s: %v", u, err)
		}
	}
}
