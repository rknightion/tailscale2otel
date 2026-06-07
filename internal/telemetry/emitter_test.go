package telemetry_test

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// newTestEmitter wires an Emitter to an in-memory metric reader so tests can
// assert exactly what was recorded. Logs go to a no-op logger here.
func newTestEmitter(t *testing.T) (telemetry.Emitter, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	e := telemetry.NewEmitter(mp.Meter("test"), noop.NewLoggerProvider().Logger("test"))
	return e, reader
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return rm
}

func findMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	t.Fatalf("metric %q not found in %d scopes", name, len(rm.ScopeMetrics))
	return metricdata.Metrics{}
}

func attrString(t *testing.T, set attribute.Set, key string) string {
	t.Helper()
	v, ok := set.Value(attribute.Key(key))
	if !ok {
		t.Fatalf("attribute %q not present", key)
	}
	return v.AsString()
}

func TestEmitter_HistogramRecordsBuckets(t *testing.T) {
	e, reader := newTestEmitter(t)

	bounds := []float64{0, 7, 30}
	e.Histogram("test.dist", "d", "desc", -3, bounds, nil) // (-inf,0]
	e.Histogram("test.dist", "d", "desc", 5, bounds, nil)  // (0,7]
	e.Histogram("test.dist", "d", "desc", 50, bounds, nil) // (30,+inf]

	m := findMetric(t, collect(t, reader), "test.dist")
	if m.Unit != "d" {
		t.Fatalf("unit = %q, want %q", m.Unit, "d")
	}
	h, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("data type = %T, want Histogram[float64]", m.Data)
	}
	if len(h.DataPoints) != 1 {
		t.Fatalf("data points = %d, want 1", len(h.DataPoints))
	}
	dp := h.DataPoints[0]
	if dp.Count != 3 {
		t.Errorf("Count = %d, want 3", dp.Count)
	}
	// buckets for bounds [0,7,30] = (-inf,0],(0,7],(7,30],(30,+inf]
	want := []uint64{1, 1, 0, 1}
	if !reflect.DeepEqual(dp.BucketCounts, want) {
		t.Errorf("BucketCounts = %v, want %v", dp.BucketCounts, want)
	}
}

func TestEmitter_CounterRecordsSum(t *testing.T) {
	e, reader := newTestEmitter(t)

	e.Counter("tailscale.network.io", "By", "network bytes transferred", 1500, telemetry.Attrs{
		"network.io.direction": "transmit",
	})

	m := findMetric(t, collect(t, reader), "tailscale.network.io")
	if m.Unit != "By" {
		t.Fatalf("unit = %q, want %q", m.Unit, "By")
	}
	sum, ok := m.Data.(metricdata.Sum[float64])
	if !ok {
		t.Fatalf("data type = %T, want Sum[float64]", m.Data)
	}
	if !sum.IsMonotonic {
		t.Fatal("counter sum should be monotonic")
	}
	if len(sum.DataPoints) != 1 {
		t.Fatalf("got %d data points, want 1", len(sum.DataPoints))
	}
	if sum.DataPoints[0].Value != 1500 {
		t.Fatalf("value = %v, want 1500", sum.DataPoints[0].Value)
	}
	if got := attrString(t, sum.DataPoints[0].Attributes, "network.io.direction"); got != "transmit" {
		t.Fatalf("direction attr = %q, want transmit", got)
	}
}

func TestEmitter_GaugeRecordsValue(t *testing.T) {
	e, reader := newTestEmitter(t)

	e.Gauge("tailscale.device.online", "1", "device connected to control", 1, telemetry.Attrs{
		"host.name": "laptop",
	})

	m := findMetric(t, collect(t, reader), "tailscale.device.online")
	g, ok := m.Data.(metricdata.Gauge[float64])
	if !ok {
		t.Fatalf("data type = %T, want Gauge[float64]", m.Data)
	}
	if len(g.DataPoints) != 1 {
		t.Fatalf("got %d data points, want 1", len(g.DataPoints))
	}
	if g.DataPoints[0].Value != 1 {
		t.Fatalf("value = %v, want 1", g.DataPoints[0].Value)
	}
	if got := attrString(t, g.DataPoints[0].Attributes, "host.name"); got != "laptop" {
		t.Fatalf("host.name attr = %q, want laptop", got)
	}
}

