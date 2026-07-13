package config_test

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

func TestDurationUnmarshalYAML(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"5m", 5 * time.Minute},
		{"168h", 168 * time.Hour},
		{"500ms", 500 * time.Millisecond},
		{"1h30m", 90 * time.Minute},
	}
	for _, tc := range cases {
		var d config.Duration
		if err := yaml.Unmarshal([]byte(tc.in), &d); err != nil {
			t.Fatalf("unmarshal %q: %v", tc.in, err)
		}
		if d.D() != tc.want {
			t.Fatalf("%q: D() = %v, want %v", tc.in, d.D(), tc.want)
		}
	}
}

func TestDurationUnmarshalInvalid(t *testing.T) {
	var d config.Duration
	if err := yaml.Unmarshal([]byte("notaduration"), &d); err == nil {
		t.Fatalf("expected error for invalid duration, got nil")
	}
}
