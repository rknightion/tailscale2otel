package telemetry

import (
	"context"
	"errors"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestCountingMetricExporterCountsDataPoints(t *testing.T) {
	inner := &fakeMetricExporter{}
	c := newCountingMetricExporter(inner)

	rm := &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{
		Metrics: []metricdata.Metrics{
			{Data: metricdata.Sum[float64]{DataPoints: make([]metricdata.DataPoint[float64], 3)}},
			{Data: metricdata.Gauge[int64]{DataPoints: make([]metricdata.DataPoint[int64], 2)}},
			{Data: metricdata.Histogram[float64]{DataPoints: make([]metricdata.HistogramDataPoint[float64], 1)}},
		},
	}}}
	if err := c.Export(context.Background(), rm); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := c.Export(context.Background(), rm); err != nil { // cumulative
		t.Fatalf("Export: %v", err)
	}
	if got := c.count(); got != 12 { // (3+2+1) * 2
		t.Errorf("datapoints = %d, want 12", got)
	}
	if inner.calls != 2 {
		t.Errorf("inner.Export calls = %d, want 2 (must delegate)", inner.calls)
	}
}

func TestCountingLogExporterCountsRecords(t *testing.T) {
	inner := &fakeLogExporter{}
	c := newCountingLogExporter(inner)
	if err := c.Export(context.Background(), make([]sdklog.Record, 4)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if got := c.count(); got != 4 {
		t.Errorf("log records = %d, want 4", got)
	}
	if inner.calls != 1 {
		t.Errorf("inner.Export calls = %d, want 1", inner.calls)
	}
}

// fakeMetricExporter implements sdkmetric.Exporter; only Export is exercised.
type fakeMetricExporter struct{ calls int }

func (f *fakeMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (f *fakeMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}
func (f *fakeMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	f.calls++
	return nil
}
func (f *fakeMetricExporter) ForceFlush(context.Context) error { return nil }
func (f *fakeMetricExporter) Shutdown(context.Context) error   { return nil }

type fakeLogExporter struct{ calls int }

func (f *fakeLogExporter) Export(context.Context, []sdklog.Record) error { f.calls++; return nil }
func (f *fakeLogExporter) ForceFlush(context.Context) error              { return nil }
func (f *fakeLogExporter) Shutdown(context.Context) error                { return nil }

// fakeErrMetricExporter is a no-op sdkmetric.Exporter whose Export returns errOut.
type fakeErrMetricExporter struct{ errOut error }

func (f *fakeErrMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (f *fakeErrMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}
func (f *fakeErrMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	return f.errOut
}
func (f *fakeErrMetricExporter) ForceFlush(context.Context) error { return nil }
func (f *fakeErrMetricExporter) Shutdown(context.Context) error   { return nil }

// fakeErrLogExporter is a no-op sdklog.Exporter whose Export returns errOut.
type fakeErrLogExporter struct{ errOut error }

func (f *fakeErrLogExporter) Export(context.Context, []sdklog.Record) error { return f.errOut }
func (f *fakeErrLogExporter) ForceFlush(context.Context) error              { return nil }
func (f *fakeErrLogExporter) Shutdown(context.Context) error                { return nil }

func TestCountingMetricExporterObservesDuration(t *testing.T) {
	var gotSignal, gotOutcome string
	var gotSeconds float64
	var called int
	c := newCountingMetricExporter(&fakeErrMetricExporter{})
	c.setObserver(func(signal, outcome string, seconds float64) {
		gotSignal, gotOutcome, gotSeconds = signal, outcome, seconds
		called++
	})
	if err := c.Export(context.Background(), &metricdata.ResourceMetrics{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if called != 1 {
		t.Fatalf("observer called %d times, want 1", called)
	}
	if gotSignal != "metrics" {
		t.Errorf("signal = %q, want metrics", gotSignal)
	}
	if gotOutcome != "success" {
		t.Errorf("outcome = %q, want success", gotOutcome)
	}
	if gotSeconds < 0 {
		t.Errorf("seconds = %v, want >= 0", gotSeconds)
	}
}

func TestCountingMetricExporterObservesFailure(t *testing.T) {
	var gotOutcome string
	var called int
	c := newCountingMetricExporter(&fakeErrMetricExporter{errOut: errors.New("boom")})
	c.setObserver(func(_, outcome string, _ float64) { gotOutcome = outcome; called++ })
	if err := c.Export(context.Background(), &metricdata.ResourceMetrics{}); err == nil {
		t.Fatal("Export: want error, got nil")
	}
	if called != 1 {
		t.Fatalf("observer called %d times, want 1", called)
	}
	if gotOutcome != "failure" {
		t.Errorf("outcome = %q, want failure", gotOutcome)
	}
}

func TestCountingMetricExporterNilObserver(t *testing.T) {
	c := newCountingMetricExporter(&fakeErrMetricExporter{})
	if err := c.Export(context.Background(), &metricdata.ResourceMetrics{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
}

func TestCountingLogExporterObservesDuration(t *testing.T) {
	var signal, outcome string
	var called int
	c := newCountingLogExporter(&fakeErrLogExporter{})
	c.setObserver(func(s, o string, _ float64) { signal, outcome = s, o; called++ })
	if err := c.Export(context.Background(), nil); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if called != 1 || signal != "logs" || outcome != "success" {
		t.Fatalf("got called=%d signal=%q outcome=%q, want 1/logs/success", called, signal, outcome)
	}
}

func TestCountingLogExporterObservesFailure(t *testing.T) {
	var outcome string
	c := newCountingLogExporter(&fakeErrLogExporter{errOut: errors.New("boom")})
	c.setObserver(func(_, o string, _ float64) { outcome = o })
	if err := c.Export(context.Background(), nil); err == nil {
		t.Fatal("want error")
	}
	if outcome != "failure" {
		t.Errorf("outcome = %q, want failure", outcome)
	}
}
