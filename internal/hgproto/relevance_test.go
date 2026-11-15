package hgproto

import "testing"

func TestMatchesSearchKeyword(t *testing.T) {
	cases := []struct {
		name    string
		keyword string
		fields  []any
		want    bool
	}{
		{"标题命中", "都市", []any{"都市爱情剧"}, true},
		{"标题未命中", "都市", []any{"古装剧"}, false},
		{"多词全中", "都市 甜宠", []any{"都市甜宠剧"}, true},
		{"多词缺一", "都市 玄幻", []any{"都市甜宠剧"}, false},
		{"标签命中", "战神", []any{"无关标题", []any{map[string]any{"name": "战神"}}}, true},
		{"实体还原", "a&b", []any{"a&amp;b"}, true},
		{"去 HTML 标签", "甜宠", []any{"<em>甜宠</em>剧"}, true},
		{"全角归一化", "ABC", []any{"ａｂｃ剧"}, true},
		{"大小写无关", "abc", []any{"ABC剧"}, true},
		{"忽略非字符串字段", "123", []any{123.0}, false},
		{"空关键词恒真", "   ", []any{"任意"}, true},
		{"空字段恒假", "都市", []any{""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesSearchKeyword(tc.keyword, tc.fields...); got != tc.want {
				t.Fatalf("matchesSearchKeyword(%q, %+v) = %v, want %v", tc.keyword, tc.fields, got, tc.want)
			}
		})
	}
}

// TestRelevanceTextShape 覆盖对象 / 数组 / 标量的取值规则。
func TestRelevanceTextShape(t *testing.T) {
	if got := relevanceText(map[string]any{"name": "剧名", "unrelated": "噪声"}); got != "剧名     " {
		t.Fatalf("对象取值 = %q", got)
	}
	if got := relevanceText([]any{"甲", map[string]any{"title": "乙"}}); got != "甲    乙  " {
		t.Fatalf("数组取值 = %q", got)
	}
	if got := relevanceText(42.0); got != "" {
		t.Fatalf("标量取值 = %q", got)
	}
}
