package media

import "testing"

func TestParseRange(t *testing.T) {
	cases := []struct {
		name   string
		header string
		total  int64
		want   Range
	}{
		{"含两端闭区间", "bytes=0-1023", 10000, Range{Start: 0, End: 1023, Valid: true}},
		{"中间区间", "bytes=500-999", 10000, Range{Start: 500, End: 999, Valid: true}},
		{"开区间到末尾", "bytes=5000-", 10000, Range{Start: 5000, End: 9999, Valid: true}},
		{"后缀长度", "bytes=-500", 10000, Range{Start: 9500, End: 9999, Valid: true}},
		{"后缀长度超过总数", "bytes=-999999", 10000, Range{Start: 0, End: 9999, Valid: true}},
		{"起点等于总数", "bytes=10000-", 10000, Range{Start: 10000, End: 10000, Valid: true}},
		{"无前缀", "0-1023", 10000, Range{Valid: false}},
		{"空串", "", 10000, Range{Valid: false}},
		{"多区间", "bytes=0-100,200-300", 10000, Range{Valid: false}},
		{"end 小于 start", "bytes=500-400", 10000, Range{Valid: false}},
		{"end 超出总数", "bytes=500-99999", 10000, Range{Start: 500, End: 9999, Valid: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRange(tc.header, tc.total)
			if got.Valid != tc.want.Valid {
				t.Fatalf("Valid = %v, want %v", got.Valid, tc.want.Valid)
			}
			if !got.Valid {
				return
			}
			if got.Start != tc.want.Start {
				t.Errorf("Start = %d, want %d", got.Start, tc.want.Start)
			}
			if got.End != tc.want.End {
				t.Errorf("End = %d, want %d", got.End, tc.want.End)
			}
		})
	}
}

func TestContentRange(t *testing.T) {
	got := ContentRange(0, 1023, 10000)
	want := "bytes 0-1023/10000"
	if got != want {
		t.Fatalf("ContentRange = %q, want %q", got, want)
	}
}
