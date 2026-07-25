package tsscope_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/tsscope"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   tsscope.Class
	}{
		{"nil", nil, tsscope.ClassNone},
		{"empty", []string{}, tsscope.ClassNone},
		{"blank entries only", []string{"", "  "}, tsscope.ClassNone},
		{"all", []string{"all"}, tsscope.ClassAll},
		{"all read", []string{"all:read"}, tsscope.ClassAllRead},
		{"narrow read", []string{"devices:core:read"}, tsscope.ClassRead},
		{"narrow write", []string{"devices:core"}, tsscope.ClassWrite},
		{"two narrow reads", []string{"devices:core:read", "dns:read"}, tsscope.ClassRead},
		{"read plus write", []string{"devices:core:read", "auth_keys"}, tsscope.ClassWrite},
		{"write outranks all:read", []string{"all:read", "devices:core"}, tsscope.ClassWrite},
		{"all outranks everything", []string{"devices:core", "all"}, tsscope.ClassAll},
		{"all:read outranks narrow read", []string{"dns:read", "all:read"}, tsscope.ClassAllRead},
		{"unknown future read scope stays read", []string{"quantum_tunnels:read"}, tsscope.ClassRead},
		{"unknown future write scope stays write", []string{"quantum_tunnels"}, tsscope.ClassWrite},
		{"case and space insensitive", []string{" ALL:READ "}, tsscope.ClassAllRead},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tsscope.Classify(tc.scopes); got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.scopes, got, tc.want)
			}
		})
	}
}

func TestIsReadOnly(t *testing.T) {
	tests := []struct {
		scope string
		want  bool
	}{
		{"all:read", true},
		{"devices:core:read", true},
		{"logs:network:read", true},
		{"all", false},
		{"devices:core", false},
		{"auth_keys", false},
		{"", false},
		// "read" must be the LAST segment, not merely present anywhere.
		{"read:devices", false},
	}
	for _, tc := range tests {
		if got := tsscope.IsReadOnly(tc.scope); got != tc.want {
			t.Errorf("IsReadOnly(%q) = %v, want %v", tc.scope, got, tc.want)
		}
	}
}

func TestSatisfies(t *testing.T) {
	tests := []struct {
		name     string
		granted  []string
		required string
		want     bool
	}{
		{"all satisfies anything", []string{"all"}, "devices:core", true},
		{"all satisfies read", []string{"all"}, "dns:read", true},
		{"all:read satisfies a read scope", []string{"all:read"}, "dns:read", true},
		{"all:read does NOT satisfy a write scope", []string{"all:read"}, "devices:core", false},
		{"exact match", []string{"dns:read"}, "dns:read", true},
		{"write parent satisfies its read child", []string{"devices:core"}, "devices:core:read", true},
		{"read child does NOT satisfy its write parent", []string{"devices:core:read"}, "devices:core", false},
		{"unrelated scope", []string{"dns:read"}, "devices:core:read", false},
		{"empty grant satisfies nothing", nil, "dns:read", false},
		{"empty requirement is always satisfied", []string{"dns:read"}, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tsscope.Satisfies(tc.granted, tc.required); got != tc.want {
				t.Errorf("Satisfies(%q, %q) = %v, want %v", tc.granted, tc.required, got, tc.want)
			}
		})
	}
}
