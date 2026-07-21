package telemetry

import (
	"testing"

	"go.opentelemetry.io/otel/log/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// TestEmitStats_CountsEveryInstrument asserts the emit-boundary counters tally one
// data point per synchronous instrument call, len(points) per GaugeSnapshot, and
// one record per log event — the input to the status page's throughput trend.
func TestEmitStats_CountsEveryInstrument(t *testing.T) {
	e, _ := newReaderEmitter(t, nil)

	if got := e.EmitStats(); got.MetricPoints != 0 || got.LogRecords != 0 {
		t.Fatalf("fresh emitter EmitStats = %+v, want zero", got)
	}

	e.Counter("c", "1", "d", 1, nil)
	e.Gauge("g", "1", "d", 1, nil)
	e.UpDownCounter("u", "1", "d", 1, nil)
	e.Histogram("h", "s", "d", 1, []float64{1}, nil)
	e.GaugeSnapshot("s", "1", "d", []GaugePoint{
		{Value: 1, Attrs: Attrs{"k": "a"}},
		{Value: 2, Attrs: Attrs{"k": "b"}},
	})
	e.LogEvent(Event{Name: "x", Body: "b"})
	e.LogEvent(Event{Name: "y", Body: "b"})

	got := e.EmitStats()
	if want := uint64(6); got.MetricPoints != want { // 4 synchronous + 2 snapshot points
		t.Errorf("MetricPoints = %d, want %d", got.MetricPoints, want)
	}
	if want := uint64(2); got.LogRecords != want {
		t.Errorf("LogRecords = %d, want %d", got.LogRecords, want)
	}
}

// TestEmitStats_HistogramCountedOnce asserts Histogram (which delegates to
// HistogramCtx) records exactly one point, not two.
func TestEmitStats_HistogramCountedOnce(t *testing.T) {
	e, _ := newReaderEmitter(t, nil)
	e.Histogram("h", "s", "d", 1, []float64{1}, nil)
	if got := e.EmitStats().MetricPoints; got != 1 {
		t.Errorf("MetricPoints after one Histogram = %d, want 1", got)
	}
}

// TestProviderEmitStats asserts the accessor is reachable from a Provider, since
// the app aggregates throughput across the process + per-tailnet providers.
func TestProviderEmitStats(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	p := &Provider{emitter: newOtelEmitter(mp.Meter("test"), noop.NewLoggerProvider().Logger("test"), nil, nil, nil, nil, nil)}
	p.emitter.Counter("c", "1", "d", 1, nil)
	if got := p.EmitStats(); got.MetricPoints != 1 {
		t.Errorf("Provider.EmitStats().MetricPoints = %d, want 1", got.MetricPoints)
	}
}
