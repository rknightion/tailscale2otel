package telemetry

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	tracetest "go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// TestBuildResourceOmitsTailnetProvider asserts buildResource no longer carries
// tailscale.tailnet / tailscale2otel.provider on the Resource. Roadmap item L moved
// these to signal-scoped attributes (metric data points, log records, spans) so they
// are real, joinless labels on every backend rather than target_info-only.
func TestBuildResourceOmitsTailnetProvider(t *testing.T) {
	res, err := buildResource(context.Background(), Options{ServiceName: "svc", TailnetName: "alpha", Provider: "tailscale"})
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	for _, kv := range res.Attributes() {
		if string(kv.Key) == "tailscale.tailnet" || string(kv.Key) == "tailscale2otel.provider" {
			t.Errorf("Resource still carries %s (item L moved it to a signal attr)", kv.Key)
		}
	}
}

// TestConstLabelAttrs pins constLabelAttrs: it returns the provider-scoped
// attributes (tailnet, provider) each only when non-empty, and nil when both empty.
func TestConstLabelAttrs(t *testing.T) {
	got := constLabelAttrs(Options{TailnetName: "alpha", Provider: "tailscale"})
	if !hasAttr(got, "tailscale.tailnet", "alpha") || !hasAttr(got, "tailscale2otel.provider", "tailscale") {
		t.Errorf("both set: %v", got)
	}
	got = constLabelAttrs(Options{Provider: "tailscale"})
	if hasAttr(got, "tailscale.tailnet", "") || len(got) != 1 || !hasAttr(got, "tailscale2otel.provider", "tailscale") {
		t.Errorf("provider-only: %v", got)
	}
	if got := constLabelAttrs(Options{}); got != nil {
		t.Errorf("empty = %v, want nil", got)
	}
}

// TestEmitterStampsConstAttrsOnMetricDataPoint asserts the const attrs built from a
// per-tailnet provider's Options end up on an actually-recorded metric data point
// (tailnet + provider), while a process provider's metric carries provider only (no
// tailnet). This drives the real Gauge -> buildAttrs -> SDK path against a
// ManualReader, not just constLabelAttrs in isolation.
func TestEmitterStampsConstAttrsOnMetricDataPoint(t *testing.T) {
	emit := func(opts Options) attribute.Set {
		t.Helper()
		reader := sdkmetric.NewManualReader()
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
		e := newOtelEmitter(mp.Meter("test"), lognoop.NewLoggerProvider().Logger("test"),
			nil, nil, nil, nil, constLabelAttrs(opts))
		e.Gauge("tailscale.devices.count", "1", "devices", 3, Attrs{"k": "v"})
		return collectAttrs(t, reader, "tailscale.devices.count")
	}

	// Per-tailnet provider: data point carries the source attr plus tailnet+provider.
	tn := emit(Options{TailnetName: "alpha", Provider: "tailscale"})
	if v, ok := tn.Value(attribute.Key("k")); !ok || v.AsString() != "v" {
		t.Errorf("per-tailnet point missing source attr k=v; set=%v", tn)
	}
	if v, ok := tn.Value(attribute.Key("tailscale.tailnet")); !ok || v.AsString() != "alpha" {
		t.Errorf("per-tailnet point missing tailscale.tailnet=alpha; set=%v", tn)
	}
	if v, ok := tn.Value(attribute.Key("tailscale2otel.provider")); !ok || v.AsString() != "tailscale" {
		t.Errorf("per-tailnet point missing tailscale2otel.provider=tailscale; set=%v", tn)
	}

	// Process provider (no tailnet): provider only, never a tailnet label.
	proc := emit(Options{Provider: "tailscale"})
	if v, ok := proc.Value(attribute.Key("tailscale2otel.provider")); !ok || v.AsString() != "tailscale" {
		t.Errorf("process point missing tailscale2otel.provider=tailscale; set=%v", proc)
	}
	if _, ok := proc.Value(attribute.Key("tailscale.tailnet")); ok {
		t.Errorf("process point must not carry tailscale.tailnet; set=%v", proc)
	}
}

