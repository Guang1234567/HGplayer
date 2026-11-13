package guard

import "testing"

func TestCheckURL(t *testing.T) {
	g := New("fqnovel.com", "snssdk.com")

	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"白名单精确", "https://fqnovel.com/api", false},
		{"白名单子域", "https://log5-applog-lf.fqnovel.com/x", false},
		{"另一白名单", "https://is.snssdk.com/y", false},
		{"非 https", "http://fqnovel.com/api", true},
		{"非白名单域", "https://evil.example.com/api", true},
		{"含认证信息", "https://user:pass@fqnovel.com/api", true},
		{"IP 直连", "https://1.2.3.4/api", true},
		{"本地回环", "https://127.0.0.1/api", true},
		{"空主机", "https:///api", true},
		{"非白名单后缀", "https://notfqnovel.com/api", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := g.CheckURL(tc.url)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestAllowedDomains(t *testing.T) {
	g := New("a.com", "b.com")
	got := g.AllowedDomains()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}
