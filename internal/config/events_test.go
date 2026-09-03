package config_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestDefaults_Events(t *testing.T) {
	c := config.Default()
	if !c.Events.Enabled {
		t.Error("events.enabled defaults to false; the built-in event explorer is the point of collecting this")
	}
	if want := 5000; c.Events.MaxEvents != want {
		t.Errorf("events.max_events default = %d, want %d", c.Events.MaxEvents, want)
	}
}

// MaxEvents sizes an in-memory ring, so a mistyped value is a memory bug
// rather than a slow query. Both ends are bounded (mirrors
// TestValidate_FlowsRetentionBounds).
func TestValidate_EventsMaxEventsBounds(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		maxEvents int
		wantErr   bool
	}{
		{name: "default", enabled: true, maxEvents: 5000},
		{name: "minimum", enabled: true, maxEvents: 100},
		{name: "maximum", enabled: true, maxEvents: 100000},
		{name: "below minimum", enabled: true, maxEvents: 99, wantErr: true},
		{name: "zero", enabled: true, maxEvents: 0, wantErr: true},
		{name: "negative", enabled: true, maxEvents: -1, wantErr: true},
		{name: "beyond the in-memory bound", enabled: true, maxEvents: 100001, wantErr: true},
		// Disabled means the store is never built, so the value cannot hurt anyone.
		{name: "ignored when disabled", enabled: false, maxEvents: 0},
		{name: "absurd but disabled", enabled: false, maxEvents: 10000000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			c.Events.Enabled = tc.enabled
			c.Events.MaxEvents = tc.maxEvents

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for max_events %d", tc.maxEvents)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "events.max_events"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

// The store's only consumer is the admin page. Spending memory on data
// nobody can reach is the kind of thing an operator finds months later
// (mirrors TestWarnings_FlowsUnreachableWithoutAdminPage).
func TestWarnings_EventsUnreachableWithoutAdminPage(t *testing.T) {
	tests := []struct {
		name        string
		landingPage bool
		adminOn     bool
		wantWarn    bool
	}{
		{name: "reachable", landingPage: true, adminOn: true},
		{name: "admin server off", landingPage: true, adminOn: false, wantWarn: true},
		{name: "landing page off", landingPage: false, adminOn: true, wantWarn: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			c.Events.Enabled = true
			c.Admin.Enabled = tc.adminOn
			c.Admin.LandingPage = tc.landingPage

			warnings := c.Warnings()
			var found bool
			for _, w := range warnings {
				if strings.Contains(w, "events.enabled") {
					found = true
				}
			}
			if found != tc.wantWarn {
				t.Errorf("events warning present = %v, want %v; warnings: %v", found, tc.wantWarn, warnings)
			}
		})
	}
}

// Disabling the view must not produce the "unreachable" advisory — there is
// nothing to be unreachable (mirrors TestWarnings_FlowsDisabledIsQuiet).
func TestWarnings_EventsDisabledIsQuiet(t *testing.T) {
	c := config.Default()
	c.Events.Enabled = false
	c.Admin.Enabled = false
	for _, w := range c.Warnings() {
		if strings.Contains(w, "events.enabled") {
			t.Errorf("unexpected events advisory with the view disabled: %q", w)
		}
	}
}