// TestTLSConfigPinsMinVersionTLS12 guards the OTLP exporter client TLS config
// against the semgrep missing-ssl-minversion finding: when CA/cert/key files
// configure a *tls.Config, it must floor the negotiated version at TLS 1.2 so a
// downgrade to TLS 1.0/1.1 is never possible. 1.2 (not 1.3) is the deliberate
// floor for proxy / self-signed interop.
func TestTLSConfigPinsMinVersionTLS12(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	cfg, err := tlsConfig(Options{CAFile: caFile})
	if err != nil {
		t.Fatalf("tlsConfig returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("tlsConfig returned nil; expected a config when CAFile is set")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("cfg.MinVersion = %#x, want tls.VersionTLS12 (%#x)", cfg.MinVersion, tls.VersionTLS12)
	}
}

// TestCumulativeTemporalitySelectorAlwaysCumulative pins the OTLP metric
// temporality. Grafana Cloud / Mimir OTLP ingestion accepts CUMULATIVE only
// (delta is rejected with HTTP 400 and there is no server-side delta->cumulative
// conversion), so the selector must return cumulative for EVERY instrument kind
// — a future SDK default change must never silently switch us to delta.
func TestCumulativeTemporalitySelectorAlwaysCumulative(t *testing.T) {
	kinds := []sdkmetric.InstrumentKind{
		sdkmetric.InstrumentKindCounter,
		sdkmetric.InstrumentKindUpDownCounter,
		sdkmetric.InstrumentKindHistogram,
		sdkmetric.InstrumentKindGauge,
		sdkmetric.InstrumentKindObservableCounter,
		sdkmetric.InstrumentKindObservableUpDownCounter,
		sdkmetric.InstrumentKindObservableGauge,
	}
	for _, k := range kinds {
		if got := cumulativeTemporalitySelector(k); got != metricdata.CumulativeTemporality {
			t.Errorf("cumulativeTemporalitySelector(%v) = %v, want CumulativeTemporality", k, got)
		}
	}
}

// TestBuildResourceEnrichesAndMergesCleanly is the regression guard for the
// resource enrichment: combining schemaless WithAttributes (service.*) with the
// core host/os/process detectors (all sharing one semconv schema URL) must NOT
// raise a schema-URL conflict, and the resulting resource must carry the
// instance identity plus host/os/process attributes used to distinguish
// instances in Grafana. A partial-resource error (e.g. a detector that can't
// read its source in CI) is tolerated; a hard error is not.
func TestBuildResourceEnrichesAndMergesCleanly(t *testing.T) {
	res, err := buildResource(context.Background(), Options{
		ServiceName:    "tailscale2otel",
		ServiceVersion: "test",
		InstanceID:     "inst-42",
	})
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		t.Fatalf("buildResource returned a non-partial error: %v", err)
	}
	if res == nil {
		t.Fatal("buildResource returned nil resource")
	}
	if res.SchemaURL() == "" {
		t.Error("resource SchemaURL is empty; expected the semconv schema URL from the detectors")
	}

	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got["service.instance.id"] != "inst-42" {
		t.Errorf("service.instance.id = %q, want inst-42", got["service.instance.id"])
	}
	for _, key := range []string{
		"service.name",
		"host.name",
		"os.type",
		"process.pid",
		"process.executable.name",
		"process.runtime.name",
		"process.runtime.version",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("resource missing attribute %q (have: %v)", key, got)
		}
	}
}

