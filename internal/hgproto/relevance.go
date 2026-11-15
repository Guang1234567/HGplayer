package hgproto

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// 上游在无匹配结果时会替换成热门剧目，因此只比对可搜索元数据，
// 绝不比对 ID、封面 URL 等无关字段（与源实现一致）。

// relevanceKeys 是参与匹配的对象字段。
var relevanceKeys = []string{"name", "nickname", "tag_name", "title", "text", "sub_title"}

var (
	relevanceTagPattern  = regexp.MustCompile(`<[^>]*>`)
	relevanceNbspPattern = regexp.MustCompile(`(?i)&nbsp;`)
	relevanceAmpPattern  = regexp.MustCompile(`(?i)&amp;`)
	relevanceQuotPattern = regexp.MustCompile(`(?i)&quot;`)
	relevanceAposPattern = regexp.MustCompile(`&#0?39;`)
	relevanceLtPattern   = regexp.MustCompile(`(?i)&lt;`)
	relevanceGtPattern   = regexp.MustCompile(`(?i)&gt;`)
)

// matchesSearchKeyword 判断可搜索元数据是否命中关键词的全部词项。
func matchesSearchKeyword(keyword string, fields ...any) bool {
	var terms []string
	for _, term := range strings.Fields(relevanceText(keyword)) {
		terms = append(terms, term)
	}
	if len(terms) == 0 {
		return true
	}

	var b strings.Builder
	for _, field := range fields {
		b.WriteString(relevanceText(field))
		b.WriteByte(' ')
	}
	metadata := b.String()

	for _, term := range terms {
		if !strings.Contains(metadata, term) {
			return false
		}
	}
	return true
}

// relevanceText 把任意响应字段渲染为可比较文本。
//
// 非字符串标量一律视为空串（与源实现一致），避免把数值字段纳入匹配。
func relevanceText(value any) string {
	switch v := value.(type) {
	case string:
		return normalizeRelevanceText(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, relevanceText(item))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		parts := make([]string, 0, len(relevanceKeys))
		for _, key := range relevanceKeys {
			parts = append(parts, relevanceText(v[key]))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// normalizeRelevanceText 去标签、还原实体、做 NFKC 归一化并转小写。
func normalizeRelevanceText(value string) string {
	s := relevanceTagPattern.ReplaceAllString(value, "")
	s = relevanceNbspPattern.ReplaceAllString(s, " ")
	s = relevanceAmpPattern.ReplaceAllString(s, "&")
	s = relevanceQuotPattern.ReplaceAllString(s, `"`)
	s = relevanceAposPattern.ReplaceAllString(s, "'")
	s = relevanceLtPattern.ReplaceAllString(s, "<")
	s = relevanceGtPattern.ReplaceAllString(s, ">")
	return strings.ToLower(norm.NFKC.String(s))
}
