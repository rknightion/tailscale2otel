package listenaddr

import "testing"

func TestCanonical(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		// Wildcard spellings all mean "every interface" to net.Listen, so they
		// collapse to one form: two listeners spelled differently still collide.
		{":9091", "*:9091"},
		{"0.0.0.0:9091", "*:9091"},
		{"[::]:9091", "*:9091"},

		// IP literals normalize through net.IP so cosmetic differences do not
		// hide a collision.
		{"127.0.0.1:9091", "127.0.0.1:9091"},
		// Brackets are kept: an IPv6 canonical form must still be splittable,
		// or "::1:9091" is just ambiguous text.
		{"[::1]:9091", "[::1]:9091"},
		{"[0:0:0:0:0:0:0:1]:9091", "[::1]:9091"},

		// Hostnames are lowercased but never resolved: a DNS answer is not a
		// stable identity, and resolution here would make validation depend on
		// the resolver.
		{"LocalHost:9091", "localhost:9091"},
		{"example.com:9091", "example.com:9091"},

		// Port 0 is "any free port" and is a legitimate address.
		{"127.0.0.1:0", "127.0.0.1:0"},
	}
	for _, tc := range tests {
		got, err := Canonical(tc.addr)
		if err != nil {
			t.Errorf("Canonical(%q) returned error: %v", tc.addr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Canonical(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestCanonicalRejectsUnusableAddresses(t *testing.T) {
	// Every one of these is accepted by the current raw-string handling and
	// then fails inside net.Listen, on a goroutine, after startup (#306).
	tests := []struct {
		addr string
		why  string
	}{
		{"", "empty"},
		{"9091", "bare port with no colon: net.Listen wants host:port"},
		{"127.0.0.1", "host with no port"},
		{"127.0.0.1:9091:extra", "too many colons"},
		{"127.0.0.1:http", "service names are not resolved here"},
		{"127.0.0.1:-1", "negative port"},
		{"127.0.0.1:65536", "port above the 16-bit range"},
		{"127.0.0.1:99999", "port far above the 16-bit range"},
	}
	for _, tc := range tests {
		if got, err := Canonical(tc.addr); err == nil {
			t.Errorf("Canonical(%q) = %q, want an error (%s)", tc.addr, got, tc.why)
		}
	}
}

func TestCanonicalCollides(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Identical after canonicalization.
		{":9091", "0.0.0.0:9091", true},
		{":9091", "[::]:9091", true},

		// A wildcard bind owns the port on every interface, so a specific
		// address on the same port cannot also bind it.
		{":9091", "127.0.0.1:9091", true},
		{"0.0.0.0:2112", "192.168.1.5:2112", true},

		// Different ports never collide, whatever the hosts.
		{":9091", ":2112", false},
		{"0.0.0.0:9091", "127.0.0.1:2112", false},

		// Two specific addresses on one port are distinct sockets.
		{"127.0.0.1:9091", "192.168.1.5:9091", false},

		// Port 0 is a fresh ephemeral port each time: never a collision, not
		// even with itself.
		{"127.0.0.1:0", "127.0.0.1:0", false},
		{":0", "0.0.0.0:0", false},
	}
	for _, tc := range tests {
		if got := Collides(tc.a, tc.b); got != tc.want {
			t.Errorf("Collides(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
