package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/otlptranslator"
)

func scrape(t *testing.T, ps *ProviderSet) string {
	t.Helper()
	srv := httptest.NewServer(promhttp.HandlerFor(ps.PromGatherers(), promhttp.HandlerOpts{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("scrape status = %d, body=%s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// TestPrometheusConstAttrCollisionGuard pins #91: a data-point attribute whose
// normalized Prometheus name equals a const attr's (tailscale_tailnet /
// tailscale2otel_provider) must be dropped by the collision guard so the appended
// const attr is not duplicated into a sample-rejecting collision.
func TestPrometheusConstAttrCollisionGuard(t *testing.T) {
	ctx := context.Background()
	ps, err := NewProviderSet(ctx, Options{ServiceName: "tailscale2otel", Provider: "tailscale",
		PrometheusEnabled: true, Protocol: "stdout", StdoutWriter: io.Discard},
		[]PerTailnetOptions{{Name: "solo", InstanceID: "i"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	defer func() { _ = ps.Shutdown(ctx) }()

	ps.Tailnet("solo").Emitter().Gauge("tailscale.devices.count", "1", "d", 5, Attrs{
		"tailscale_tailnet":       "evil",
		"tailscale2otel_provider": "evil",
	})
	body := scrape(t, ps)
	seen := false
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "tailscale_devices_count") {
			continue
		}
		seen = true
		if n := strings.Count(line, "tailscale_tailnet="); n != 1 {
			t.Errorf("want exactly one tailscale_tailnet label, got %d: %q", n, line)
		}
		if strings.Contains(line, `tailscale_tailnet="evil"`) {
			t.Errorf("passthrough value beat the const attr: %q", line)
		}
		if !strings.Contains(line, `tailscale_tailnet="solo"`) {
			t.Errorf("const tailscale_tailnet=solo missing: %q", line)
		}
		if n := strings.Count(line, "tailscale2otel_provider="); n != 1 {
			t.Errorf("want exactly one tailscale2otel_provider label, got %d: %q", n, line)
		}
	}
	if !seen {
		t.Fatalf("no tailscale_devices_count series in body:\n%s", body)
	}
}

func TestPrometheusMultiTailnetExposition(t *testing.T) {
	ctx := context.Background()
	ps, err := NewProviderSet(ctx, Options{ServiceName: "tailscale2otel", PrometheusEnabled: true, Protocol: "stdout", StdoutWriter: io.Discard},
		[]PerTailnetOptions{{Name: "alpha", InstanceID: "i/alpha"}, {Name: "beta", InstanceID: "i/beta"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	defer func() { _ = ps.Shutdown(ctx) }()

	// tailscale.devices.count is a unit-"1" gauge -> tailscale_devices_count_ratio.
	ps.Tailnet("alpha").Emitter().Gauge("tailscale.devices.count", "1", "devices", 3, nil)
	ps.Tailnet("beta").Emitter().Gauge("tailscale.devices.count", "1", "devices", 7, nil)
	ps.Process().Emitter().Gauge("tailscale2otel.up", "1", "up", 1, nil)

	body := scrape(t, ps)

	// The unit suffix IS asserted exactly (#379): the pull path pins
	// UnderscoreEscapingWithSuffixes, under which a unit-"1" gauge gets `_ratio`
	// — the same rule Mimir's OTLP ingest applies, because both use
	// prometheus/otlptranslator. Each tailnet's series must be present and carry
	// its own tailscale_tailnet label — a per-data-point attribute (roadmap item
	// L), not a resource-promoted constant label, and it must appear exactly once.
	if !strings.Contains(body, "\n# TYPE tailscale_devices_count_ratio gauge\n") {
		t.Errorf("want exact family `tailscale_devices_count_ratio` (gauge); body:\n%s", body)
	}
	hasAlpha, hasBeta := false, false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "tailscale_devices_count_ratio{") {
			if strings.Count(line, "tailscale_tailnet=") > 1 {
				t.Errorf("duplicate tailscale_tailnet label on series: %q", line)
			}
			if strings.Contains(line, `tailscale_tailnet="alpha"`) {
				hasAlpha = true
			}
			if strings.Contains(line, `tailscale_tailnet="beta"`) {
				hasBeta = true
			}
		}
	}
	if !hasAlpha || !hasBeta {
		t.Errorf("missing per-tailnet devices_count series (alpha=%v beta=%v); body:\n%s", hasAlpha, hasBeta, body)
	}
	if strings.Contains(body, "otel_scope_name") {
		t.Errorf("otel_scope_name leaked (WithoutScopeInfo not applied); body:\n%s", body)
	}
	if !strings.Contains(body, "target_info") {
		t.Errorf("target_info missing; body:\n%s", body)
	}
	// Process-global series must NOT carry a tailnet label.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "tailscale2otel_up") && strings.Contains(line, "tailscale_tailnet=") {
			t.Errorf("process-global up carries tailnet label: %q", line)
		}
	}
}

func TestPrometheusSingleTailnetLabel(t *testing.T) {
	ctx := context.Background()
	ps, err := NewProviderSet(ctx, Options{ServiceName: "tailscale2otel", PrometheusEnabled: true, Protocol: "stdout", StdoutWriter: io.Discard},
		[]PerTailnetOptions{{Name: "solo", InstanceID: "i"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	defer func() { _ = ps.Shutdown(ctx) }()
	ps.Tailnet("solo").Emitter().Gauge("tailscale.devices.count", "1", "devices", 5, nil)
	body := scrape(t, ps)
	if !strings.Contains(body, "tailscale_devices_count_ratio{") {
		t.Errorf("want exact series name tailscale_devices_count_ratio; body:\n%s", body)
	}
	if !strings.Contains(body, `tailscale_tailnet="solo"`) {
		t.Errorf("single-tailnet missing tailnet label (data-point attr, item L); body:\n%s", body)
	}
}

// TestPromTranslationStrategyPinned asserts the pull path pins its
// OTLP→Prometheus translation strategy EXPLICITLY (#379). The pinned exporter's
// zero value happens to resolve to the same strategy today, so this test is a
// guard against an upstream default flip (which upstream has announced) silently
// renaming or re-escaping every series on /metrics. If this fails because the
// pin was changed on purpose, every name in TestPromExactNames and in
// internal/catalog's promname manifest changes too — that is a breaking change.
func TestPromTranslationStrategyPinned(t *testing.T) {
	if got, want := PromTranslationStrategy, otlptranslator.UnderscoreEscapingWithSuffixes; got != want {
		t.Fatalf("PromTranslationStrategy = %q, want %q", got, want)
	}
	if !PromTranslationStrategy.ShouldAddSuffixes() {
		t.Errorf("pinned strategy must add unit/type suffixes")
	}
	if !PromTranslationStrategy.ShouldEscape() {
		t.Errorf("pinned strategy must escape non-Prometheus runes to underscores")
	}
}

// TestPromExactNames pins the EXACT /metrics names for a representative
// counter, gauge, up-down counter and histogram — including the histogram's
// _bucket/_sum/_count family members — plus the token-dedupe case that
// metricdoc.PromName used to get wrong (tailscale2otel.objectstore.bytes with
// unit By must NOT become *_bytes_bytes_total). This replaces the previous
// prefix-only assertions, which could not have caught a unit-suffix change.
func TestPromExactNames(t *testing.T) {
	ctx := context.Background()
	ps, err := NewProviderSet(ctx, Options{ServiceName: "tailscale2otel", Provider: "tailscale",
		PrometheusEnabled: true, Protocol: "stdout", StdoutWriter: io.Discard},
		[]PerTailnetOptions{{Name: "solo", InstanceID: "i"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	defer func() { _ = ps.Shutdown(ctx) }()
	e := ps.Tailnet("solo").Emitter()

	// counter + By  -> _bytes_total
	e.Counter("tailscale.network.io", "By", "bytes transferred", 1500, nil)
	// counter + annotation unit -> no unit suffix, just _total
	e.Counter("tailscale.config.audit.events", "{event}", "audit events", 3, nil)
	// counter + By where the name already carries the `bytes` token -> deduped
	e.Counter("tailscale2otel.objectstore.bytes", "By", "object bytes", 7, nil)
	// gauge + "1" -> _ratio (even though it is a plain integer count)
	e.Gauge("tailscale.devices.count", "1", "devices", 5, nil)
	// gauge + s -> _seconds, no _total, no _ratio
	e.Gauge("tailscale2otel.api.last_probe", "s", "epoch seconds", 1, nil)
	// up-down counter + annotation unit -> no _total at all
	e.UpDownCounter("tailscale.dns.nameservers", "{record}", "nameservers", 2, nil)
	// histogram + s -> _seconds with the _bucket/_sum/_count family
	e.Histogram("tailscale2otel.export.duration", "s", "export latency", 0.5, []float64{0.1, 1}, nil)

	body := scrape(t, ps)
	types := map[string]string{}
	series := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(line, "# TYPE "); ok {
			f := strings.Fields(rest)
			if len(f) == 2 {
				types[f[0]] = f[1]
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, _ := strings.Cut(line, "{")
		name, _, _ = strings.Cut(name, " ")
		series[name] = true
	}

	// NOTE: in the TEXT exposition a counter's `# TYPE` line carries the
	// _total-suffixed name (client_golang), while a histogram's carries the
	// un-suffixed base with _bucket/_sum/_count only on the samples. Both halves
	// are asserted so a suffix change cannot hide in either.
	wantFamilies := map[string]string{
		"tailscale_network_io_bytes_total":       "counter",
		"tailscale_config_audit_events_total":    "counter",
		"tailscale2otel_objectstore_bytes_total": "counter",
		"tailscale_devices_count_ratio":          "gauge",
		"tailscale2otel_api_last_probe_seconds":  "gauge",
		"tailscale_dns_nameservers":              "gauge",
		"tailscale2otel_export_duration_seconds": "histogram",
	}
	for fam, kind := range wantFamilies {
		if got, ok := types[fam]; !ok {
			t.Errorf("missing metric family %q (want %s); body:\n%s", fam, kind, body)
		} else if got != kind {
			t.Errorf("family %q type = %q, want %q", fam, got, kind)
		}
	}
	// Exact SERIES names. Counters expose the _total-suffixed series; the # TYPE
	// family name above is the un-suffixed base, so both halves matter.
	wantSeries := []string{
		"tailscale_network_io_bytes_total",
		"tailscale_config_audit_events_total",
		"tailscale2otel_objectstore_bytes_total",
		"tailscale_devices_count_ratio",
		"tailscale2otel_api_last_probe_seconds",
		"tailscale_dns_nameservers",
		"tailscale2otel_export_duration_seconds_bucket",
		"tailscale2otel_export_duration_seconds_sum",
		"tailscale2otel_export_duration_seconds_count",
	}
	for _, s := range wantSeries {
		if !series[s] {
			t.Errorf("missing exact series %q; body:\n%s", s, body)
		}
	}
	// Negative assertions: the names the OLD metricdoc.PromName rule predicted,
	// and the suffixes a strategy flip would drop or add.
	for _, bad := range []string{
		"tailscale2otel_objectstore_bytes_bytes_total", // double unit token
		"tailscale_devices_count",                      // _ratio dropped
		"tailscale_devices_count_ratio_total",          // _total on a gauge
		"tailscale_dns_nameservers_total",              // _total on an up-down counter
		"tailscale_dns_nameservers_record",             // annotation unit leaked
		"tailscale2otel_export_duration_seconds_total", // _total on a histogram
		"tailscale_network_io_total",                   // unit suffix dropped
		"tailscale.network.io",                         // NoTranslation strategy
	} {
		if series[bad] || types[bad] != "" {
			t.Errorf("unexpected series/family %q on /metrics; body:\n%s", bad, body)
		}
	}
}
