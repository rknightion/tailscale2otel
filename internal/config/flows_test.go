package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

func TestDefaults_Flows(t *testing.T) {
	c := config.Default()
	if !c.Flows.Enabled {
		t.Error("flows.enabled defaults to false; the built-in flow view is the point of collecting this")
	}
	if want := 6 * time.Hour; c.Flows.Retention.D() != want {
		t.Errorf("flows.retention default = %v, want %v", c.Flows.Retention.D(), want)
	}
}

// Retention sizes an in-memory ring, so a mistyped value is a memory bug rather
// than a slow query. Both ends are bounded.
func TestValidate_FlowsRetentionBounds(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		retention time.Duration
		wantErr   bool
	}{
		{name: "default", enabled: true, retention: 6 * time.Hour},
		{name: "minimum", enabled: true, retention: time.Minute},
		{name: "maximum", enabled: true, retention: 24 * time.Hour},
		{name: "below one bucket", enabled: true, retention: 30 * time.Second, wantErr: true},
		{name: "zero", enabled: true, retention: 0, wantErr: true},
		{name: "negative", enabled: true, retention: -time.Hour, wantErr: true},
		{name: "beyond the in-memory bound", enabled: true, retention: 48 * time.Hour, wantErr: true},
		// Disabled means the store is never built, so the value cannot hurt anyone.
		{name: "ignored when disabled", enabled: false, retention: 0},
		{name: "absurd but disabled", enabled: false, retention: 1000 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			c.Flows.Enabled = tc.enabled
			c.Flows.Retention = config.Duration(tc.retention)

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for retention %v", tc.retention)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.retention"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

// The store's only consumer is the admin page. Spending memory on data nobody
// can reach is the kind of thing an operator finds months later.
func TestWarnings_FlowsUnreachableWithoutAdminPage(t *testing.T) {
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
			c.Flows.Enabled = true
			c.Admin.Enabled = tc.adminOn
			c.Admin.LandingPage = tc.landingPage

			warnings := c.Warnings()
			var found bool
			for _, w := range warnings {
				if strings.Contains(w, "flows.enabled") {
					found = true
				}
			}
			if found != tc.wantWarn {
				t.Errorf("flows warning present = %v, want %v; warnings: %v", found, tc.wantWarn, warnings)
			}
		})
	}
}

// Disabling the view must not produce the "unreachable" advisory — there is
// nothing to be unreachable.
func TestWarnings_FlowsDisabledIsQuiet(t *testing.T) {
	c := config.Default()
	c.Flows.Enabled = false
	c.Admin.Enabled = false
	for _, w := range c.Warnings() {
		if strings.Contains(w, "flows.enabled") {
			t.Errorf("unexpected flows advisory with the view disabled: %q", w)
		}
	}
}
