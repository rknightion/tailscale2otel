package listenaddr

import "testing"

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		// Loopback-only binds: unreachable from another host.
		{"127.0.0.1:9091", true},
		{"127.0.0.1:0", true},
		{"127.0.0.53:80", true},
		{"[::1]:9091", true},
		{"localhost:9091", true},
		{"LocalHost:9091", true},

		// Wildcard binds: every interface, so network-reachable.
		{":9091", false},
		{"0.0.0.0:9091", false},
		{"[::]:9091", false},

		// Specific but routable interfaces.
		{"192.168.1.5:9091", false},
		{"10.0.0.7:2112", false},
		// A tailnet address is reachable by every peer on the tailnet — that is
		// precisely the #227 threat model, so it must NOT count as loopback.
		{"100.64.0.1:9091", false},

		// Fail closed on anything we cannot positively prove is loopback.
		{"", false},
		{"garbage", false},
		{"9091", false},
		{"example.com:9091", false},
		{"[::1]", false},
	}
	for _, tc := range tests {
		if got := IsLoopback(tc.addr); got != tc.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
