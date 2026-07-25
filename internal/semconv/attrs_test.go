package semconv

import "testing"

func TestAttrProviderConstant(t *testing.T) {
	if AttrProvider != "tailscale2otel.provider" {
		t.Errorf("AttrProvider = %q, want %q", AttrProvider, "tailscale2otel.provider")
	}
	if AttrTailnet != "tailscale.tailnet" {
		t.Errorf("AttrTailnet = %q, want %q", AttrTailnet, "tailscale.tailnet")
	}
}

// TestAttrErrorTypeConstant pins the wire key of the bounded error class. It is
// emitted on the collector scrape span AND on tailscale2otel.scrape.errors, and
// is rendered into docs/metrics.md, so changing it is a breaking rename of a
// user-visible label rather than an internal refactor.
func TestAttrErrorTypeConstant(t *testing.T) {
	if AttrErrorType != "error.type" {
		t.Errorf("AttrErrorType = %q, want %q", AttrErrorType, "error.type")
	}
}
