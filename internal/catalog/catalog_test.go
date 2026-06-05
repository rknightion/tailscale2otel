package catalog_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/internal/catalog"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
)

// canonicalGroups are the docs/metrics.md sections every declared metric/log
// event must belong to. A typo'd or new Group fails here (and would also fail
// Render's coverage check), so the docs structure stays in sync with the code.
var canonicalGroups = map[string]bool{
	"Self-observability": true,
	"Network / flow":     true,
	"Devices":            true,
	"Users":              true,
	"Keys":               true,
	"Settings":           true,
	"ACL":                true,
	"DNS":                true,
	"Contacts":           true,
	"Webhooks":           true,
	"Posture":            true,
	"Log streaming":      true,
	"Features":           true,
	"Receivers":          true,
	"Node metrics":       true,
}

func TestMetrics_NoDuplicateNames(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range catalog.Metrics() {
		if seen[m.Name] {
			t.Errorf("duplicate metric name across package catalogs: %q", m.Name)
		}
		seen[m.Name] = true
	}
}

func TestMetrics_WellFormed(t *testing.T) {
	valid := map[metricdoc.Instrument]bool{
		metricdoc.Counter: true, metricdoc.Gauge: true, metricdoc.UpDownCounter: true,
	}
	for _, m := range catalog.Metrics() {
		if m.Name == "" || m.Unit == "" || m.Description == "" || m.Group == "" {
			t.Errorf("metric has empty required field: %+v", m)
		}
		if !valid[m.Instrument] {
			t.Errorf("metric %q has invalid instrument %q", m.Name, m.Instrument)
		}
		if !canonicalGroups[m.Group] {
			t.Errorf("metric %q has non-canonical group %q", m.Name, m.Group)
		}
	}
}

func TestLogEvents_WellFormed(t *testing.T) {
	for _, e := range catalog.LogEvents() {
		if e.Name == "" || e.Severity == "" || e.Description == "" || e.Group == "" {
			t.Errorf("log event has empty required field: %+v", e)
		}
		if !canonicalGroups[e.Group] {
			t.Errorf("log event %q has non-canonical group %q", e.Name, e.Group)
		}
	}
}

// TestContainsRepresentativeSignals is a sanity check that the aggregation wired
// up every layer: a self-obs metric, a network metric, a node metric, a counter,
// and a couple of log events are all present.
func TestContainsRepresentativeSignals(t *testing.T) {
	metricNames := map[string]bool{}
	for _, m := range catalog.Metrics() {
		metricNames[m.Name] = true
	}
	for _, want := range []string{
		"tailscale2otel.up",
		"tailscale2otel.build_info",
		"tailscale2otel.scrape.duration",
		"tailscale.network.io",
		"tailscale.config.audit.events",
		"tailscale.device.online",
		"tailscale.users.count",
		"tailscale.node.up",
		"tailscale.stream.records",
		"tailscale.webhook.events",
		"tailscale.feature.enabled",
		"tailscale.contact.needs_verification",
		"tailscale.webhook_endpoints.count",
		"tailscale.posture_integrations.count",
		"tailscale.logstream.configured",
	} {
		if !metricNames[want] {
			t.Errorf("aggregate metric catalog missing %q", want)
		}
	}

	logNames := map[string]bool{}
	for _, e := range catalog.LogEvents() {
		logNames[e.Name] = true
	}
	for _, want := range []string{"tailscale.network.flow", "tailscale.config.audit", "tailscale.key.expiring", "tailscale.logstream.error"} {
		if !logNames[want] {
			t.Errorf("aggregate log catalog missing %q", want)
		}
	}
}