func TestEmitter_UpDownCounterIsNonMonotonic(t *testing.T) {
	e, reader := newTestEmitter(t)

	e.UpDownCounter("tailscale.test.updown", "1", "", 5, nil)

	m := findMetric(t, collect(t, reader), "tailscale.test.updown")
	sum, ok := m.Data.(metricdata.Sum[float64])
	if !ok {
		t.Fatalf("data type = %T, want Sum[float64]", m.Data)
	}
	if sum.IsMonotonic {
		t.Fatal("up/down counter sum should be non-monotonic")
	}
	if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 5 {
		t.Fatalf("data points = %+v, want single value 5", sum.DataPoints)
	}
}

// recordingLogExporter captures emitted log records for assertions.
type recordingLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *recordingLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range recs {
		e.records = append(e.records, recs[i].Clone())
	}
	return nil
}
func (e *recordingLogExporter) Shutdown(context.Context) error   { return nil }
func (e *recordingLogExporter) ForceFlush(context.Context) error { return nil }

func (e *recordingLogExporter) all() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdklog.Record(nil), e.records...)
}

func logAttrs(r sdklog.Record) map[string]string {
	out := map[string]string{}
	r.WalkAttributes(func(kv log.KeyValue) bool {
		out[kv.Key] = kv.Value.AsString()
		return true
	})
	return out
}

func TestEmitter_LogEventSetsBodySeverityAndEventName(t *testing.T) {
	exp := &recordingLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })
	mp := sdkmetric.NewMeterProvider()
	e := telemetry.NewEmitter(mp.Meter("test"), lp.Logger("test"))

	e.LogEvent(telemetry.Event{
		Name:     "tailscale.network.flow",
		Body:     "tcp virtual 100.64.0.1:443 -> 100.64.0.2:51820",
		Severity: telemetry.SeverityWarn,
		Attrs:    telemetry.Attrs{"network.transport": "tcp"},
	})

	recs := exp.all()
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	r := recs[0]
	if got := r.Body().AsString(); got != "tcp virtual 100.64.0.1:443 -> 100.64.0.2:51820" {
		t.Fatalf("body = %q", got)
	}
	if r.Severity() != log.SeverityWarn {
		t.Fatalf("severity = %v, want Warn", r.Severity())
	}
	if r.SeverityText() != "WARN" {
		t.Fatalf("severity text = %q, want WARN", r.SeverityText())
	}
	if got := r.EventName(); got != "tailscale.network.flow" {
		t.Fatalf("EventName() = %q, want tailscale.network.flow", got)
	}
	attrs := logAttrs(r)
	if _, ok := attrs["event.name"]; ok {
		t.Fatalf("event.name must be the native EventName, not an attribute; got attr %q", attrs["event.name"])
	}
	if attrs["network.transport"] != "tcp" {
		t.Fatalf("network.transport attr = %q, want tcp", attrs["network.transport"])
	}
}

func TestHistogramCtxAttachesExemplar(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	// Default MeterProvider uses the trace-based exemplar filter, so a recording
	// under a sampled span context will attach an exemplar.
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	e := telemetry.NewEmitter(mp.Meter("test"), noop.NewLoggerProvider().Logger("test"))

	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x02},
		SpanID:     trace.SpanID{0x02},
		TraceFlags: trace.FlagsSampled,
	}))
	e.HistogramCtx(ctx, "h.exemplar", "s", "desc", 0.5, []float64{1}, telemetry.Attrs{"k": "v"})

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
		t.Errorf("got %d exemplar(s); want 1 via HistogramCtx", exemplars)
	}
}
