package statushtml_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v2/internal/app/statushtml"
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

// TestRender_RefreshMs asserts the configurable refresh interval is rendered
// into the page and the old hardcoded 10s poll is gone.
func TestRender_RefreshMs(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{RefreshMs: 3000}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	// html/template's JS-context escaper space-pads numeric values, so match the
	// value near the assignment rather than an exact "= 3000" spacing.
	i := strings.Index(out, "__refreshMs")
	if i < 0 || !strings.Contains(out[i:i+40], "3000") {
		t.Fatalf("refresh interval 3000 not rendered into page")
	}
	if strings.Contains(out, "refresh, 10000)") {
		t.Fatalf("hardcoded 10000 refresh still present")
	}
}

// TestRender_TabbedStructure asserts the six tabs, the theme toggle, and the
// noscript fallback are present.
func TestRender_TabbedStructure(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`data-tab="overview"`, `data-tab="collectors"`, `data-tab="api"`,
		`data-tab="cardinality"`, `data-tab="inventory"`, `data-tab="config"`,
		`id="themeToggle"`, `id="tabs"`, "<noscript>", `data-theme`,
		"function showTab", "function drawChart", "function toggleTheme",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tabbed page missing %q", want)
		}
	}
}

// TestRender_NewPanels asserts the new data surfaces render: collector
// freshness, the API auth panel, the cardinality suite (alerts, labels, growth)
// and the config download link.
func TestRender_NewPanels(t *testing.T) {
	s := statusdata.Status{
		Service: statusdata.ServiceInfo{Name: "tailscale2otel", SelfObs: true},
		Collectors: []statusdata.CollectorStatus{{
			Name: "devices", HasRun: true, LastSuccess: true, Runs: 3,
			Freshness: "12s", FreshnessState: "ok", LastSuccessAt: "2026-01-01T00:00:00Z",
		}},
		API: statusdata.APIInfo{
			Auth: statusdata.APIAuth{Method: "oauth", TotalCalls: 128, Total429: 2},
		},
		Cardinality: statusdata.CardinalityInfo{
			Available: true, Total: 5000, TotalMetrics: 3,
			Thresholds: statusdata.CardinalityThresholds{Warning: 2000, Critical: 8000},
			Series:     []statusdata.SeriesRow{{Metric: "flow.io", Count: 3000, Level: "warning"}},
			Alerts:     []statusdata.CardinalityAlert{{Metric: "flow.io", Count: 3000, Level: "warning"}},
			Labels: []statusdata.LabelRow{{Label: "src.node", TotalDistinct: 42, Metrics: []statusdata.LabelMetricRow{
				{Metric: "flow.io", Distinct: 42, Examples: []string{"laptop", "phone"}},
			}}},
			Growth: []statusdata.GrowthRow{{Metric: "flow.io", Current: 3000, DeltaPct: 50}},
		},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Freshness", "12s", // freshness column
		"Total API calls", "oauth", // api auth panel
		"Active alerts", "High-cardinality labels", "src.node", "laptop", // cardinality suite
		"Growth", "flow.io",
		"/api/config.json", "/api/cardinality.json", // export links
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// TestRender_ThroughputAndFleetTrend asserts the throughput/fleet trend section
// renders on the Overview tab: its heading, the three chart canvases, the stat
// cards, and the JS chart registrations that read the JSON fields.
func TestRender_ThroughputAndFleetTrend(t *testing.T) {
	s := statusdata.Status{
		Throughput: statusdata.ThroughputInfo{
			MetricPoints: 1200, LogRecords: 340,
			MetricPointsPerSec: 12.5, LogRecordsPerSec: 3.5,
			MetricPointsPerSecSeries: []float64{10, 12.5},
			LogRecordsPerSecSeries:   []float64{3, 3.5},
		},
		Fleet: statusdata.FleetInfo{
			Active: 7, Failing: 1, MeanDurationMs: 84,
			FailingSeries:        []int{0, 1},
			MeanDurationMsSeries: []float64{70, 84},
		},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Throughput &amp; fleet trend (~10 min)",           // the new sub-heading
		`id="chEmit"`, `id="chFailing"`, `id="chDuration"`, // the three charts
		`id="emitMetricRate"`, `id="emitLogRate"`, // throughput stat cards
		`id="fleetActive"`, `id="fleetFailing"`, `id="fleetMeanDur"`, // fleet stat cards
		"metric_points_per_sec_series", "log_records_per_sec_series", // JS reads these
		"failing_series", "mean_duration_ms_series",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	// The new section belongs on Overview, immediately after the runtime trend.
	runtime := strings.Index(out, "Runtime trend (~10 min)")
	tp := strings.Index(out, "Throughput &amp; fleet trend (~10 min)")
	tailnets := strings.Index(out, `data-tab="collectors"`)
	if runtime < 0 || tp < runtime || (tailnets > 0 && tp > tailnets) {
		t.Errorf("throughput section is not on the overview tab after the runtime trend (runtime=%d throughput=%d collectors=%d)", runtime, tp, tailnets)
	}
}

// TestRender_CardinalityGatedOff asserts the cardinality tab shows the enable
// prompt (not empty tables) when self-obs is off.
func TestRender_CardinalityGatedOff(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{Service: statusdata.ServiceInfo{SelfObs: false}}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(buf.String(), "Enable <code>self_observability</code>") {
		t.Error("cardinality tab should show the enable-self-obs prompt when off")
	}
}
