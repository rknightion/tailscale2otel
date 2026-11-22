package app

import (
	"slices"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
)

func TestHeadscaleInactiveCollectorsCoverUnsupportedFeatures(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "headscale"
	inactive := cfg.ProviderInactiveCollectors()
	p := provider.Headscale(nil)
	for _, feature := range provider.AllFeatures {
		if !p.Supports(feature) && !slices.Contains(inactive, feature) {
			t.Errorf("unsupported feature %q missing from inactive collectors %v", feature, inactive)
		}
	}
}
