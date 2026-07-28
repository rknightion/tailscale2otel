package statushtml_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statushtml"
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

// TestRender_CapabilityMatrix asserts the #430 capability tab renders each row's
// requirement, preflight and live state, and that a non-active row explains
// itself rather than just reading as broken.
func TestRender_CapabilityMatrix(t *testing.T) {
	s := statusdata.Status{
		Provider:     "tailscale",
		Capabilities: []string{"devices", "flowlogs"},
		CapabilityMatrix: []statusdata.CapabilityRow{
			{Collector: "devices", Capability: "devices", RequiredScopes: []string{"devices:core:read"},
				ConfigEnabled: true, ProviderSupported: true, Active: true,
				ScopeStatus: statusdata.ScopeSatisfied, State: "supported", LastProbe: "2026-07-25T12:00:00Z"},
			{Collector: "devices", Subrequest: "device_invites", Capability: "device_invites",
				RequiredScopes: []string{"devices_invites:read"}, MissingScopes: []string{"devices_invites:read"},
				ConfigEnabled: true, ProviderSupported: true, Active: true,
				ScopeStatus: statusdata.ScopeInsufficient, State: "scope_denied", Actionable: true},
			{Collector: "dns", Capability: "dns", ConfigEnabled: true, Active: false,
				Reason: statusdata.CapabilityReasonUnsupported, ScopeStatus: statusdata.ScopeUnknown, State: "unknown"},
			{Collector: "flowlogs", Capability: "flowlogs", ConfigEnabled: true, ProviderSupported: true,
				Active: false, Reason: statusdata.CapabilityReasonNotRegistered, Source: "stream",
				ScopeStatus: statusdata.ScopeSatisfied, State: "unknown"},
		},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`data-tab="capabilities"`, `data-target="capabilities"`,
		"devices:core:read", "device_invites", "devices_invites:read",
		"scope_denied", "insufficient", "unsupported", "not polling", "stream",
		"2026-07-25T12:00:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("capability tab missing %q", want)
		}
	}
	// Summary tiles: 2 active, 1 actionable, 1 running scope gap.
	for _, want := range []string{
		`<div class="k">Active capabilities</div><div class="v">2</div>`,
		`<div class="k">Needing attention</div><div class="v">1</div>`,
		`<div class="k">Scope gaps</div><div class="v">1</div>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("capability summary missing %q", want)
		}
	}
}

// A component failure has to be VISIBLE, not just folded into the overall
// verdict. "degraded" with no indication of which subsystem went leaves an
// operator reading logs, which is what the status page exists to avoid (#318).
func TestRender_ComponentFailureIsVisible(t *testing.T) {
	s := statusdata.Status{
		Health: "degraded",
		Components: []statusdata.ComponentStatus{
			{Name: "admin", Enabled: true},
			{Name: "metrics", Enabled: true, Failed: true, Reason: "listen tcp :2112: bind: address already in use"},
			{Name: "webhook"},
		},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	// Each of these must be unique to the components table. A bare "metrics" or
	// "componentsBody" also matches the JS updater and half the rest of the page,
	// so it would pass with the table deleted — verified by removing it.
	for _, want := range []string{
		`id="componentsBody"`,    // the live-update target, or the table goes stale on poll
		"address already in use", // the reason, so the failure is actionable without the logs
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page does not contain %q", want)
		}
	}
	// A disabled component is still listed: "off" and "not shown" are different
	// answers to "why am I not getting webhook events".
	if !strings.Contains(out, "webhook") {
		t.Error("a disabled component is missing from the page entirely")
	}
}

// The page's "last export" was a collector-success proxy, so it could report
// recent data flow while every OTLP request failed. Delivery is now its own
// section, per signal, sourced from the exporters (#317).
func TestRender_DeliveryIsPerSignalAndClassOnly(t *testing.T) {
	s := statusdata.Status{
		Delivery: []statusdata.DeliverySignal{
			{Signal: "metrics", Exports: 12, LastSuccessAt: "2026-07-28T12:00:00Z", LastDurationMs: 250},
			{Signal: "logs", Exports: 12, Failures: 5, ConsecutiveFailures: 5, Failing: true,
				LastFailureAt: "2026-07-28T12:01:00Z", LastErrorClass: "unauthenticated"},
			{Signal: "traces"},
		},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`id="deliveryBody"`, // the live-update target
		"unauthenticated",   // the error CLASS is shown, so the failure is actionable
		"2026-07-28T12:01:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page does not contain %q", want)
		}
	}
	// A signal that has never exported is still a row: "no exports yet" is a
	// finding, and omitting it reads as "fine". Asserted as the rendered badge
	// markup, not the bare phrase — the JS updater builds the same words, so a
	// substring check passes with the table deleted.
	if !strings.Contains(out, `<span class="badge pending">no exports yet</span>`) {
		t.Error("an idle signal is not rendered at all")
	}
}

// The config warnings were logged once at startup and otherwise reduced to a
// COUNT on a gauge, so an operator alerting on that gauge had to find the
// startup log of a pod that may since have restarted (#319).
func TestRender_ConfigAdvisoriesAreListed(t *testing.T) {
	s := statusdata.Status{
		Advisories: []statusdata.ConfigAdvisory{
			{Key: "tailscale.auth.method", Message: "tailscale.auth.method=apikey: prefer an OAuth client"},
			{Message: "an advisory with no derivable key"},
		},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`id="advisoriesBody"`,
		"prefer an OAuth client",             // the remediation, which is the point
		"<code>tailscale.auth.method</code>", // the key, as its own column
		"an advisory with no derivable key",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page does not contain %q", want)
		}
	}
}

// TestRender_TailnetSelector asserts the per-tailnet selector (#325) renders
// only in multi-tailnet mode, lists every tailnet plus an "All" option, and is
// wired to the JS that filters Collectors/API/Cardinality/Devices.
func TestRender_TailnetSelector(t *testing.T) {
	multi := statusdata.Status{Tailnets: []statusdata.TailnetStatus{
		{Name: "acme.example.com"},
		{Name: "beta.example.com"},
	}}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, multi); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`id="tnSel"`,
		`<option value="">All (2)</option>`,
		`<option value="acme.example.com">acme.example.com</option>`,
		`<option value="beta.example.com">beta.example.com</option>`,
		`var TAILNETS = ["acme.example.com","beta.example.com"];`,
		"function tailnetView", "function initTailnetSelector", "function rebuildDevices", "function rebuildCardinality",
		"initTailnetSelector();",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-tailnet page missing %q", want)
		}
	}

	// Single tailnet: no selector, empty TAILNETS array — verified by deleting
	// the `{{if gt (len .Tailnets) 1}}` gate around the selector block, which
	// made this branch fail with "unexpected id=\"tnSel\"" before being
	// restored, so the guard is checking real markup rather than a name that
	// never appears.
	single := statusdata.Status{Tailnets: []statusdata.TailnetStatus{{Name: "solo.example.com"}}}
	buf.Reset()
	if err := statushtml.Render(&buf, single); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	sout := buf.String()
	if strings.Contains(sout, `id="tnSel"`) {
		t.Error("single-tailnet page should not render the tailnet selector")
	}
	if strings.Contains(sout, `id="tailnetSelectorWrap"`) {
		t.Error("single-tailnet page should not render the selector wrapper")
	}
	if !strings.Contains(sout, `var TAILNETS = ["solo.example.com"];`) {
		t.Error("TAILNETS should still list the one tailnet even though the selector is hidden")
	}

	// Zero tailnets (the zero-value Status a test or an early admin-server
	// probe might render): TAILNETS must be a valid empty JS array, not "[,]"
	// or similarly malformed from an empty range.
	buf.Reset()
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(buf.String(), "var TAILNETS = [];") {
		t.Error("zero-tailnet page should render an empty TAILNETS array")
	}
}

// TestRender_TailnetFilteredSections asserts the Devices and Cardinality
// sections carry the DOM hooks (`id="devBody"`, `id="cardSeriesBody"`, etc.) the
// tailnet-selector JS rebuilds on selection — a bare-phrase check would also
// match the JS source itself, so these assert the actual table structure.
func TestRender_TailnetFilteredSections(t *testing.T) {
	s := statusdata.Status{
		Devices: []statusdata.DeviceRow{{Name: "laptop", OS: "linux"}},
		Cardinality: statusdata.CardinalityInfo{
			Available: true, Total: 10,
			Series: []statusdata.SeriesRow{{Metric: "m", Count: 10}},
			Alerts: []statusdata.CardinalityAlert{{Metric: "m", Count: 10, Level: "warning"}},
			Labels: []statusdata.LabelRow{{Label: "l", TotalDistinct: 1}},
		},
	}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, s); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<tbody id="devBody">`,
		`<tbody id="cardAlertsBody">`,
		`<tbody id="cardSeriesBody">`,
		`<tbody id="cardLabelsBody">`,
		`id="devCount"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// An empty list must read as "checked, nothing wrong", not as a missing section.
func TestRender_NoAdvisoriesSaysSo(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	// The rendered cell, not the bare phrase: the JS updater builds the same
	// words, so a substring check passes with the table deleted. Third time this
	// shape has been vacuous in this file.
	if !strings.Contains(buf.String(), `<td colspan="2" class="muted">No active configuration advisories.</td>`) {
		t.Error("an empty advisory list renders nothing at all, which reads as a missing section")
	}
}

// TestRender_EventsLinkFollowsTheStore asserts the /events explorer (#300) is
// reachable from the status page exactly when its store is being fed.
//
// This guard exists because the two were built by separate lanes and the link
// was missing entirely: the explorer's routes register only when a store is
// fed, so the page is the only way an operator would ever find it, and nothing
// else in either package's tests would have noticed a page nobody can reach.
// The inverse matters just as much — linking unconditionally hands the operator
// a link that 404s, which reads as a broken exporter rather than a disabled
// feature.
// TestRender_TabsHaveARIARoles asserts the tab bar and its panels carry the
// standard ARIA tabs pattern: a tablist owning tab buttons that each name the
// panel they control, and panels that name the tab that owns them. This is
// what lets a screen reader announce "tab, 1 of 7, selected" instead of just
// "button" for a control that behaves nothing like one.
func TestRender_TabsHaveARIARoles(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<nav class="tabs" id="tabs" role="tablist" aria-label="Status sections">`,
		`id="tab-overview" data-target="overview" role="tab" aria-controls="panel-overview" aria-selected="true" tabindex="0"`,
		`id="tab-collectors" data-target="collectors" role="tab" aria-controls="panel-collectors" aria-selected="false" tabindex="-1"`,
		`<section data-tab="overview" id="panel-overview" role="tabpanel" aria-labelledby="tab-overview" tabindex="0">`,
		`<section data-tab="config" id="panel-config" role="tabpanel" aria-labelledby="tab-config" tabindex="0">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// TestRender_TabsHaveArrowKeyNavigation asserts the keydown handler that
// implements roving-tabindex arrow-key navigation across the tab bar is wired
// up. This cannot assert the actual keyboard behavior (no browser here), only
// that the handler exists and is attached to the tablist.
func TestRender_TabsHaveArrowKeyNavigation(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"function handleTabsKeydown",
		`tabs.addEventListener('keydown', handleTabsKeydown)`,
		"ArrowRight", "ArrowLeft", "'Home'", "'End'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// TestRender_ChartsHaveAccessibleNamesAndSummaries asserts every chart <svg>
// carries role="img" plus a static accessible name rendered by the server,
// and a data-chart-name hook the client uses to compute a live textual
// summary (min/max/latest) once data arrives. The live summary text itself is
// JS-computed and cannot be asserted from a server-rendered fixture.
func TestRender_ChartsHaveAccessibleNamesAndSummaries(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<svg class="chart" id="chGoroutines" role="img" aria-label="Goroutines over the last 10 minutes" data-chart-name="Goroutines"></svg>`,
		`<svg class="chart" id="chHeap" role="img" aria-label="Heap in use over the last 10 minutes" data-chart-name="Heap in use"></svg>`,
		`<svg class="chart" id="chGC" role="img" aria-label="GC rate over the last 10 minutes" data-chart-name="GC rate"></svg>`,
		`<svg class="chart" id="chEmit" role="img" aria-label="Emitted throughput over the last 10 minutes" data-chart-name="Emitted throughput"></svg>`,
		`<svg class="chart" id="chFailing" role="img" aria-label="Collectors failing over the last 10 minutes" data-chart-name="Collectors failing"></svg>`,
		`<svg class="chart" id="chDuration" role="img" aria-label="Mean run duration over the last 10 minutes" data-chart-name="Mean run duration"></svg>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// TestRender_LiveRegionsAnnounceHealthAndStaleness asserts the health-reasons
// strip and the disconnected/stale banner are announced by assistive tech
// when their text changes, not just visually shown.
func TestRender_LiveRegionsAnnounceHealthAndStaleness(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<div class="reasons" id="healthReasons" role="status"`,
		`<div id="staleBanner" role="alert"></div>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// TestRender_FocusVisibleStylesPresent asserts a focus-visible rule exists and
// that the old collector-info rule which unconditionally suppressed the
// outline on focus (not just on mouse hover) is gone.
func TestRender_FocusVisibleStylesPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, ":focus-visible{outline:") {
		t.Error("no :focus-visible outline rule present")
	}
	if strings.Contains(out, ".collector-info:hover,.collector-info:focus{color:var(--accent);outline:none}") {
		t.Error("collector-info still unconditionally suppresses the focus outline (only :hover should lose it)")
	}
}

// TestRender_ReducedMotionRespected asserts a prefers-reduced-motion rule is
// present in the stylesheet.
func TestRender_ReducedMotionRespected(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(buf.String(), "@media (prefers-reduced-motion: reduce)") {
		t.Error("no prefers-reduced-motion rule present")
	}
}

// TestRender_UpdateBadgeOnlyVisibleWhenAvailable asserts the header update
// badge (#330) is always PRESENT in the DOM (so the JS poller can toggle it
// without re-inserting a node) but only VISIBLE — no inline
// `style="display:none"` — when State is "available". Every other state must
// hide it, since a badge visible for "current"/"error"/"unknown" would read
// as "you have an update" when you don't.
func TestRender_UpdateBadgeOnlyVisibleWhenAvailable(t *testing.T) {
	avail := statusdata.Status{Update: statusdata.UpdateInfo{
		State: "available", CurrentVersion: "1.2.3", LatestVersion: "1.5.0",
		ReleaseURL: "https://github.com/rknightion/tailscale2otel/releases/latest",
	}}
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, avail); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`id="updateBadge"`,
		`href="https://github.com/rknightion/tailscale2otel/releases/latest"`,
		`target="_blank"`,
		`rel="noopener noreferrer"`,
		"update available: 1.5.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("available-update page missing %q", want)
		}
	}
	if strings.Contains(out, `id="updateBadge" href="https://github.com/rknightion/tailscale2otel/releases/latest" target="_blank" rel="noopener noreferrer" title="current 1.2.3 · latest 1.5.0" style="display:none"`) {
		t.Error("available state should NOT hide the badge")
	}

	for _, st := range []string{"current", "error", "disabled", "checking", "unknown", ""} {
		s := statusdata.Status{Update: statusdata.UpdateInfo{State: st, LatestVersion: "1.5.0"}}
		buf.Reset()
		if err := statushtml.Render(&buf, s); err != nil {
			t.Fatalf("Render error (state=%q): %v", st, err)
		}
		hout := buf.String()
		if !strings.Contains(hout, `id="updateBadge"`) {
			t.Errorf("state=%q: updateBadge element must still be present (JS toggles it, never inserts it)", st)
		}
		if !strings.Contains(hout, `id="updateBadge" href="" target="_blank" rel="noopener noreferrer" title="" style="display:none"`) {
			t.Errorf("state=%q: badge should be hidden with style=\"display:none\"", st)
		}
	}
}

// TestRender_UpdateCardReflectsState asserts the Overview "Update check" card
// renders distinct, non-vacuous text for each state — verified against the
// actual `id="updateState"`/`id="updateSub"` elements, not a bare phrase that
// could equally come from the JS source embedded later in the same page.
func TestRender_UpdateCardReflectsState(t *testing.T) {
	cases := []struct {
		state        string
		wantState    string
		wantSubPiece string
	}{
		{"available", "update available", "1.2.3"},
		{"current", "up to date", "1.2.3"},
		{"error", "check failed", "network"},
		{"disabled", "disabled", ""},
		{"checking", "checking", ""},
		{"unknown", "unknown", ""},
	}
	for _, c := range cases {
		s := statusdata.Status{Update: statusdata.UpdateInfo{
			State: c.state, CurrentVersion: "1.2.3", LatestVersion: "1.5.0", LastErrorClass: "network",
		}}
		var buf bytes.Buffer
		if err := statushtml.Render(&buf, s); err != nil {
			t.Fatalf("Render error (state=%q): %v", c.state, err)
		}
		out := buf.String()
		wantState := `<div class="v" id="updateState">` + c.wantState
		if !strings.Contains(out, wantState) {
			t.Errorf("state=%q: missing %q in rendered page", c.state, wantState)
		}
		if c.wantSubPiece != "" && !strings.Contains(out, c.wantSubPiece) {
			t.Errorf("state=%q: missing sub-text piece %q", c.state, c.wantSubPiece)
		}
	}
}

// TestRender_UpdateCardJSMirrorsTemplate asserts the client-side poller JS
// that rebuilds the update badge/card on every refresh is actually present —
// this is the dominant vacuity shape in this file (a string that matches the
// JS source itself rather than server-rendered markup), so this checks the
// function definition AND that it is wired into refresh().
func TestRender_UpdateCardJSMirrorsTemplate(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"function setUpdateInfo(d)",
		"setUpdateInfo(d);",
		"u.state==='available'",
		"'update available'", "'up to date'", "'check failed'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

func TestRender_EventsLinkFollowsTheStore(t *testing.T) {
	const link = `<a href="/events" title="Explore recent audit and webhook events">events &rarr;</a>`

	var on bytes.Buffer
	if err := statushtml.Render(&on, statusdata.Status{
		Events: statusdata.EventStoreInfo{Enabled: true, Capacity: 5000},
	}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(on.String(), link) {
		t.Errorf("event store enabled but the page offers no /events link; want %q", link)
	}

	var off bytes.Buffer
	if err := statushtml.Render(&off, statusdata.Status{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if strings.Contains(off.String(), `href="/events"`) {
		t.Error("event store disabled but the page links /events, which is not registered and would 404")
	}
}
