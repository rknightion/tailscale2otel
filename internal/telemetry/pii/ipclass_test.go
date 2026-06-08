package pii

import "testing"

func TestClassifyIP(t *testing.T) {
	cases := []struct {
		in   string
		want ipClass
	}{
		{"100.64.1.2", ipTailscale},        // CGNAT
		{"100.127.255.254", ipTailscale},   // CGNAT upper
		{"fd7a:115c:a1e0::1", ipTailscale}, // Tailscale ULA
		{"10.0.0.5", ipInternal},
		{"172.16.4.4", ipInternal},
		{"192.168.1.1", ipInternal},
		{"169.254.1.1", ipInternal}, // link-local
		{"fe80::1", ipInternal},     // link-local v6
		{"fc00::1", ipInternal},     // ULA (non-tailscale)
		{"127.0.0.1", ipInternal},   // loopback
		{"::1", ipInternal},         // loopback v6
		{"8.8.8.8", ipExternal},
		{"1.1.1.1", ipExternal},
		{"2606:4700::1111", ipExternal},
		{"not-an-ip", ipNotIP},
		{"host.example.com", ipNotIP},
		{"", ipNotIP},
	}
	for _, c := range cases {
		if got := classifyIP(c.in); got != c.want {
			t.Errorf("classifyIP(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
