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

// TestClassifyIPWithPort covers #198: the default node-metrics identity is
// formatted "host:port" (see internal/collector/nodemetrics), so an IP address
// followed by a port must still be classified by its address portion rather
// than falling through to ipNotIP (which mis-routes it to the hostnames
// category and leaks a disabled IP category).
func TestClassifyIPWithPort(t *testing.T) {
	cases := []struct {
		in   string
		want ipClass
	}{
		{"100.64.0.1:5252", ipTailscale}, // CGNAT + port, the default node-metrics identity
		{"10.0.0.5:8080", ipInternal},    // RFC1918 + port
		{"172.16.4.4:8080", ipInternal},
		{"192.168.1.1:8080", ipInternal},
		{"8.8.8.8:53", ipExternal},                // external + port
		{"[fd7a:115c:a1e0::1]:5252", ipTailscale}, // bracketed Tailscale IPv6 + port
		{"[fc00::1]:8080", ipInternal},            // bracketed ULA IPv6 + port
		{"[2606:4700::1111]:443", ipExternal},     // bracketed external IPv6 + port
		{"[::1]:8080", ipInternal},                // bracketed loopback IPv6 + port
		{"myhost:5252", ipNotIP},                  // genuine hostname:port must still fall back
		{"host.example.com:443", ipNotIP},
		{"not-an-ip:port", ipNotIP},
		{":5252", ipNotIP}, // no host at all
	}
	for _, c := range cases {
		if got := classifyIP(c.in); got != c.want {
			t.Errorf("classifyIP(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
