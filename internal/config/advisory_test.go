package config

import (
	"strings"
	"testing"
)

// Warnings() returned prose, logged once at startup and otherwise reduced to a
// COUNT on a gauge. An operator alerting on tailscale2otel_config_warnings_ratio
// learned that something was wrong and then had to go find the startup log to
// learn what — on a pod that may have restarted since (#319).

func TestAdvisoriesCarryTheKeyAndTheMessage(t *testing.T) {
	c := Default()
	c.Tailscale.Auth.Method = "apikey"

	got := c.Advisories()
	if len(got) == 0 {
		t.Fatal("no advisories for an apikey config, which Warnings() has always flagged")
	}
	var found bool
	for _, a := range got {
		if a.Key == "tailscale.auth.method" {
			found = true
			if !strings.Contains(a.Message, "OAuth") {
				t.Errorf("advisory message = %q, want the remediation", a.Message)
			}
		}
	}
	if !found {
		t.Errorf("no advisory keyed on tailscale.auth.method: %+v", got)
	}
}

// The count on the gauge and the list on the page must be the same set, or the
// page becomes a second, quieter source of truth.
func TestAdvisoriesMatchWarningsOneForOne(t *testing.T) {
	c := Default()
	c.Tailscale.Auth.Method = "apikey"
	c.Prometheus.Enabled = true

	warnings := c.Warnings()
	advisories := c.Advisories()
	if len(warnings) != len(advisories) {
		t.Fatalf("Warnings() returned %d and Advisories() %d; the metric counts one and the page "+
			"lists the other", len(warnings), len(advisories))
	}
	for i, w := range warnings {
		// Verbatim: the key is metadata, so nothing may be lost in deriving it.
		// An earlier version split the message at the first colon and silently
		// dropped the "=apikey" from "tailscale.auth.method=apikey: ...".
		if advisories[i].Message != w {
			t.Errorf("advisory %d is not its warning verbatim:\n warning:  %q\n advisory: %q",
				i, w, advisories[i].Message)
		}
	}
}

// Every advisory names a config key. A warning an operator cannot trace back to
// a setting is the reason they were hunting through logs in the first place.
func TestEveryAdvisoryNamesAKey(t *testing.T) {
	// A config that trips as many independent advisories as one config can.
	c := Default()
	c.Tailscale.Auth.Method = "apikey"
	c.Prometheus.Enabled = true
	c.Prometheus.Listen = "0.0.0.0:2112"
	c.Admin.Listen = "0.0.0.0:9091"
	c.Streaming.Enabled = true
	c.Streaming.Listen = "0.0.0.0:8088"
	c.PIIFilter.TailnetName = false

	got := c.Advisories()
	if len(got) < 2 {
		t.Fatalf("only %d advisories from a deliberately messy config; the fixture is not "+
			"exercising enough of Warnings() for this test to mean anything", len(got))
	}
	for _, a := range got {
		if a.Key == "" {
			t.Errorf("advisory has no key: %q", a.Message)
			continue
		}
		if strings.ContainsAny(a.Key, " \t") {
			t.Errorf("advisory key %q is prose, not a config path", a.Key)
		}
	}
}

func TestNoAdvisoriesOnADefaultConfig(t *testing.T) {
	// Not a style preference: a default install that greets its operator with
	// advisories teaches them to ignore the list.
	if got := Default().Advisories(); len(got) != 0 {
		t.Errorf("the default config produces %d advisories: %+v", len(got), got)
	}
}
