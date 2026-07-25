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

// TestClassifyIPNormalization covers #465 (security:SEC-02): a disabled IP
// category must not be bypassable by changing the address's TEXTUAL
// representation. An IPv4-mapped IPv6 address (`::ffff:100.64.0.1`, or its hex
// form `::ffff:6440:1`) is the same address as `100.64.0.1`, but netip's Prefix
// matching deliberately does not match a 4-in-6 address against an IPv4 prefix,
// so the value must be unmapped before range classification. Surrounding
// whitespace is likewise a representation difference, not a different address.
func TestClassifyIPNormalization(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want ipClass
	}{
		{"canonical ipv4 cgnat", "100.64.0.1", ipTailscale},
		{"canonical ipv6 tailscale", "fd7a:115c:a1e0::1", ipTailscale},
		{"mapped ipv4 cgnat", "::ffff:100.64.0.1", ipTailscale},
		{"mapped ipv4 cgnat hex form", "::ffff:6440:1", ipTailscale},
		{"mapped ipv4 rfc1918", "::ffff:10.0.0.5", ipInternal},
		{"mapped ipv4 loopback", "::ffff:127.0.0.1", ipInternal},
		{"mapped ipv4 link-local", "::ffff:169.254.1.1", ipInternal},
		{"mapped ipv4 external", "::ffff:8.8.8.8", ipExternal},
		{"mapped ipv4 cgnat with port", "[::ffff:100.64.0.1]:41641", ipTailscale},
		{"address port", "100.64.0.1:41641", ipTailscale},
		{"bracketed loopback with port", "[::1]:80", ipInternal},
		{"leading and trailing spaces", "  100.64.0.1  ", ipTailscale},
		{"tab and newline padding", "\t100.64.0.1\n", ipTailscale},
		{"padded address port", " 100.64.0.1:41641 ", ipTailscale},
		{"padded mapped address", " ::ffff:100.64.0.1 ", ipTailscale},
		{"malformed extra octet", "100.64.0.1.5", ipNotIP},
		{"malformed truncated", "100.64.0.", ipNotIP},
		{"malformed hex garbage", "fd7a:115c:zzzz::1", ipNotIP},
		{"sentinel external", "external", ipNotIP},
		{"whitespace only", "   ", ipNotIP},
		{"empty", "", ipNotIP},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyIP(c.in); got != c.want {
				t.Errorf("classifyIP(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
