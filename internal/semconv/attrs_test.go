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
