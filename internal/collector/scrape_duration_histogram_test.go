package collector_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/log/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// TestSelfObs_ScrapeDurationHistogramEmittedAlongsideGauge verifies that a
// scrape emits BOTH the pre-existing tailscale2otel.scrape.duration gauge
// (unchanged, for compatibility) AND a new explicit-bucket histogram, so a
// slow scrape gets a distribution (and, via HistogramCtx, exemplars) without
// breaking anything that already reads the gauge.
func TestSelfObs_ScrapeDurationHistogramEmittedAlongsideGauge(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "ok", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		return nil
	}}, time.Millisecond)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now)

	waitFor(t, func() bool {
		_, ok := findPoint(rec, collector.MetricScrapeDurationHistogram, "ok")
		return ok
	}, 2*time.Second)

	// The existing gauge must still be emitted, unchanged.
	dur, ok := findPoint(rec, collector.MetricScrapeDuration, "ok")
	if !ok {
		t.Fatalf("%s not emitted", collector.MetricScrapeDuration)
	}
	if dur.Kind != "gauge" || dur.Unit != semconv.UnitSeconds {
		t.Fatalf("duration gauge = %+v, want gauge in seconds", dur)
	}

	hist, ok := findPoint(rec, collector.MetricScrapeDurationHistogram, "ok")
	if !ok {
		t.Fatalf("%s not emitted", collector.MetricScrapeDurationHistogram)
	}
	if hist.Kind != "histogram" {
		t.Fatalf("scrape duration histogram kind = %q, want histogram", hist.Kind)
	}
	if hist.Unit != semconv.UnitSeconds {
		t.Fatalf("scrape duration histogram unit = %q, want %q", hist.Unit, semconv.UnitSeconds)
	}
	if hist.Count < 1 {
		t.Fatalf("scrape duration histogram count = %d, want >= 1", hist.Count)
	}
	if len(hist.Bounds) == 0 {
		t.Fatalf("scrape duration histogram has no bucket bounds")
	}
	// Bounds must cover both normal (sub-second) and overrun (multi-minute,
	// matching the longest default collector interval of 600s) durations.
	if hist.Bounds[0] >= 1 {
		t.Fatalf("scrape duration histogram bounds = %v, want a sub-second lower bound for normal scrapes", hist.Bounds)
	}
	if hist.Bounds[len(hist.Bounds)-1] < 300 {
		t.Fatalf("scrape duration histogram bounds = %v, want an upper bound >= 300s to cover overrun scrapes", hist.Bounds)
	}
	// Cardinality must stay bounded to the same collector attribute the gauge
	// uses -- nothing per-device/per-entity.
	if len(hist.Attrs) != 1 || hist.Attrs[semconv.AttrCollector] != "ok" {
		t.Fatalf("scrape duration histogram attrs = %+v, want only {%s: ok}", hist.Attrs, semconv.AttrCollector)
	}
}

// TestSelfObs_ScrapeDurationHistogramCarriesExemplar verifies that the
// histogram is recorded with the scrape's span context (HistogramCtx, not the
// context-free Histogram), so the SDK's trace-based exemplar filter attaches
// an exemplar pointing at the scrape span. This drives the real scheduler tick
// (RunTick, exported for tests) against a real metric SDK + a real, always-on
// sampling tracer, mirroring internal/telemetry's own
// TestHistogramCtxAttachesExemplar but exercising it end-to-end through the
// collector package.
func TestSelfObs_ScrapeDurationHistogramCarriesExemplar(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	e := telemetry.NewEmitter(mp.Meter("test"), noop.NewLoggerProvider().Logger("test"))

	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	s := collector.NewScheduler(e, collector.NewMemoryStore(),
		collector.WithTracer(tp.Tracer("test")),
		collector.WithStaggerWindow(0),
	)
	last := time.Now()
	s.RunTick(context.Background(),
		collector.Entry{Collector: fakeOK("dev"), Interval: time.Minute},
		&last)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	exemplars := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != collector.MetricScrapeDurationHistogram {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s data = %T, want Histogram[float64]", collector.MetricScrapeDurationHistogram, m.Data)
			}
			for _, dp := range h.DataPoints {
				exemplars += len(dp.Exemplars)
			}
		}
	}
	if exemplars != 1 {
		t.Errorf("got %d exemplar(s) on %s; want 1 via HistogramCtx", exemplars, collector.MetricScrapeDurationHistogram)
	}
}
