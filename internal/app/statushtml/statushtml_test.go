package statushtml_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/internal/app/statushtml"
)

// TestRender_ZeroStatus asserts the template executes against a zero value
// without error (also exercising the //go:embed + parse done at package init).
func TestRender_ZeroStatus(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render(zero) error: %v", err)
	}
	if !strings.Contains(buf.String(), "<!DOCTYPE html>") {
		t.Fatalf("output is not an HTML document")
	}
}

// TestRender_Populated asserts representative values from each major section
// appear in the rendered page.
func TestRender_Populated(t *testing.T) {
	s := statusdata.Status{
		Service:    statusdata.ServiceInfo{Name: "tailscale2otel", Version: "v1.2.3", Uptime: "3h12m", SelfObs: true},
		Telemetry:  statusdata.TelemetryInfo{Protocol: "grpc", Endpoint: "otlp.example:4317"},
		Collectors: []statusdata.CollectorStatus{{Name: "devices", HasRun: true, LastSuccess: true, Runs: 5}},
		Cache:      statusdata.CacheInfo{Devices: 7, Age: "30s"},
		Cardinality: statusdata.CardinalityInfo{Available: true, Total: 42, Series: []statusdata.SeriesRow{
			{Metric: "tailscale.network.io", PromName: "tailscale_network_io_bytes_total", Count: 12},
		}},
		NodeDiscovery: statusdata.NodeDiscovery{Enabled: true, LastOK: true, Static: 1, Active: 2, Targets: []statusdata.NodeTarget{
			{Instance: "node-a", URL: "http://100.64.0.1:5252/metrics", Source: "discovered"},
		}},
		Metrics:   []statusdata.MetricRow{{Name: "tailscale.devices.count", PromName: "tailscale_devices_count_ratio", Instrument: "gauge", Series: 3}},
		LogEvents: []statusdata.LogRow{{Name: "tailscale.acl.changed", Severity: "INFO"}},
		Config:    statusdata.ConfigSummary{LogLevel: "info", AuthMethod: "oauth", APIKeySet: false, GCloudTokenSet: true},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"tailscale2otel", "v1.2.3", "3h12m",
		"devices", "tailscale.network.io", "tailscale_network_io_bytes_total",
		"node-a", "discovered", "tailscale.acl.changed", "grafana_cloud_token",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// TestRender_PerTailnetSection asserts the multi-tailnet section renders one row
// per tailnet (and is absent for a single tailnet).
func TestRender_PerTailnetSection(t *testing.T) {
	multi := statusdata.Status{Tailnets: []statusdata.TailnetStatus{
		{Name: "acme.example.com", AuthMethod: "oauth", Cache: statusdata.CacheInfo{Devices: 12}},
		{Name: "beta.example.com", AuthMethod: "apikey", Cache: statusdata.CacheInfo{Devices: 3}, Failing: 1},
	}}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, multi); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Tailnets (2)", "acme.example.com", "beta.example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-tailnet page missing %q", want)
		}
	}

	// Single tailnet: the section must not appear.
	single := statusdata.Status{Tailnets: []statusdata.TailnetStatus{{Name: "solo.example.com"}}}
	buf.Reset()
	if err := statushtml.Render(&buf, single); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if strings.Contains(buf.String(), `id="tailnets"`) {
		t.Error("single-tailnet page should not render the Tailnets section")
	}
}

// TestRender_CollectorInfoTooltip asserts the per-collector info affordance is
// present: the server-rendered no-JS fallback carries the purpose + metric
// names, and the client-side rebuild reads the new JSON fields so the rich
// popover survives the auto-refresh.
func TestRender_CollectorInfoTooltip(t *testing.T) {
	s := statusdata.Status{
		Collectors: []statusdata.CollectorStatus{{
			Name:        "devices",
			HasRun:      true,
			LastSuccess: true,
			Description: "Lists every tailnet device and refreshes the enrichment cache.",
			Metrics: []statusdata.MetricBrief{
				{Name: "tailscale.device.online", Description: "1 if the device is online."},
			},
		}},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Lists every tailnet device", // server fallback purpose
		"tailscale.device.online",    // server fallback metric name
		"collector-info",             // tooltip affordance hook (CSS + JS)
		"c.description",              // JS rebuild reads the field
		"c.metrics",                  // JS rebuild reads the field
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}
