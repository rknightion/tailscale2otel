package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// TestCollectorDocs_CoverAllRegistered guards the admin info-tooltip data: every
// collector that can be registered must resolve to a non-empty purpose and at
// least one emitted metric, so a newly added collector cannot ship without its
// tooltip. It exercises both the normal poll path and the stream-only flowlogs
// feature probe.
func TestCollectorDocs_CoverAllRegistered(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	c := &cfg.Collectors
	c.Devices.Enabled = true
	c.Users.Enabled = true
	c.Keys.Enabled = true
	c.Settings.Enabled = true
	c.Acl.Enabled = true
	c.Dns.Enabled = true
	c.Contacts.Enabled = true
	c.Webhooks.Enabled = true
	c.PostureIntegrations.Enabled = true
	c.LogStream.Enabled = true
	c.Services.Enabled = true
	c.NodeMetrics.Enabled = true
	c.NodeMetrics.Targets = []config.NodeMetricsTarget{{URL: "http://node:5252/metrics"}}
	c.Flowlogs.Enabled = true
	c.Auditlogs.Enabled = true
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	// Stream-only flowlogs registers the lightweight feature probe instead.
	cfg2 := config.Default()
	cfg2.Tailscale.Tailnet = "example.com"
	cfg2.Collectors.Flowlogs.Enabled = true
	cfg2.Collectors.Flowlogs.Source = "stream"
	a2 := baseTestApp(t, cfg2, "http://127.0.0.1:0", telemetrytest.New())

	var entries []collector.Entry
	entries = append(entries, a.runtimes[0].registry.Entries()...)
	entries = append(entries, a2.runtimes[0].registry.Entries()...)

	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Collector.Name()
		if seen[name] {
			continue
		}
		seen[name] = true
		about, metrics := collectorBrief(name)
		if about == "" {
			t.Errorf("collector %q has no tooltip description", name)
		}
		if len(metrics) == 0 {
			t.Errorf("collector %q has no tooltip metrics", name)
		}
	}
	if !seen["flowlogs-feature"] {
		t.Fatal("flowlogs-feature probe was not registered by the stream-only config")
	}
}