// TestOTLPHTTPURL pins the OTLP/HTTP per-signal URL construction. The OTEL Go
// otlphttp exporter's WithEndpointURL uses the URL path AS-IS (it does not
// append /v1/<signal>), so a Grafana Cloud base endpoint like ".../otlp" must
// have the signal path appended or the gateway returns 404.
func TestOTLPHTTPURL(t *testing.T) {
	cases := []struct {
		base, signal, want string
	}{
		{"https://otlp-gateway-prod-gb-south-1.grafana.net/otlp", "metrics", "https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/v1/metrics"},
		{"https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/", "logs", "https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/v1/logs"},
		{"https://x/otlp/v1/metrics", "metrics", "https://x/otlp/v1/metrics"}, // already signal-specific: no double-append
		{"http://collector:4318", "metrics", "http://collector:4318/v1/metrics"},
	}
	for _, c := range cases {
		if got := otlpHTTPURL(c.base, c.signal); got != c.want {
			t.Errorf("otlpHTTPURL(%q, %q) = %q, want %q", c.base, c.signal, got, c.want)
		}
	}
}

// TestMeterProviderEnablesExemplarsWhenTracing is the mirror of
// TestMeterProviderDisablesExemplars: with tracing on, metricProviderOptions
// must use the trace-based exemplar filter so a Float64Histogram recorded under
// a SAMPLED span context attaches exactly one exemplar. Histograms are the only
// instrument kind recorded under a real span context in this app (e.g.
// api.duration via HistogramCtx), so they must keep default exemplar reservoirs.
func TestMeterProviderEnablesExemplarsWhenTracing(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(append(
		metricProviderOptions(resource.Empty(), 10000, true),
		sdkmetric.WithReader(reader),
	)...)
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	hist, err := mp.Meter("test").Float64Histogram(
		"t.exemplar.histogram",
		metric.WithExplicitBucketBoundaries(0, 5, 10, 25, 50, 100),
	)
	if err != nil {
		t.Fatalf("Float64Histogram: %v", err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01},
		SpanID:     trace.SpanID{0x01},
		TraceFlags: trace.FlagsSampled,
	}))
	hist.Record(ctx, 42.0, metric.WithAttributes(attribute.String("k", "v")))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	exemplars := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if h, ok := m.Data.(metricdata.Histogram[float64]); ok {
				for _, dp := range h.DataPoints {
					exemplars += len(dp.Exemplars)
				}
			}
		}
	}
	if exemplars != 1 {
		t.Errorf("got %d exemplar(s) on histogram; want 1 (histograms must keep trace exemplar reservoirs when tracing is on)", exemplars)
	}
}

