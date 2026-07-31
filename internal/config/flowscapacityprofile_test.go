package config_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
)

// flows.capacity_profile only tunes internal/flowstore's in-memory ring
// (flowstore.WithCapacityProfile is passed only when building that backend —
// see internal/app/admin_flows.go). Once flows.store.directory selects the
// persistent sqlite backend instead, WithCapacityProfile is never called at
// all: the profile is silently ignored, and the status page reports
// capacity_profile: "sqlite" rather than the configured value. This is the
// same bug class as cardinality.flow.source_port under metrics_mode: rollup
// (a setting that looks active but is never actually applied) — Warnings()
// must flag it rather than let an operator discover it by reading source.
func TestWarnings_FlowsCapacityProfileIgnoredWithStore(t *testing.T) {
	tests := []struct {
		name      string
		profile   string
		directory string
		wantWarn  bool
	}{
		{
			name:      "non-default profile with store directory set",
			profile:   flowstore.ProfileCompact,
			directory: "/var/lib/tailscale2otel/flows",
			wantWarn:  true,
		},
		{
			name:      "non-default profile with store directory empty",
			profile:   flowstore.ProfileExpanded,
			directory: "",
			wantWarn:  false,
		},
		{
			name:      "default profile with store directory set",
			profile:   flowstore.ProfileDefault,
			directory: "/var/lib/tailscale2otel/flows",
			wantWarn:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			c.Flows.CapacityProfile = tc.profile
			c.Flows.Store.Directory = tc.directory

			var found bool
			for _, w := range c.Warnings() {
				if strings.HasPrefix(w, "flows.capacity_profile=") {
					found = true
				}
			}
			if found != tc.wantWarn {
				t.Errorf("flows.capacity_profile advisory present = %v, want %v (profile=%q, directory=%q)",
					found, tc.wantWarn, tc.profile, tc.directory)
			}
		})
	}
}

// The advisory message format is a frozen seam (internal/config/advisory.go's
// advisoryKey): the message must begin with the literal dotted config key so
// the status page can extract it. Prove the extraction lands on exactly
// "flows.capacity_profile", not "flows.capacity_profile=compact" or "".
func TestAdvisories_FlowsCapacityProfileKey(t *testing.T) {
	c := config.Default()
	c.Flows.CapacityProfile = flowstore.ProfileCompact
	c.Flows.Store.Directory = "/var/lib/tailscale2otel/flows"

	var found bool
	for _, a := range c.Advisories() {
		if strings.HasPrefix(a.Message, "flows.capacity_profile=") {
			found = true
			if a.Key != "flows.capacity_profile" {
				t.Errorf("advisory key = %q, want %q", a.Key, "flows.capacity_profile")
			}
		}
	}
	if !found {
		t.Fatal("no flows.capacity_profile advisory found")
	}
}
