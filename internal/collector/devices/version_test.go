package devices_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/devices"
)

func TestNormalizeVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.98.4-t01c6b9661", "1.98.4"},
		{"1.80.3-1 (OpenWrt)", "1.80.3"},
		{"1.96.4", "1.96.4"},
		{"v1.36.0", "1.36.0"},
		{"1.82.5-dev20251119-tcdd609053-dirty", "1.82.5"},
		{"1.90.9-t6e8a4f2de-g19196f361", "1.90.9"},
		{"", "unknown"},
		{"garbage", "unknown"},
		{"1.2", "unknown"}, // needs all three components
	}
	for _, c := range cases {
		if got := devices.NormalizeVersion(c.in); got != c.want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