// TestMeterProviderDropsExemplarsForSyncCountersWhenTracing asserts that when
// tracing is enabled, synchronous Counter, UpDownCounter, and Gauge instruments
// produce ZERO exemplars even under a SAMPLED span context. These instruments
// are always recorded with context.Background() in the app (via Counter/Gauge/
// UpDownCounter on the Emitter), so their per-series reservoirs can never capture
// an exemplar — the no-op reservoir eliminates that dead-weight heap allocation.
// The test also verifies aggregation is unaffected: the recorded values still land
// in the data points correctly.
func TestMeterProviderDropsExemplarsForSyncCountersWhenTracing(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(append(
		metricProviderOptions(resource.Empty(), 10000, true),
		sdkmetric.WithReader(reader),
	)...)
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	m := mp.Meter("test")

	ctr, err := m.Int64Counter("t.noop.counter")
	if err != nil {
		t.Fatalf("Int64Counter: %v", err)
	}
	udctr, err := m.Int64UpDownCounter("t.noop.updowncounter")
	if err != nil {
		t.Fatalf("Int64UpDownCounter: %v", err)
	}
	gauge, err := m.Float64Gauge("t.noop.gauge")
	if err != nil {
		t.Fatalf("Float64Gauge: %v", err)
	}

	// Record under a fully sampled span context — the exact condition that the
	// default TraceBasedFilter WOULD capture into an exemplar.
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01},
		SpanID:     trace.SpanID{0x01},
		TraceFlags: trace.FlagsSampled,
	}))
	attrs := metric.WithAttributes(attribute.String("k", "v"))
	ctr.Add(ctx, 1, attrs)
	udctr.Add(ctx, 1, attrs)
	gauge.Record(ctx, 3.14, attrs)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	type result struct {
		exemplars int
		value     float64
	}
	results := map[string]result{}
	for _, sm := range rm.ScopeMetrics {
		for _, met := range sm.Metrics {
			switch d := met.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					r := results[met.Name]
					r.exemplars += len(dp.Exemplars)
					r.value += float64(dp.Value)
					results[met.Name] = r
				}
			case metricdata.Gauge[float64]:
				for _, dp := range d.DataPoints {
					r := results[met.Name]
					r.exemplars += len(dp.Exemplars)
					r.value = dp.Value
					results[met.Name] = r
				}
			}
		}
	}

	checks := []struct {
		name      string
		wantValue float64
	}{
		{"t.noop.counter", 1},
		{"t.noop.updowncounter", 1},
		{"t.noop.gauge", 3.14},
	}
	for _, c := range checks {
		r, ok := results[c.name]
		if !ok {
			t.Errorf("metric %q not found in collected output", c.name)
			continue
		}
		if r.exemplars != 0 {
			t.Errorf("metric %q: got %d exemplar(s); want 0 (no-op reservoir must suppress exemplars for sync counters/gauges when tracing is on)", c.name, r.exemplars)
		}
		if r.value != c.wantValue {
			t.Errorf("metric %q: value = %v, want %v (aggregation must be unaffected by exemplar suppression)", c.name, r.value, c.wantValue)
		}
	}
}

// TestMeterProviderDisablesExemplars guards that metrics run with exemplars OFF.
// The app configures no TracerProvider, so the SDK's default trace-based exemplar
// filter would allocate a reservoir per series that can never be populated (there
// are no spans) yet is still walked and serialized on every export — pure
// dead-weight allocation/CPU. metricProviderOptions must pin
// exemplar.AlwaysOffFilter, so even a measurement recorded under a SAMPLED span
// context — the one case the default filter WOULD capture — attaches no exemplar.
func TestMeterProviderDisablesExemplars(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(append(
		metricProviderOptions(resource.Empty(), 10000, false),
		sdkmetric.WithReader(reader),
	)...)
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	ctr, err := mp.Meter("test").Int64Counter("t.exemplar.probe")
	if err != nil {
		t.Fatalf("Int64Counter: %v", err)
	}

	// A valid, sampled span context is exactly what the default TraceBasedFilter
	// samples into an exemplar; with AlwaysOffFilter it must be ignored.
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01},
		SpanID:     trace.SpanID{0x01},
		TraceFlags: trace.FlagsSampled,
	}))
	ctr.Add(ctx, 1, metric.WithAttributes(attribute.String("k", "v")))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	exemplars := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				exemplars += len(dp.Exemplars)
			}
		}
	}
	if exemplars != 0 {
		t.Errorf("got %d metric exemplar(s); want 0 (exemplars must be disabled — no TracerProvider exists to populate them)", exemplars)
	}
}

func TestConstAttrSpanProcessorStampsSpans(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSpanProcessor(constAttrSpanProcessor{attrs: []attribute.KeyValue{
			attribute.String("tailscale.tailnet", "alpha"),
			attribute.String("tailscale2otel.provider", "tailscale"),
		}}),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	_, span := tp.Tracer("test").Start(context.Background(), "op")
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	want := map[string]string{"tailscale.tailnet": "alpha", "tailscale2otel.provider": "tailscale"}
	for _, a := range spans[0].Attributes {
		if v, ok := want[string(a.Key)]; ok && a.Value.AsString() == v {
			delete(want, string(a.Key))
		}
	}
	if len(want) != 0 {
		t.Errorf("span missing const attrs: %v; got %v", want, spans[0].Attributes)
	}
}
