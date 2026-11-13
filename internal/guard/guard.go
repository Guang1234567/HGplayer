// Package guard 提供上游请求的域名白名单与基础安全校验。
//
// 所有上游请求必须通过本包校验，确保只访问受信任的域名，
// 并对解析结果做基础防护。真实实现包含 IP 黑白名单、
// DoH 竞速解析等；此处先落地可测试的域名白名单骨架。
package guard

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// ErrBlocked 表示请求被安全策略拒绝。
var ErrBlocked = errors.New("请求地址未通过安全检查")

// Guard 是域名白名单守卫。
type Guard struct {
	// allowed 是允许访问的域名后缀集合（小写，含前导点表示子域匹配）。
	allowed map[string]struct{}
}

// New 创建守卫。allowedDomains 为允许的域名（精确匹配或子域匹配）。
func New(allowedDomains ...string) *Guard {
	g := &Guard{allowed: make(map[string]struct{}, len(allowedDomains))}
	for _, d := range allowedDomains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			g.allowed[d] = struct{}{}
		}
	}
	return g
}

// CheckURL 校验目标 URL 是否允许访问。
//
// 要求：必须是 https、无认证信息、主机名属于白名单。
func (g *Guard) CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrBlocked
	}
	if u.Scheme != "https" {
		return nil, ErrBlocked
	}
	if u.User != nil {
		return nil, ErrBlocked
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, ErrBlocked
	}
	// 禁止 IP 直连（上游域名应经 DNS 解析，IP 直连常用于 SSRF 绕过）。
	if net.ParseIP(host) != nil {
		return nil, ErrBlocked
	}
	if !g.hostAllowed(host) {
		return nil, ErrBlocked
	}
	return u, nil
}

// hostAllowed 判断主机是否命中白名单。
//
// 匹配规则：精确匹配，或域名以 "." + 白名单域名 结尾。
func (g *Guard) hostAllowed(host string) bool {
	if _, ok := g.allowed[host]; ok {
		return true
	}
	for d := range g.allowed {
		if strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// AllowedDomains 返回白名单副本。
func (g *Guard) AllowedDomains() []string {
	out := make([]string, 0, len(g.allowed))
	for d := range g.allowed {
		out = append(out, d)
	}
	return out
}
