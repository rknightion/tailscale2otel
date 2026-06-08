package provider

import (
	"sort"
	"testing"
)

func TestTailscaleSupportsEverything(t *testing.T) {
	p := Tailscale(nil) // Client nil is fine; capability set is independent of it.
	for _, f := range AllFeatures {
		if !p.Supports(f) {
			t.Errorf("tailscale provider should support %q", f)
		}
	}
	if p.Kind != KindTailscale {
		t.Errorf("Kind = %q, want %q", p.Kind, KindTailscale)
	}
	if len(p.Capabilities()) != len(AllFeatures) {
		t.Errorf("Capabilities len = %d, want %d", len(p.Capabilities()), len(AllFeatures))
	}
}

func TestHeadscaleSupportsSubset(t *testing.T) {
	p := Headscale(nil)
	want := map[string]bool{"devices": true, "users": true, "keys": true, "acl": true, "nodemetrics": true}
	for _, f := range AllFeatures {
		if got := p.Supports(f); got != want[f] {
			t.Errorf("headscale Supports(%q) = %v, want %v", f, got, want[f])
		}
	}
	if p.Kind != KindHeadscale {
		t.Errorf("Kind = %q, want %q", p.Kind, KindHeadscale)
	}
	caps := p.Capabilities()
	if !sort.StringsAreSorted(caps) {
		t.Errorf("Capabilities() not sorted: %v", caps)
	}
	if len(caps) != 5 {
		t.Errorf("headscale Capabilities len = %d, want 5: %v", len(caps), caps)
	}
}

func TestSupportsUnknownFeature(t *testing.T) {
	if Headscale(nil).Supports("does-not-exist") {
		t.Error("unknown feature should be unsupported")
	}
}
